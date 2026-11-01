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

// ServicePath returns the owner-qualified service identity for the request.
//
// Owner-qualified on purpose: the canonical template defaults
// __SERVICE_PATH__ to the image basename, which makes two repos sharing a
// basename indistinguishable in the Job annotation the dedupe reads back.
func (r Request) ServicePath() string {
	return r.Repo
}

//counterfeiter:generate -o ../../mocks/build_dispatcher.go --fake-name BuildDispatcher . Dispatcher

// Dispatcher dispatches at most one build per (repo, tag).
//
// Idempotency is a pre-create lookup, not a name collision: Job names are unique
// per run so a legitimate re-run of a still-non-terminal tag is possible at all,
// which means the dedupe has to ask the cluster rather than rely on Create
// failing.
type Dispatcher interface {
	// Dispatch runs the pre-flight gates and, when they pass, creates the
	// build Job. A skip (no auto-build opt-in, no manifest, unrendered token,
	// image already present, registry unreachable, cap reached) is a nil error
	// — the cycle succeeded, it simply built nothing.
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

//counterfeiter:generate -o ../../mocks/build_metrics.go --fake-name BuildMetrics . Metrics

// Metrics is the narrow slice of watcher metrics the dispatcher records.
type Metrics interface {
	// IncFilterSkipped — reason: "image_exists" | "registry_unreachable"
	IncFilterSkipped(reason string)
	// IncBuildDispatched — result: "created" | "exists" | "skipped" | "deferred" | "error"
	IncBuildDispatched(result string)
}

// Deps is the dispatcher's dependency set.
type Deps struct {
	Manifests     ManifestLoader
	Jobs          JobCreator
	Images        ImageChecker
	Configs       ConfigProvider
	Epochs        CommitEpochProvider
	ActiveJobs    JobLister
	Metrics       Metrics
	MaxConcurrent int
}

// NewDispatcher wires the dispatch pipeline.
func NewDispatcher(d Deps) Dispatcher {
	if d.MaxConcurrent <= 0 {
		d.MaxConcurrent = DefaultMaxConcurrentBuilds
	}
	return &dispatcher{deps: d}
}

type dispatcher struct {
	deps Deps
}

// Dispatch implements Dispatcher.
//
// Order matters and mirrors the design record: opt-in gate, manifest, render,
// registry pre-flight, dedupe, cap, create. The registry check sits *after*
// rendering because the image reference it needs is only knowable once the tag
// has been substituted into the repo's own Job spec.
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

// prepare runs the gates that decide whether a build should happen at all. It
// returns proceed=false when a gate legitimately says "build nothing" — that
// is a successful cycle, not an error.
func (d *dispatcher) prepare(
	ctx context.Context,
	req Request,
	trigger Trigger,
) (batchv1.Job, string, bool, error) {
	cfg, err := d.deps.Configs.Config(ctx, req.Repo)
	if err != nil {
		return batchv1.Job{}, "", false,
			errors.Wrapf(ctx, err, "resolve build config repo=%s", req.Repo)
	}
	if !cfg.AutoBuild {
		return d.skip(ctx, req, trigger, "no_auto_build")
	}
	if err := cfg.Validate(ctx); err != nil {
		return d.skip(ctx, req, trigger, "no_build_config")
	}

	raw, err := d.deps.Manifests.LoadBuildJobManifest(ctx, req.Repo, req.Tag)
	if err != nil {
		return batchv1.Job{}, "", false,
			errors.Wrapf(ctx, err, "load build job manifest repo=%s tag=%s", req.Repo, req.Tag)
	}
	if len(raw) == 0 {
		return d.skip(ctx, req, trigger, "no_build_manifest")
	}

	job, err := d.render(ctx, raw, req, cfg)
	if err != nil {
		if stderrors.Is(err, ErrUnrenderedToken) {
			return d.skip(ctx, req, trigger, "unrendered_token")
		}
		return batchv1.Job{}, "", false, err
	}

	imageRef := ImageReference(cfg.Destinations[0], req.Tag)
	exists, err := d.deps.Images.Exists(ctx, imageRef)
	if err != nil {
		// Fail closed: an unreachable registry must never be read as "the
		// image is absent, build it". Re-pushing an existing tag produces a
		// mutable tag pointing at different content — the exact failure this
		// guard exists to prevent.
		d.deps.Metrics.IncFilterSkipped("registry_unreachable")
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
		d.deps.Metrics.IncFilterSkipped("image_exists")
		glog.V(2).Infof(
			"github-release-watcher build skipped repo=%s ref=%s trigger=%s reason=image_exists image=%s",
			req.Repo, req.Ref(), trigger, imageRef,
		)
		return batchv1.Job{}, "", false, nil
	}

	return d.admit(ctx, req, trigger, job, imageRef)
}

// render assembles the canonical render params and substitutes them.
func (d *dispatcher) render(
	ctx context.Context,
	raw []byte,
	req Request,
	cfg BuildConfig,
) (batchv1.Job, error) {
	epoch, err := d.deps.Epochs.CommitEpoch(ctx, req.Repo, req.Ref())
	if err != nil {
		return batchv1.Job{}, errors.Wrapf(
			ctx, err, "resolve commit epoch repo=%s ref=%s", req.Repo, req.Ref(),
		)
	}
	return RenderJob(ctx, raw, RenderParams{
		JobName:         NewJobName(req.Repo, req.Tag),
		RepoURL:         RepoURL(req.Repo),
		Ref:             req.Ref(),
		Tag:             req.Tag,
		Dockerfile:      cfg.Dockerfile,
		Image:           cfg.Destinations[0],
		Push:            true,
		PushSecret:      cfg.PushSecret,
		DeadlineSeconds: cfg.DeadlineSeconds,
		SourceDateEpoch: epoch,
		ServicePath:     req.ServicePath(),
		ContextPath:     cfg.ContextPath,
		DockerfilePath:  cfg.DockerfilePath,
	})
}

// admit applies the dedupe and the concurrency cap, both of which read the
// cluster rather than the manifest.
func (d *dispatcher) admit(
	ctx context.Context,
	req Request,
	trigger Trigger,
	job batchv1.Job,
	imageRef string,
) (batchv1.Job, string, bool, error) {
	active, err := d.deps.ActiveJobs.ActiveByTag(ctx, req.Tag)
	if err != nil {
		return batchv1.Job{}, "", false,
			errors.Wrapf(ctx, err, "list active build jobs tag=%s", req.Tag)
	}
	for _, a := range active {
		if a.Service != req.ServicePath() {
			continue
		}
		d.deps.Metrics.IncBuildDispatched("exists")
		glog.V(2).Infof(
			"github-release-watcher build dispatched repo=%s ref=%s trigger=%s image=%s job=%s namespace=%s result=job-exists",
			req.Repo, req.Ref(), trigger, imageRef, a.Name, job.Namespace,
		)
		return batchv1.Job{}, "", false, nil
	}
	if len(active) >= d.deps.MaxConcurrent {
		// A queue, not a failure: the tag is not going away, so the next poll
		// cycle retries. Recorded distinctly so an operator can tell a busy
		// cluster from a broken dispatcher.
		d.deps.Metrics.IncBuildDispatched("deferred")
		glog.V(2).Infof(
			"github-release-watcher build deferred repo=%s ref=%s trigger=%s reason=build_cap_reached active=%d cap=%d",
			req.Repo, req.Ref(), trigger, len(active), d.deps.MaxConcurrent,
		)
		return batchv1.Job{}, "", false, nil
	}
	return job, imageRef, true, nil
}

// skip records a legitimate "build nothing" outcome.
func (d *dispatcher) skip(
	ctx context.Context,
	req Request,
	trigger Trigger,
	reason string,
) (batchv1.Job, string, bool, error) {
	d.deps.Metrics.IncBuildDispatched("skipped")
	glog.V(2).Infof(
		"github-release-watcher build skipped repo=%s ref=%s trigger=%s reason=%s",
		req.Repo, req.Ref(), trigger, reason,
	)
	return batchv1.Job{}, "", false, nil
}

// create applies the rendered Job.
func (d *dispatcher) create(
	ctx context.Context,
	req Request,
	job batchv1.Job,
	imageRef string,
	trigger Trigger,
) error {
	if err := d.deps.Jobs.Create(ctx, job); err != nil {
		if stderrors.Is(err, ErrJobAlreadyExists) {
			d.deps.Metrics.IncBuildDispatched("exists")
			return nil
		}
		d.deps.Metrics.IncBuildDispatched("error")
		return errors.Wrapf(
			ctx, err, "create build job repo=%s tag=%s job=%s", req.Repo, req.Tag, job.Name,
		)
	}

	d.deps.Metrics.IncBuildDispatched("created")
	glog.V(2).Infof(
		"github-release-watcher build dispatched repo=%s ref=%s trigger=%s image=%s job=%s namespace=%s",
		req.Repo, req.Ref(), trigger, imageRef, job.Name, job.Namespace,
	)
	return nil
}

// ImageRef joins an image repository and a tag.
func ImageReference(image, tag string) string {
	if strings.Contains(image, ":") {
		return image
	}
	return image + ":" + tag
}

// RepoURL returns the clone URL for a repo key.
func RepoURL(repo string) string {
	return "https://github.com/" + repo + ".git"
}

// JobImage returns the image reference of the Job's first container, or "".
func JobImage(job batchv1.Job) string {
	containers := job.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return ""
	}
	return strings.TrimSpace(containers[0].Image)
}
