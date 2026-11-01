// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build_test

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// buildJobManifest is a minimal in-repo build recipe in the shape the
// dispatcher expects: a Job whose image carries the tag placeholder.
const buildJobManifest = `apiVersion: batch/v1
kind: Job
metadata:
  name: ignored-by-the-dispatcher
spec:
  template:
    spec:
      containers:
        - name: build
          image: docker.io/bborbe/example:${TAG}
      restartPolicy: Never
`

var _ = Describe("Dispatcher", func() {
	var (
		ctx        context.Context
		gate       *mocks.BuildAutoBuildGate
		manifests  *mocks.BuildManifestLoader
		images     *mocks.BuildImageChecker
		jobs       *mocks.BuildJobCreator
		metrics    *mocks.BuildMetrics
		dispatcher build.Dispatcher
		req        build.Request
	)

	BeforeEach(func() {
		ctx = context.Background()
		gate = new(mocks.BuildAutoBuildGate)
		manifests = new(mocks.BuildManifestLoader)
		images = new(mocks.BuildImageChecker)
		jobs = new(mocks.BuildJobCreator)
		metrics = new(mocks.BuildMetrics)
		dispatcher = build.NewDispatcher(manifests, jobs, images, gate, metrics)
		req = build.Request{Repo: "bborbe/example", Tag: "v1.2.3"}

		gate.EnabledReturns(true, nil)
		manifests.LoadBuildJobManifestReturns([]byte(buildJobManifest), nil)
		images.ExistsReturns(false, nil)
		jobs.CreateReturns(nil)
	})

	It("creates the build job for the tag", func() {
		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(1))
		_, job := jobs.CreateArgsForCall(0)
		Expect(job.Name).To(Equal(build.JobName("bborbe/example", "v1.2.3")))
		Expect(job.Namespace).To(Equal("build"))
		Expect(build.JobImage(job)).To(Equal("docker.io/bborbe/example:v1.2.3"))

		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("created"))
	})

	It("reads the manifest at the tag being built", func() {
		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		_, repo, tag := manifests.LoadBuildJobManifestArgsForCall(0)
		Expect(repo).To(Equal("bborbe/example"))
		Expect(tag).To(Equal("v1.2.3"))
	})

	It("skips a repo that did not opt into auto-build", func() {
		gate.EnabledReturns(false, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
		Expect(manifests.LoadBuildJobManifestCallCount()).To(Equal(0))
		Expect(metrics.IncBuildDispatchedArgsForCall(0)).To(Equal("skipped"))
	})

	It("skips a repo with no build recipe", func() {
		manifests.LoadBuildJobManifestReturns(nil, nil)

		Expect(dispatcher.Dispatch(ctx, req, build.TriggerWebhook)).To(Succeed())

		Expect(jobs.CreateCallCount()).To(Equal(0))
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

	It("treats an existing job as a successful no-op", func() {
		// The webhook and the poll backstop both see the same tag; the Job is
		// the idempotency key.
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
