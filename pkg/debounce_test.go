// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"time"

	"github.com/bborbe/github-release-watcher/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg.Debouncer", func() {
	var (
		now     libtime.DateTime
		clk     libtime.CurrentDateTimeGetter
		d       pkg.Debouncer
		advance func(time.Duration)
	)

	BeforeEach(func() {
		now = libtime.NewDateTime(2026, 8, 23, 10, 0, 0, 0, time.UTC)
		clk = libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime { return now })
		advance = func(d time.Duration) { now = now.Add(libtime.Duration(d)) }
		d = pkg.NewDebouncer(5*time.Minute, clk)
	})

	It("allows the first trigger for a key", func() {
		Expect(d.Allow("bborbe/obsidian-openclaw")).To(BeTrue())
	})

	It("denies a second trigger for the same key within the interval", func() {
		Expect(d.Allow("bborbe/obsidian-openclaw")).To(BeTrue())
		Expect(d.Allow("bborbe/obsidian-openclaw")).To(BeFalse())
	})

	It("allows again after the interval elapses", func() {
		Expect(d.Allow("bborbe/obsidian-openclaw")).To(BeTrue())
		advance(6 * time.Minute)
		Expect(d.Allow("bborbe/obsidian-openclaw")).To(BeTrue())
	})

	It("tracks keys independently", func() {
		Expect(d.Allow("bborbe/a")).To(BeTrue())
		Expect(d.Allow("bborbe/b")).To(BeTrue())
		Expect(d.Allow("bborbe/a")).To(BeFalse())
	})
})
