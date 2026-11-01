// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	batchv1 "k8s.io/api/batch/v1"
)

// Trigger names what caused a build dispatch. It is carried on the dispatch
// log line so the two entry points are distinguishable from logs alone: a
// `trigger=webhook` line proves the webhook path fired, `trigger=poll` proves
// the 15m backstop recovered a delivery the webhook missed. A criterion that
// only asks "was a build dispatched" cannot tell those apart, and the backstop
// would silently satisfy the webhook criterion.
type Trigger string

const (
	// TriggerWebhook is a dispatch caused by a signature-verified push delivery.
	TriggerWebhook Trigger = "webhook"
	// TriggerPoll is a dispatch caused by the periodic backstop scan.
	TriggerPoll Trigger = "poll"
)

// TagRefPrefix is the ref prefix GitHub uses for tag pushes. The dispatch log
// line echoes the full ref (`refs/tags/v1.2.3`) so an operator can grep the
// same string the webhook payload carried.
const TagRefPrefix = "refs/tags/"

// Request identifies one build to dispatch: the repo and the bare tag.
type Request struct {
	// Repo is the host-qualified repo key ("owner/name") the watcher uses
	// everywhere else.
	Repo string
	// Tag is the bare tag name ("v1.2.3"), never the full ref.
	Tag string
}

// Validate rejects a request that cannot name a build. An empty repo or tag
// would render a manifest with unsubstituted-looking placeholders and create a
// Job nobody can trace back to a release.
func (r Request) Validate(ctx context.Context) error {
	if r.Repo == "" {
		return errors.Errorf(ctx, "build request missing repo")
	}
	if r.Tag == "" {
		return errors.Errorf(ctx, "build request missing tag for repo=%s", r.Repo)
	}
	return nil
}

// Ref returns the full git ref the request's tag corresponds to.
func (r Request) Ref() string {
	return TagRefPrefix + r.Tag
}

//counterfeiter:generate -o ../../mocks/build_dispatcher.go --fake-name BuildDispatcher . Dispatcher

// Dispatcher dispatches at most one build per (repo, tag).
//
// Implementations must be idempotent: the webhook path and the poll backstop
// routinely see the same tag, and a re-dispatch must not create a second Job
// nor fail the cycle.
type Dispatcher interface {
	// Dispatch runs the pre-flight gates and, when they pass, creates the
	// build Job. A skip (no auto-build opt-in, no manifest, image already
	// present, registry unreachable) is a nil error — the cycle succeeded,
	// it simply built nothing.
	Dispatch(ctx context.Context, req Request, trigger Trigger) error
}

//counterfeiter:generate -o ../../mocks/build_manifest_loader.go --fake-name BuildManifestLoader . ManifestLoader

// ManifestLoader returns the raw bytes of a repo's `hack/build-job.yaml` at
// the given tag, or nil when the file does not exist. A nil slice means "this
// repo has not opted into an in-repo build recipe", which is a skip, not an
// error.
type ManifestLoader interface {
	LoadBuildJobManifest(ctx context.Context, repo, tag string) ([]byte, error)
}

//counterfeiter:generate -o ../../mocks/build_job_creator.go --fake-name BuildJobCreator . JobCreator

// JobCreator creates one Job in the cluster.
type JobCreator interface {
	// Create creates the Job. It returns an error satisfying
	// ErrJobAlreadyExists when a Job of that name is already present, which
	// the dispatcher treats as a successful no-op.
	Create(ctx context.Context, job batchv1.Job) error
}

// ErrJobAlreadyExists reports that a Job of the requested name already exists.
var ErrJobAlreadyExists = stderrors.New("build job already exists")

//counterfeiter:generate -o ../../mocks/build_image_checker.go --fake-name BuildImageChecker . ImageChecker

// ImageChecker reports whether an image reference is already published.
//
// The error return is load-bearing: it is what the fail-closed rule is built
// on. An implementation that cannot reach the registry MUST return an error,
// never (false, nil) — "I could not tell" must not read as "it is not there",
// because that is the path that re-pushes an existing tag.
type ImageChecker interface {
	Exists(ctx context.Context, imageRef string) (bool, error)
}

//counterfeiter:generate -o ../../mocks/build_auto_build_gate.go --fake-name BuildAutoBuildGate . AutoBuildGate

// AutoBuildGate reports whether a repo opted into automatic builds.
//
// The gate is opt-in: an absent or false `dockerBuild.autoBuild` in
// `.maintainer.yaml` means the repo is not built. Without it every tag of every
// allowlisted repo would fire a build.
type AutoBuildGate interface {
	Enabled(ctx context.Context, repo string) (bool, error)
}

//counterfeiter:generate -o ../../mocks/build_metrics.go --fake-name BuildMetrics . Metrics

// Metrics is the narrow slice of watcher metrics the dispatcher records.
//
// IncFilterSkipped carries the two design-mandated reasons
// ("image_exists" | "registry_unreachable") — the same counter the poll
// filters already use, so one query answers "what did the watcher skip and
// why" across both paths.
type Metrics interface {
	// IncFilterSkipped — reason: "image_exists" | "registry_unreachable"
	IncFilterSkipped(reason string)
	// IncBuildDispatched — result: "created" | "exists" | "skipped" | "error"
	IncBuildDispatched(result string)
}

// NewDispatcher wires the dispatch pipeline.
func NewDispatcher(
	manifests ManifestLoader,
	jobs JobCreator,
	images ImageChecker,
	gate AutoBuildGate,
	metrics Metrics,
) Dispatcher {
	return &dispatcher{
		manifests: manifests,
		jobs:      jobs,
		images:    images,
		gate:      gate,
		metrics:   metrics,
	}
}

