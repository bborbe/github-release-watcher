// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build_test

import (
	"context"
	stderrors "errors"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// buildJobManifest is a minimal in-repo build recipe in the canonical dialect.
const buildJobManifest = `apiVersion: batch/v1
kind: Job
metadata:
  name: __JOB_NAME__
  namespace: build
  annotations:
    build.benjamin-borbe.de/service: __SERVICE_PATH__
spec:
  template:
    spec:
      containers:
        - name: build
          image: __IMAGE__:__TAG__
      restartPolicy: Never
`

var _ = Describe("Dispatcher", func() {
	var (
		ctx        context.Context
		configs    *mocks.BuildConfigProvider
		manifests  *mocks.BuildManifestLoader
		images     *mocks.BuildImageChecker
		jobs       *mocks.BuildJobCreator
		epochs     *mocks.BuildCommitEpochProvider
		active     *mocks.BuildJobLister
		metrics    *mocks.BuildMetrics
		dispatcher build.Dispatcher
		req        build.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		configs = new(mocks.BuildConfigProvider)
		manifests = new(mocks.BuildManifestLoader)
		images = new(mocks.BuildImageChecker)
		jobs = new(mocks.BuildJobCreator)
		epochs = new(mocks.BuildCommitEpochProvider)
		active = new(mocks.BuildJobLister)
		metrics = new(mocks.BuildMetrics)
		dispatcher = build.NewDispatcher(build.Deps{
			Manifests:  manifests,
			Jobs:       jobs,
			Images:     images,
			Configs:    configs,
			Epochs:     epochs,
			ActiveJobs: active,
			Metrics:    metrics,
		})
		req = build.Request{Repo: "bborbe/example", Tag: "v1.2.3"}

		configs.ConfigReturns(build.Config{
			AutoBuild:       true,
			Destinations:    []string{"dockerhub"},
			PushSecret:      "registry-secret",
			DeadlineSeconds: 900,
		}, nil)
		manifests.LoadBuildJobManifestReturns([]byte(buildJobManifest), nil)
		images.ExistsReturns(false, nil)
		epochs.CommitEpochReturns(1767225600, nil)
		active.ActiveByTagReturns(nil, nil)
		jobs.CreateReturns(nil)
	})

	It("creates the build job for the tag", func() {
		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(1))
		_, job := jobs.CreateArgsForCall(0)
		Expect(job.Name).To(HavePrefix("build-example-v1-2-3-"))
		Expect(job.Namespace).To(Equal("build"))
		Expect(build.JobImage(job)).To(Equal("docker.io/bborbe/example:v1.2.3"))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("created"))
	})

	It("resolves the commit epoch for the ref being built", func() {
		// __SOURCE_DATE_EPOCH__ is validated inside the build container against
		// the cloned commit, so it must come from the ref, not from now().
		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		_, repo, ref := epochs.CommitEpochArgsForCall(0)
		Expect(repo).To(Equal("bborbe/example"))
		Expect(ref).To(Equal("refs/tags/v1.2.3"))
	})

	It("reads the manifest at the tag being built", func() {
		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		_, repo, tag := manifests.LoadBuildJobManifestArgsForCall(0)
		Expect(repo).To(Equal("bborbe/example"))
		Expect(tag).To(Equal("v1.2.3"))
	})

	It("skips a repo that did not opt into auto-build", func() {
		configs.ConfigReturns(build.Config{AutoBuild: false}, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
		Expect(manifests.LoadBuildJobManifestCallCount()).To(Equal(0))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("skipped"))
	})

	It("skips a repo whose destination class this build arm cannot resolve", func() {
		configs.ConfigReturns(build.Config{
			AutoBuild:    true,
			Destinations: []string{"dockerhub-typo"},
		}, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0),
			"a class that resolves to no registry must never reach a Job")
		Expect(manifests.LoadBuildJobManifestCallCount()).To(Equal(0))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("skipped"))
	})

	It("builds to the in-house registry for the internal class", func() {
		// The internal host carries a port, which is the case a naive
		// colon-means-tag check gets wrong.
		configs.ConfigReturns(build.Config{
			AutoBuild:       true,
			Destinations:    []string{"internal"},
			PushSecret:      "registry-secret",
			DeadlineSeconds: 900,
		}, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(1))
		_, job := jobs.CreateArgsForCall(0)
		Expect(build.JobImage(job)).To(
			Equal("docker.prod.nuke.benjamin-borbe.de:443/bborbe/example:v1.2.3"))
		_, probed := images.ExistsArgsForCall(0)
		Expect(probed).To(
			Equal("docker.prod.nuke.benjamin-borbe.de:443/bborbe/example:v1.2.3"))
	})

	It("skips a repo with no build recipe", func() {
		manifests.LoadBuildJobManifestReturns(nil, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("skipped"))
	})

	It("skips rather than creating a Job with a surviving token", func() {
		manifests.LoadBuildJobManifestReturns(
			[]byte(buildJobManifest+"# __UNKNOWN_TOKEN__\n"), nil,
		)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0),
			"a Job carrying a literal token fails minutes later in the pod")
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("skipped"))
	})

	It("fails closed when the registry is unreachable", func() {
		// The whole point of the guard: an unreachable registry must not read
		// as "the image is absent, build it".
		images.ExistsReturns(false, errors.Errorf(ctx, "dial tcp: connection refused"))

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0),
			"an unreachable registry must never create a job")
		Expect(metrics.IncFilterSkippedCallCount()).To(Equal(1))
		Expect(metrics.IncFilterSkippedArgsForCall(0)).To(Equal("registry_unreachable"))
	})

	It("skips when the image is already published", func() {
		images.ExistsReturns(true, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
		Expect(metrics.IncFilterSkippedArgsForCall(0)).To(Equal("image_exists"))
	})

	It("treats a non-terminal job for the same service and tag as a no-op", func() {
		// Job names are unique per run, so idempotency is this lookup rather
		// than a name collision. Without it the webhook and the poll backstop
		// would both build one tag.
		active.ActiveByTagReturns(
			[]build.ActiveJob{{Name: "build-example-v1-2-3-old", Service: "bborbe/example"}},
			nil,
		)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerPoll)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("exists"))
	})

	It("does not dedupe against a different service sharing the tag", func() {
		active.ActiveByTagReturns(
			[]build.ActiveJob{{Name: "other", Service: "bborbe/other"}}, nil,
		)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(1))
	})

	It("defers rather than failing when the build cap is reached", func() {
		// A queue, not an error: the tag is not going away, so the next poll
		// cycle retries.
		active.ActiveByTagReturns([]build.ActiveJob{
			{Name: "a", Service: "bborbe/other1"},
			{Name: "b", Service: "bborbe/other2"},
			{Name: "c", Service: "bborbe/other3"},
		}, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("deferred"))
	})

	It("treats an existing job as a successful no-op", func() {
		jobs.CreateReturns(build.ErrJobAlreadyExists)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerPoll)).To(Succeed())

		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("exists"))
	})

	It("returns an error when job creation fails", func() {
		jobs.CreateReturns(errors.Errorf(ctx, "forbidden"))

		err := dispatcher.Dispatch(ctx, req, build.TriggerWebhook)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("create build job"))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("error"))
	})

	It("rejects a request with no tag", func() {
		err := dispatcher.Dispatch(
			ctx, build.Request{Repo: "bborbe/example"}, build.TriggerWebhook,
		)
		Expect(err).To(HaveOccurred())
		Expect(jobs.CreateCallCount()).To(Equal(0))
	})
})

