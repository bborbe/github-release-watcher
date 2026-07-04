// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter.AutoReleaseFilter", func() {
	It("AutoReleaseFilter passes when AutoRelease is true", func() {
		f := filter.NewAutoReleaseFilter()
		Expect(f.Skip(filter.Release{AutoRelease: true})).To(BeEmpty())
	})

	It("AutoReleaseFilter skips with 'auto_release' label when AutoRelease is false", func() {
		f := filter.NewAutoReleaseFilter()
		Expect(f.Skip(filter.Release{AutoRelease: false})).To(Equal("auto_release"))
	})

	It("AutoReleaseFilter skips the zero-value Release with 'auto_release' label", func() {
		f := filter.NewAutoReleaseFilter()
		Expect(f.Skip(filter.Release{})).To(Equal("auto_release"))
	})
})
