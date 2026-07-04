// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("filter.SHAUnchangedFilter", func() {
	newCursor := func(data map[string]string) *mocks.CursorReader {
		c := &mocks.CursorReader{}
		c.LastSeenSHAStub = func(repoKey string) string { return data[repoKey] }
		return c
	}

	It("SHAUnchangedFilter skips when LastSeenSHA equals HeadSHA", func() {
		f := filter.NewSHAUnchangedFilter(newCursor(map[string]string{
			"github.com/bborbe/docker-utils": "d630ef3",
		}))
		Expect(f.Skip(filter.Release{
			RepoKey: "github.com/bborbe/docker-utils",
			HeadSHA: "d630ef3",
		})).To(Equal("sha_unchanged"))
	})

	It("SHAUnchangedFilter emits when LastSeenSHA differs from HeadSHA", func() {
		f := filter.NewSHAUnchangedFilter(newCursor(map[string]string{
			"github.com/bborbe/docker-utils": "d630ef3",
		}))
		Expect(f.Skip(filter.Release{
			RepoKey: "github.com/bborbe/docker-utils",
			HeadSHA: "different-sha",
		})).To(BeEmpty())
	})

	It("SHAUnchangedFilter emits when repo is unseen by the cursor", func() {
		f := filter.NewSHAUnchangedFilter(newCursor(map[string]string{}))
		Expect(f.Skip(filter.Release{
			RepoKey: "github.com/bborbe/new-repo",
			HeadSHA: "abc123",
		})).To(BeEmpty())
	})

	It("SHAUnchangedFilter handles empty HeadSHA against unseen repo", func() {
		// degenerate case — production path never passes empty HeadSHA through; documented for posterity
		f := filter.NewSHAUnchangedFilter(newCursor(map[string]string{}))
		Expect(f.Skip(filter.Release{
			RepoKey: "x",
			HeadSHA: "",
		})).To(Equal("sha_unchanged"))
	})
})
