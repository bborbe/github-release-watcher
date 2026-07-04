// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

// NewEmptyUnreleasedFilter returns "empty_unreleased" for Releases whose
// ## Unreleased section has zero bullets (header present but no entries).
//
// Rationale: an empty Unreleased section is a CHANGELOG hygiene oddity —
// either a contributor will fill it in (next poll picks it up) or the section
// should be removed. Emitting a task for it would create operator noise.
//
// No knobs. The Release.UnreleasedBullets count is computed once by
// ParseChangelog and passed through; this filter is a pure predicate.
func NewEmptyUnreleasedFilter() TaskCreationFilter {
	return TaskCreationFilterFunc(func(release Release) string {
		if release.UnreleasedBullets == 0 {
			return "empty_unreleased"
		}
		return ""
	})
}
