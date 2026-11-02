// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build_test

import (
	"context"

	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseBuildConfig", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("reads the dockerBuild.autoBuild opt-in",
		func(raw string, expected bool) {
			cfg, err := build.ParseBuildConfig(ctx, []byte(raw), "bborbe/example")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.AutoBuild).To(Equal(expected))
		},
		Entry("opted in", "dockerBuild:\n  autoBuild: true\n", true),
		Entry("explicitly off", "dockerBuild:\n  autoBuild: false\n", false),
		Entry("block absent — the common case", "release:\n  autoRelease: true\n", false),
		Entry("empty file", "", false),
	)

	It("returns an error for malformed YAML rather than reading it as 'off'", func() {
		// A typo in the opt-in must be visible; silently treating it as "not
		// opted in" is how a repo ends up never building with no explanation.
		_, err := build.ParseBuildConfig(ctx, []byte("dockerBuild: [not a map"), "bborbe/example")
		Expect(err).To(HaveOccurred())
	})

	It("carries the whole dockerBuild block, not just the opt-in", func() {
		// Every template token the repo can vary comes from here, so a field
		// dropped in parsing becomes a literal placeholder in a real Job.
		cfg, err := build.ParseBuildConfig(ctx, []byte(`
dockerBuild:
  autoBuild: true
  destinations:
    - dockerhub
  pushSecret: registry-secret
  deadlineSeconds: 900
  dockerfile: Containerfile
  servicePath: bborbe/example/sub
  contextPath: /src/sub
  dockerfilePath: /src/sub
`), "bborbe/example")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.AutoBuild).To(BeTrue())
		Expect(cfg.Destinations).To(Equal([]string{"dockerhub"}))
		Expect(cfg.PushSecret).To(Equal("registry-secret"))
		Expect(cfg.DeadlineSeconds).To(Equal(900))
		Expect(cfg.Dockerfile).To(Equal("Containerfile"))
		Expect(cfg.ServicePath).To(Equal("bborbe/example/sub"))
		Expect(cfg.ContextPath).To(Equal("/src/sub"))
		Expect(cfg.DockerfilePath).To(Equal("/src/sub"))
	})

	It("defaults servicePath to the owner-qualified repo key", func() {
		// The template defaults __SERVICE_PATH__ to the image basename, which
		// makes two repos sharing a basename indistinguishable. The dispatcher
		// must never let that default through.
		cfg, err := build.ParseBuildConfig(
			ctx, []byte("dockerBuild:\n  autoBuild: true\n"), "bborbe/example",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ServicePath).To(Equal("bborbe/example"))
	})

	It("rejects a config with no destinations — there is no image to build", func() {
		cfg, err := build.ParseBuildConfig(
			ctx, []byte("dockerBuild:\n  autoBuild: true\n"), "bborbe/example",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Validate(ctx)).To(HaveOccurred())
	})
})
