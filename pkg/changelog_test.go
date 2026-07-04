// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"github.com/bborbe/github-release-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg.ParseChangelog", func() {
	It("ParseChangelog handles Unreleased at bottom with mixed v-prefix", func() {
		content := `# Changelog

## 1.2.6

- old

## v1.2.5

- older

## Unreleased

- new entry
`
		summary := pkg.ParseChangelog([]byte(content))
		Expect(summary.UnreleasedBullets).To(Equal(1))
		Expect(summary.UnreleasedIsFirst).To(Equal(false))
		Expect(summary.LatestVersion).To(Equal("1.2.6"))
	})

	It("canonical ordering: Unreleased first with two bullets", func() {
		content := `# Changelog

## Unreleased

- entry one
- entry two

## v1.2.3

- old
`
		summary := pkg.ParseChangelog([]byte(content))
		Expect(summary.UnreleasedBullets).To(Equal(2))
		Expect(summary.UnreleasedIsFirst).To(Equal(true))
		Expect(summary.LatestVersion).To(Equal("v1.2.3"))
	})

	It("empty Unreleased header", func() {
		content := `## Unreleased

## v1.0.0

- x
`
		summary := pkg.ParseChangelog([]byte(content))
		Expect(summary.UnreleasedBullets).To(Equal(0))
		Expect(summary.UnreleasedIsFirst).To(Equal(true))
		Expect(summary.LatestVersion).To(Equal("v1.0.0"))
	})

	It("missing Unreleased section", func() {
		content := `## v1.0.0

- x
`
		summary := pkg.ParseChangelog([]byte(content))
		Expect(summary.UnreleasedBullets).To(Equal(0))
		Expect(summary.UnreleasedIsFirst).To(Equal(false))
		Expect(summary.LatestVersion).To(Equal("v1.0.0"))
	})

	It("no versions, no unreleased", func() {
		content := `# Changelog

Intro paragraph
`
		summary := pkg.ParseChangelog([]byte(content))
		Expect(summary.UnreleasedBullets).To(Equal(0))
		Expect(summary.UnreleasedIsFirst).To(Equal(false))
		Expect(summary.LatestVersion).To(Equal(""))
	})

	It("nil input returns zero values", func() {
		summary := pkg.ParseChangelog(nil)
		Expect(summary.UnreleasedBullets).To(Equal(0))
		Expect(summary.UnreleasedIsFirst).To(Equal(false))
		Expect(summary.LatestVersion).To(Equal(""))
	})

	It("empty bytes returns zero values", func() {
		summary := pkg.ParseChangelog([]byte(""))
		Expect(summary.UnreleasedBullets).To(Equal(0))
		Expect(summary.UnreleasedIsFirst).To(Equal(false))
		Expect(summary.LatestVersion).To(Equal(""))
	})

	It("H3 under Unreleased does not terminate counting", func() {
		content := `## Unreleased

### Added

- a
- b

## v1.0.0
`
		summary := pkg.ParseChangelog([]byte(content))
		Expect(summary.UnreleasedBullets).To(Equal(2))
		Expect(summary.UnreleasedIsFirst).To(Equal(true))
		Expect(summary.LatestVersion).To(Equal("v1.0.0"))
	})

	DescribeTable(
		"lenient unreleased detection (spec 064)",
		func(content string, w want) {
			summary := pkg.ParseChangelog([]byte(content))
			Expect(summary.UnreleasedBullets).To(Equal(w.Bullets))
			Expect(summary.UnreleasedIsFirst).To(Equal(w.IsFirst))
			Expect(summary.LatestVersion).To(Equal(w.Latest))
		},
		Entry(
			"literal_Unreleased",
			fixtureLiteral,
			want{Bullets: 1, IsFirst: true, Latest: "v1.2.3"},
		),
		Entry(
			"lowercase_unreleased",
			fixtureLowercase,
			want{Bullets: 2, IsFirst: true, Latest: "v1.2.3"},
		),
		Entry(
			"extended_Unreleased_changes",
			fixtureExtended,
			want{Bullets: 1, IsFirst: true, Latest: "v1.2.3"},
		),
		Entry("WIP_heading", fixtureWIP, want{Bullets: 2, IsFirst: true, Latest: "v1.2.3"}),
		Entry(
			"version_header_first_no_unreleased",
			fixtureVersionFirst,
			want{Bullets: 0, IsFirst: false, Latest: "v0.35.0"},
		),
		Entry(
			"empty_unreleased_section",
			fixtureEmpty,
			want{Bullets: 0, IsFirst: true, Latest: "v0.35.0"},
		),
		Entry(
			"trailing_whitespace_heading",
			fixtureTrailingWS,
			want{Bullets: 1, IsFirst: true, Latest: "v1.2.3"},
		),
		Entry(
			"version_header_first_then_wip",
			fixtureVersionThenWIP,
			want{Bullets: 0, IsFirst: false, Latest: "v0.35.0"},
		),
		Entry(
			"version_first_then_wip_with_bullets",
			fixtureVersionThenWIPBullets,
			want{Bullets: 2, IsFirst: false, Latest: "v0.35.0"},
		),
		Entry(
			"second_non_version_h2_after_unreleased",
			fixtureUnreleasedThenNext,
			want{Bullets: 1, IsFirst: true, Latest: ""},
		),
	)
})

type want struct {
	Bullets int
	IsFirst bool
	Latest  string
}

const (
	fixtureLiteral = `# Changelog

## Unreleased

- new entry

## v1.2.3

- old
`

	fixtureLowercase = `# Changelog

## unreleased

- entry one
- entry two

## v1.2.3
`

	fixtureExtended = `# Changelog

## Unreleased changes

- one

## v1.2.3
`

	fixtureWIP = `# Changelog

## WIP

- alpha
- beta

## v1.2.3
`

	fixtureVersionFirst = `# Changelog

## v0.35.0

- shipped
`

	fixtureEmpty = `# Changelog

## WIP

## v0.35.0

- shipped
`

	fixtureTrailingWS = "# Changelog\n\n## WIP\t\n\n- one\n\n## v1.2.3\n"

	fixtureVersionThenWIP = `# Changelog

## v0.35.0

- shipped

## WIP

## v1.0.0

- next
`

	// fixtureVersionThenWIPBullets: WIP after v0.35.0 IS the first non-version H2,
	// so its bullets ARE counted (lenient detection). The trailing v1.0.0 does not
	// displace v0.35.0 as LatestVersion (first version wins).
	fixtureVersionThenWIPBullets = `# Changelog

## v0.35.0

- shipped

## WIP

- first-wip-bullet
- second-wip-bullet

## v1.0.0

- next
`

	fixtureUnreleasedThenNext = `# Changelog

## Unreleased

- real entry

## Next

- should-not-count
- nor-this
`
)
