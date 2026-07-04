// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter.TaskCreationFilters", func() {
	It("TaskCreationFilters returns empty when every filter passes", func() {
		chain := filter.TaskCreationFilters{
			filter.NewEmptyUnreleasedFilter(),
			filter.NewAutoReleaseFilter(),
		}
		Expect(chain.Skip(filter.Release{
			UnreleasedBullets: 3,
			AutoRelease:       true,
		})).To(BeEmpty())
	})

	It("TaskCreationFilters returns reason of first filter that votes skip", func() {
		chain := filter.TaskCreationFilters{
			filter.NewEmptyUnreleasedFilter(),
			filter.NewAutoReleaseFilter(),
		}
		Expect(chain.Skip(filter.Release{
			UnreleasedBullets: 0,
			AutoRelease:       true,
		})).To(Equal("empty_unreleased"))
	})

	It("TaskCreationFilters returns reason of later filter when earlier passes", func() {
		chain := filter.TaskCreationFilters{
			filter.NewEmptyUnreleasedFilter(),
			filter.NewAutoReleaseFilter(),
		}
		Expect(chain.Skip(filter.Release{
			UnreleasedBullets: 3,
			AutoRelease:       false,
		})).To(Equal("auto_release"))
	})

	It("TaskCreationFilters with empty slice never skips", func() {
		var chain filter.TaskCreationFilters
		Expect(chain.Skip(filter.Release{})).To(BeEmpty())
	})
})
