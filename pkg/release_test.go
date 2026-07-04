// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"github.com/bborbe/github-release-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg.Release.ShortSHA", func() {
	It("returns first 7 chars for full SHA", func() {
		r := pkg.Release{HeadSHA: "d630ef3526cfc57fbdccd9ba53c5c3a02945e407"}
		Expect(r.ShortSHA()).To(Equal("d630ef3"))
	})

	It("returns the SHA verbatim when shorter than 7 chars", func() {
		r := pkg.Release{HeadSHA: "abc12"}
		Expect(r.ShortSHA()).To(Equal("abc12"))
	})

	It("returns empty when HeadSHA is empty", func() {
		r := pkg.Release{HeadSHA: ""}
		Expect(r.ShortSHA()).To(BeEmpty())
	})

	It("returns first 7 chars for exactly-7-char SHA", func() {
		r := pkg.Release{HeadSHA: "abc1234"}
		Expect(r.ShortSHA()).To(Equal("abc1234"))
	})
})
