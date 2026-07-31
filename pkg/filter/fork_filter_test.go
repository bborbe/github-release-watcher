// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter.ForkFilter", func() {
	It("ForkFilter passes a fork when AllowFork is true", func() {
		f := filter.NewForkFilter()
		Expect(f.Skip(filter.Release{Fork: true, AllowFork: true})).To(BeEmpty())
	})

	It("ForkFilter skips with 'fork' label when Fork is true and AllowFork is false", func() {
		f := filter.NewForkFilter()
		Expect(f.Skip(filter.Release{Fork: true, AllowFork: false})).To(Equal("fork"))
	})

	It("ForkFilter passes a non-fork regardless of AllowFork", func() {
		f := filter.NewForkFilter()
		Expect(f.Skip(filter.Release{Fork: false, AllowFork: false})).To(BeEmpty())
		Expect(f.Skip(filter.Release{Fork: false, AllowFork: true})).To(BeEmpty())
	})

	It("ForkFilter passes the zero-value Release (not a fork)", func() {
		f := filter.NewForkFilter()
		Expect(f.Skip(filter.Release{})).To(BeEmpty())
	})
})