var _ = Describe("Request", func() {
	It("renders the full git ref", func() {
		Expect(build.Request{Tag: "v1.2.3"}.Ref()).To(Equal("refs/tags/v1.2.3"))
	})
})

var _ = Describe("ImageRepository", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	DescribeTable("resolves each named destination class",
		func(dest build.Destination, expected string) {
			repo, err := build.ImageRepository(ctx, dest, "bborbe/example")
			Expect(err).NotTo(HaveOccurred())
			Expect(repo).To(Equal(expected))
		},
		// The shared schema defines destinations as NAMED CLASSES, never
		// registry URLs, so each one has to resolve to a host here.
		Entry("dockerhub", build.DestinationDockerHub, "docker.io/bborbe/example"),
		Entry("ghcr", build.DestinationGHCR, "ghcr.io/bborbe/example"),
		Entry("internal", build.DestinationInternal,
			"docker.prod.nuke.benjamin-borbe.de:443/bborbe/example"),
	)

	It("rejects an unknown class rather than building to a nonsense path", func() {
		// A typo must fail loudly: treating it as "not opted in" would silently
		// stop building a repo that believes it opted in.
		_, err := build.ImageRepository(ctx, build.Destination("dockerhub-typo"), "bborbe/example")
		Expect(err).To(HaveOccurred())
		Expect(stderrors.Is(err, build.ErrUnknownDestination)).To(BeTrue())
	})
})

var _ = Describe("ImageReference", func() {
	It("appends the tag to a registry without a port", func() {
		Expect(build.ImageReference("docker.io/bborbe/example", "v1.2.3")).
			To(Equal("docker.io/bborbe/example:v1.2.3"))
	})

	It("appends the tag when the registry host carries a port", func() {
		// The in-house registry is host:port, so a colon anywhere in the
		// reference is part of the host. Reading it as a tag would probe the
		// repository instead of the image and build a tagless reference.
		Expect(build.ImageReference(
			"docker.prod.nuke.benjamin-borbe.de:443/bborbe/example", "v1.2.3",
		)).To(Equal("docker.prod.nuke.benjamin-borbe.de:443/bborbe/example:v1.2.3"))
	})

	It("leaves an already-tagged reference alone", func() {
		Expect(build.ImageReference("docker.io/bborbe/example:latest", "v1.2.3")).
			To(Equal("docker.io/bborbe/example:latest"))
	})
})
