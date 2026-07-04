// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter.EmptyUnreleasedFilter", func() {
	It("EmptyUnreleasedFilter skips when UnreleasedBullets is 0", func() {
		f := filter.NewEmptyUnreleasedFilter()
		Expect(f.Skip(filter.Release{UnreleasedBullets: 0})).To(Equal("empty_unreleased"))
	})

	It("EmptyUnreleasedFilter does not skip when UnreleasedBullets is 1", func() {
		f := filter.NewEmptyUnreleasedFilter()
		Expect(f.Skip(filter.Release{UnreleasedBullets: 1})).To(BeEmpty())
	})

	It("EmptyUnreleasedFilter does not skip when UnreleasedBullets is large", func() {
		f := filter.NewEmptyUnreleasedFilter()
		Expect(f.Skip(filter.Release{UnreleasedBullets: 42})).To(BeEmpty())
	})
})
