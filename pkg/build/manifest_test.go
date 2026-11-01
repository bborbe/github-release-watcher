// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build_test

import (
	"context"
	"errors"

	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
)

// renderParams is a complete, valid params set. Individual tests override the
// field under test so a failure names one omission rather than several.
func renderParams() build.RenderParams {
	return build.RenderParams{
		JobName:         "build-example-v1-2-3-20260101-000000-abcd",
		RepoURL:         "https://github.com/bborbe/example.git",
		Ref:             "refs/tags/v1.2.3",
		Tag:             "v1.2.3",
		Dockerfile:      "Dockerfile",
		Image:           "docker.io/bborbe/example",
		Push:            true,
		PushSecret:      "registry-secret",
		DeadlineSeconds: 900,
		SourceDateEpoch: 1767225600,
		ServicePath:     "bborbe/example",
	}
}

const tokenManifest = `apiVersion: batch/v1
kind: Job
metadata:
  name: __JOB_NAME__
  namespace: build
  annotations:
    build.benjamin-borbe.de/service: __SERVICE_PATH__
spec:
  activeDeadlineSeconds: __DEADLINE_SECONDS__
  template:
    spec:
      containers:
        - name: build
          image: __IMAGE__:__TAG__
          env:
            - name: SOURCE_DATE_EPOCH
              value: "__SOURCE_DATE_EPOCH__"
          args:
            - --ref=__REF__
            - --repo=__REPO_URL__
            - --dockerfile=__DOCKERFILE__
            - --push=__PUSH__
            - --secret=__PUSH_SECRET__
            - --context=__CONTEXT_PATH__
            - --dockerfile-path=__DOCKERFILE_PATH__
`

var _ = Describe("RenderJob", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("substitutes every canonical token", func() {
		job, err := build.RenderJob(ctx, []byte(tokenManifest), renderParams())
		Expect(err).NotTo(HaveOccurred())

		Expect(job.Name).To(Equal("build-example-v1-2-3-20260101-000000-abcd"))
		Expect(job.Namespace).To(Equal("build"))
		Expect(job.Annotations).To(HaveKeyWithValue(
			"build.benjamin-borbe.de/service", "bborbe/example",
		))
		Expect(*job.Spec.ActiveDeadlineSeconds).To(Equal(int64(900)))
		Expect(build.JobImage(job)).To(Equal("docker.io/bborbe/example:v1.2.3"))
		Expect(job.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{
			"--ref=refs/tags/v1.2.3",
			"--repo=https://github.com/bborbe/example.git",
			"--dockerfile=Dockerfile",
			"--push=true",
			"--secret=registry-secret",
			"--context=/src",
			"--dockerfile-path=/src",
		}))
	})

	It("fails rather than creating a Job with a surviving token", func() {
		// This is the whole point of the unrendered check: a literal __REPO_URL__
		// in a Job spec fails minutes later inside the pod, where it reads as a
		// build bug rather than a contract bug.
		raw := []byte(tokenManifest + "            - --extra=__NOT_A_KNOWN_TOKEN__\n")
		_, err := build.RenderJob(ctx, raw, renderParams())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, build.ErrUnrenderedToken)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("__NOT_A_KNOWN_TOKEN__"))
	})

	It("rejects a zero source date epoch before substituting anything", func() {
		// A zero passes every textual substitution and then fails inside the
		// build container, which compares the cloned commit's epoch to it.
		p := renderParams()
		p.SourceDateEpoch = 0
		_, err := build.RenderJob(ctx, []byte(tokenManifest), p)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("source date epoch"))
	})

	It("deletes the nodeSelector line when no host is given", func() {
		// Empty must mean "no selector": substituting "" into the flow map
		// yields `{kubernetes.io/hostname: }`, which is not a node selector.
		raw := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: __JOB_NAME__
spec:
  template:
    spec:
      nodeSelector: {kubernetes.io/hostname: __NODE_SELECTOR__}
      containers:
        - name: build
          image: __IMAGE__:__TAG__
`)
		job, err := build.RenderJob(ctx, raw, renderParams())
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Spec.Template.Spec.NodeSelector).To(BeEmpty())
	})

	It("pins the nodeSelector to the given hostname", func() {
		raw := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: __JOB_NAME__
spec:
  template:
    spec:
      nodeSelector: {kubernetes.io/hostname: __NODE_SELECTOR__}
      containers:
        - name: build
          image: __IMAGE__:__TAG__
`)
		p := renderParams()
		p.NodeSelector = "sun-k3s-worker-0"
		job, err := build.RenderJob(ctx, raw, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(
			"kubernetes.io/hostname", "sun-k3s-worker-0",
		))
	})

	It("derives a monorepo context path from the service path", func() {
		raw := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: __JOB_NAME__
spec:
  template:
    spec:
      containers:
        - name: build
          image: __IMAGE__:__TAG__
          args: ["--context=__CONTEXT_PATH__"]
`)
		p := renderParams()
		p.ServicePath = "bborbe/monorepo/services/api"
		job, err := build.RenderJob(ctx, raw, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Spec.Template.Spec.Containers[0].Args).
			To(Equal([]string{"--context=/src/services/api"}))
	})

	It("rejects a manifest that is not valid YAML", func() {
		_, err := build.RenderJob(ctx, []byte("{not yaml"), renderParams())
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("NewJobName", func() {
	It("is unique per dispatch even for the same tag", func() {
		// Uniqueness is required by the template: a tag-derived name collides on
		// a second run and a finished Job blocks a same-name re-create for its
		// whole TTL. Idempotency is the dispatcher's pre-create lookup instead.
		a := build.NewJobName("bborbe/example", "v1.2.3")
		b := build.NewJobName("bborbe/example", "v1.2.3")
		Expect(a).NotTo(Equal(b))
	})

	It("names the tag so the job is traceable to its release", func() {
		Expect(build.NewJobName("bborbe/example", "v1.2.3")).
			To(ContainSubstring("build-example-v1-2-3-"))
	})

	It("reduces tag characters that are illegal in a DNS label", func() {
		Expect(build.NewJobName("bborbe/example", "v1.2.3_rc+1")).
			To(ContainSubstring("build-example-v1-2-3-rc-1-"))
	})

	It("stays within the 63-character DNS label limit", func() {
		long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		Expect(len(build.NewJobName("bborbe/example", long))).To(BeNumerically("<=", 63))
	})
})

var _ = Describe("JobImage", func() {
	It("returns empty for a job with no containers", func() {
		Expect(build.JobImage(batchv1.Job{})).To(BeEmpty())
	})
})
