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

var _ = Describe("ParseAutoBuild", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("reads the dockerBuild.autoBuild opt-in",
		func(raw string, expected bool) {
			enabled, err := build.ParseAutoBuild(ctx, []byte(raw))
			Expect(err).NotTo(HaveOccurred())
			Expect(enabled).To(Equal(expected))
		},
		Entry("opted in", "dockerBuild:\n  autoBuild: true\n", true),
		Entry("explicitly off", "dockerBuild:\n  autoBuild: false\n", false),
		Entry("block absent — the common case", "release:\n  autoRelease: true\n", false),
		Entry("empty file", "", false),
	)

	It("returns an error for malformed YAML rather than reading it as 'off'", func() {
		// A typo in the opt-in must be visible; silently treating it as "not
		// opted in" is how a repo ends up never building with no explanation.
		_, err := build.ParseAutoBuild(ctx, []byte("dockerBuild: [not a map"))
		Expect(err).To(HaveOccurred())
	})
})
