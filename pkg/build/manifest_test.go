// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build_test

import (
	"context"

	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
)

var _ = Describe("RenderJob", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("substitutes the tag, the bare tag and the repo", func() {
		raw := []byte(`apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      containers:
        - name: build
          image: docker.io/${REPO}:${TAG}
          args: ["--tag", "${TAG_BARE}"]
`)
		job, err := build.RenderJob(
			ctx, raw, build.Request{Repo: "bborbe/example", Tag: "v1.2.3"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(build.JobImage(job)).To(Equal("docker.io/bborbe/example:v1.2.3"))
		Expect(job.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"--tag", "1.2.3"}))
	})

	It("forces the job name so webhook and poll collide on the same key", func() {
		job, err := build.RenderJob(
			ctx,
			[]byte(buildJobManifest),
			build.Request{Repo: "bborbe/example", Tag: "v1.2.3"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Name).To(Equal("build-example-v1-2-3"))
	})

	It("defaults the namespace to build", func() {
		job, err := build.RenderJob(
			ctx,
			[]byte(buildJobManifest),
			build.Request{Repo: "bborbe/example", Tag: "v1.2.3"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Namespace).To(Equal("build"))
	})

	It("keeps a namespace the manifest sets", func() {
		raw := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  namespace: custom
spec:
  template:
    spec:
      containers:
        - name: build
          image: docker.io/bborbe/example:${TAG}
`)
		job, err := build.RenderJob(
			ctx, raw, build.Request{Repo: "bborbe/example", Tag: "v1.2.3"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Namespace).To(Equal("custom"))
	})

	It("rejects a manifest that is not valid YAML", func() {
		_, err := build.RenderJob(
			ctx, []byte("{not yaml"), build.Request{Repo: "bborbe/example", Tag: "v1.2.3"},
		)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("JobName", func() {
	It("names the tag so the job is traceable to its release", func() {
		Expect(build.JobName("bborbe/example", "v1.2.3")).To(Equal("build-example-v1-2-3"))
	})

	It("is deterministic", func() {
		Expect(build.JobName("bborbe/example", "v1.2.3")).
			To(Equal(build.JobName("bborbe/example", "v1.2.3")))
	})

	It("reduces tag characters that are illegal in a DNS label", func() {
		Expect(build.JobName("bborbe/example", "v1.2.3_rc+1")).
			To(Equal("build-example-v1-2-3-rc-1"))
	})

	It("stays within the 63-character DNS label limit", func() {
		long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		Expect(len(build.JobName("bborbe/example", long))).To(BeNumerically("<=", 63))
	})
})

var _ = Describe("JobImage", func() {
	It("returns empty for a job with no containers", func() {
		Expect(build.JobImage(batchv1.Job{})).To(BeEmpty())
	})
})
