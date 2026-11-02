// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"
	stderrors "errors"

	"github.com/bborbe/errors"
)

// Config is a repo's `dockerBuild` block from `.maintainer.yaml`, plus the
// defaults the watcher applies when the block is silent.
//
// Every field the canonical template needs beyond the release identity lives
// here rather than in watcher code, so a repo can vary its build without a
// watcher release.
type Config struct {
	// AutoBuild is the opt-in. Absent or false means this repo is not built.
	AutoBuild bool
	// Destinations names the registries the image is pushed to. The first is
	// used as the image repository.
	Destinations []string
	// PushSecret names the dockerconfigjson Secret mounted at /docker-config.
	PushSecret string
	// DeadlineSeconds is the Job's activeDeadlineSeconds.
	DeadlineSeconds int
	// Dockerfile is the Dockerfile filename. Empty means DefaultDockerfile.
	Dockerfile string
	// ServicePath is the repo-relative service path. Empty means the repo name.
	ServicePath string
	// ContextPath is the BuildKit context local. Empty derives it from
	// ServicePath.
	ContextPath string
	// DockerfilePath is the BuildKit dockerfile local. Empty mirrors ContextPath.
	DockerfilePath string
}

// ErrBuildNotConfigured reports that a repo has no usable `dockerBuild` block.
var ErrBuildNotConfigured = stderrors.New("repo has no dockerBuild config")

//counterfeiter:generate -o ../../mocks/build_config_provider.go --fake-name BuildConfigProvider . ConfigProvider

// ConfigProvider returns a repo's build configuration.
//
// The error return is load-bearing in the same way ImageChecker's is: a config
// that cannot be read must surface as an error, never as a zero Config,
// because a zero value reads as "not opted in" and would silently skip every
// build for a repo that had opted in.
type ConfigProvider interface {
	Config(ctx context.Context, repo string) (Config, error)
}

//counterfeiter:generate -o ../../mocks/build_commit_epoch_provider.go --fake-name BuildCommitEpochProvider . CommitEpochProvider

// CommitEpochProvider resolves the epoch seconds of the commit a ref points at.
//
// This feeds __SOURCE_DATE_EPOCH__, which the build container validates against
// the cloned commit and refuses to run on a mismatch. It must therefore be the
// tagged commit's committer date — not the author date, and not an annotated
// tag's own date.
type CommitEpochProvider interface {
	CommitEpoch(ctx context.Context, repo, ref string) (int64, error)
}

// ActiveJob identifies a non-terminal build Job.
type ActiveJob struct {
	// Name is the Job's name.
	Name string
	// Service is the `build.benjamin-borbe.de/service` annotation value.
	Service string
}

//counterfeiter:generate -o ../../mocks/build_job_lister.go --fake-name BuildJobLister . JobLister

// JobLister reports the non-terminal build Jobs carrying a given tag.
//
// "Non-terminal" is the whole contract: completed Jobs linger until their TTL
// expires (86400s in go-skeleton), so a bare label list would report a day of
// finished builds and suppress every legitimate re-run. Implementations must
// exclude Jobs whose succeeded or failed count is non-zero.
type JobLister interface {
	ActiveByTag(ctx context.Context, tag string) ([]ActiveJob, error)
}

// ErrBuildCapReached reports that the cluster already runs the maximum number of
// concurrent builds.
//
// The cap is a queue, not an error: the tag is not going away, so the dispatcher
// records the deferral and the next poll cycle retries. It is enforced here
// rather than by a ResourceQuota because `count/jobs.batch` counts completed
// Jobs while their objects exist — a quota of 3 with a 24h TTL would lock the
// namespace for a day after three builds, which is an outage rather than a cap.
var ErrBuildCapReached = stderrors.New("build concurrency cap reached")

// DefaultMaxConcurrentBuilds mirrors the reference renderer's
// BUILD_MAX_CONCURRENT default so both entry points queue against the same
// number.
const DefaultMaxConcurrentBuilds = 3

// DefaultPushSecret mirrors the reference renderer's BUILD_PUSH_SECRET default
// (`hack/build-cluster.sh` requires the value; the Makefile defaults it).
const DefaultPushSecret = "buildkit-push"

// DefaultDeadlineSeconds mirrors the reference renderer's BUILD_TIMEOUT default.
const DefaultDeadlineSeconds = 3600

// Validate rejects a config that cannot produce a Job.
func (c Config) Validate(ctx context.Context) error {
	if len(c.Destinations) == 0 {
		return errors.Wrapf(ctx, ErrBuildNotConfigured, "dockerBuild.destinations is empty")
	}
	// Every destination is checked, not only the one this arm builds to: a
	// second entry naming a class the fleet cannot resolve is the same
	// configuration fault, and validating it here surfaces the typo once
	// instead of the day the second destination is wired.
	for _, dest := range c.Destinations {
		if !ValidateDestination(Destination(dest)) {
			return errors.Wrapf(
				ctx, ErrUnknownDestination,
				"dockerBuild.destinations contains %q (known: dockerhub, ghcr, internal)",
				dest,
			)
		}
	}
	return nil
}

// ImageRepositoryFor resolves this config's primary destination (the first
// entry) to the image repository the build pushes to.
func (c Config) ImageRepositoryFor(ctx context.Context, repo string) (string, error) {
	if len(c.Destinations) == 0 {
		return "", errors.Wrapf(ctx, ErrBuildNotConfigured, "dockerBuild.destinations is empty")
	}
	return ImageRepository(ctx, Destination(c.Destinations[0]), repo)
}