type dispatcher struct {
	manifests ManifestLoader
	jobs      JobCreator
	images    ImageChecker
	gate      AutoBuildGate
	metrics   Metrics
}

// Dispatch implements Dispatcher.
//
// Order matters and mirrors the design record: opt-in gate, manifest, render,
// registry pre-flight, create. The registry check sits *after* rendering
// because the image reference it needs is only knowable once the tag has been
// substituted into the repo's own Job spec — asking the caller to know the
// image name up front would duplicate the recipe.
func (d *dispatcher) Dispatch(ctx context.Context, req Request, trigger Trigger) error {
	if err := req.Validate(ctx); err != nil {
		return err
	}
	job, imageRef, proceed, err := d.prepare(ctx, req, trigger)
	if err != nil || !proceed {
		return err
	}
	return d.create(ctx, req, job, imageRef, trigger)
}

// prepare runs the gates that decide whether a build should happen at all:
// the opt-in, the manifest, the render, and the registry pre-flight. It
// returns proceed=false when a gate legitimately says "build nothing" — that
// is a successful cycle, not an error.
func (d *dispatcher) prepare(
	ctx context.Context,
	req Request,
	trigger Trigger,
) (batchv1.Job, string, bool, error) {
	enabled, err := d.gate.Enabled(ctx, req.Repo)
	if err != nil {
		return batchv1.Job{}, "", false,
			errors.Wrapf(ctx, err, "resolve auto-build gate repo=%s", req.Repo)
	}
	if !enabled {
		d.metrics.IncBuildDispatched("skipped")
		glog.V(2).Infof(
			"github-release-watcher build skipped repo=%s ref=%s trigger=%s reason=no_auto_build",
			req.Repo, req.Ref(), trigger,
		)
		return batchv1.Job{}, "", false, nil
	}

	raw, err := d.manifests.LoadBuildJobManifest(ctx, req.Repo, req.Tag)
	if err != nil {
		return batchv1.Job{}, "", false,
			errors.Wrapf(ctx, err, "load build job manifest repo=%s tag=%s", req.Repo, req.Tag)
	}
	if len(raw) == 0 {
		d.metrics.IncBuildDispatched("skipped")
		glog.V(2).Infof(
			"github-release-watcher build skipped repo=%s ref=%s trigger=%s reason=no_build_manifest",
			req.Repo, req.Ref(), trigger,
		)
		return batchv1.Job{}, "", false, nil
	}

	job, err := RenderJob(ctx, raw, req)
	if err != nil {
		return batchv1.Job{}, "", false, err
	}

	imageRef := JobImage(job)
	if imageRef == "" {
		return batchv1.Job{}, "", false, errors.Errorf(
			ctx, "build job for repo=%s tag=%s names no container image", req.Repo, req.Tag,
		)
	}

	exists, err := d.images.Exists(ctx, imageRef)
	if err != nil {
		// Fail closed: an unreachable registry must never be read as "the
		// image is absent, build it". Re-pushing an existing tag produces a
		// mutable tag pointing at different content — the exact failure this
		// guard exists to prevent.
		d.metrics.IncFilterSkipped("registry_unreachable")
		glog.Warningf(
			"github-release-watcher build skipped repo=%s ref=%s trigger=%s reason=registry_unreachable image=%s err=%v",
			req.Repo,
			req.Ref(),
			trigger,
			imageRef,
			err,
		)
		return batchv1.Job{}, "", false, nil
	}
	if exists {
		d.metrics.IncFilterSkipped("image_exists")
		glog.V(2).Infof(
			"github-release-watcher build skipped repo=%s ref=%s trigger=%s reason=image_exists image=%s",
			req.Repo, req.Ref(), trigger, imageRef,
		)
		return batchv1.Job{}, "", false, nil
	}

	return job, imageRef, true, nil
}

// create applies the rendered Job, absorbing an already-present Job as a
// successful no-op: the Job is the idempotency key for (repo, tag), so a
// repeat dispatch of the same tag must not fail the cycle nor build twice.
func (d *dispatcher) create(
	ctx context.Context,
	req Request,
	job batchv1.Job,
	imageRef string,
	trigger Trigger,
) error {
	if err := d.jobs.Create(ctx, job); err != nil {
		if stderrors.Is(err, ErrJobAlreadyExists) {
			d.metrics.IncBuildDispatched("exists")
			glog.V(2).Infof(
				"github-release-watcher build dispatched repo=%s ref=%s trigger=%s image=%s job=%s namespace=%s result=job-exists",
				req.Repo, req.Ref(), trigger, imageRef, job.Name, job.Namespace,
			)
			return nil
		}
		d.metrics.IncBuildDispatched("error")
		return errors.Wrapf(
			ctx, err, "create build job repo=%s tag=%s job=%s", req.Repo, req.Tag, job.Name,
		)
	}

	d.metrics.IncBuildDispatched("created")
	glog.V(2).Infof(
		"github-release-watcher build dispatched repo=%s ref=%s trigger=%s image=%s job=%s namespace=%s",
		req.Repo, req.Ref(), trigger, imageRef, job.Name, job.Namespace,
	)
	return nil
}

// JobImage returns the image reference of the Job's first container, or "".
func JobImage(job batchv1.Job) string {
	containers := job.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return ""
	}
	return strings.TrimSpace(containers[0].Image)
}
