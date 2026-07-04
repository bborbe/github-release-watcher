// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter.RepoAllowlistFilter", func() {
	It("RepoAllowlistFilter allows everything when allowlist is empty", func() {
		f := filter.NewRepoAllowlistFilter(nil)
		Expect(f.Skip(filter.Release{RepoKey: "github.com/anyone/anything"})).To(BeEmpty())
	})

	It("RepoAllowlistFilter allows everything when allowlist is empty slice", func() {
		f := filter.NewRepoAllowlistFilter([]string{})
		Expect(f.Skip(filter.Release{RepoKey: "github.com/anyone/anything"})).To(BeEmpty())
	})

	It("RepoAllowlistFilter skips repo outside the allowlist", func() {
		f := filter.NewRepoAllowlistFilter([]string{"github.com/bborbe/docker-utils"})
		Expect(f.Skip(filter.Release{RepoKey: "github.com/bborbe/other-repo"})).To(Equal("scope"))
	})

	It("RepoAllowlistFilter does not skip repo present in the allowlist", func() {
		f := filter.NewRepoAllowlistFilter([]string{"github.com/bborbe/docker-utils"})
		Expect(f.Skip(filter.Release{RepoKey: "github.com/bborbe/docker-utils"})).To(BeEmpty())
	})
})

var _ = Describe("filter.ParseRepoAllowlist", func() {
	It("ParseRepoAllowlist returns nil on empty input", func() {
		Expect(filter.ParseRepoAllowlist("")).To(BeNil())
	})

	It("ParseRepoAllowlist trims whitespace and skips empty entries", func() {
		Expect(filter.ParseRepoAllowlist(
			"github.com/bborbe/a, github.com/bborbe/b , , github.com/bborbe/c",
		)).To(Equal([]string{"github.com/bborbe/a", "github.com/bborbe/b", "github.com/bborbe/c"}))
	})

	It("ParseRepoAllowlist returns nil for all-whitespace input", func() {
		Expect(filter.ParseRepoAllowlist("  ,   ,  ")).To(BeNil())
	})

	It("ParseRepoAllowlist returns nil for whitespace-only single entry", func() {
		Expect(filter.ParseRepoAllowlist("   ")).To(BeNil())
	})
})
