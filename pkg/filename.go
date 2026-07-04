// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import "fmt"

// ComputeTaskTitle returns the human-readable task title used by the
// CreateTaskCommand frontmatter `title`.
//
// Format:
//
//	Release <owner>-<repo> <sha[:7]>
//
// Examples:
//   - "Release bborbe-disk-status 102b3b1"
//   - "Release bborbe-docker-utils d630ef3"
//
// The dash (NOT slash) form is mandatory: agent/lib's CreateCommand validator
// rejects titles containing '/' before they reach the controller. The Phase 1
// vault file `24 Tasks/Release bborbe-docker-utils d630ef3.md` shows
// `title: Release bborbe/docker-utils at d630ef3` in frontmatter — but that
// file was written directly by the Phase 1 slash command, which bypassed the
// production validator. Slash-command vault writes are NOT evidence of the
// production-schema contract. Rung-1 verification (`cmd/run-once`) surfaced
// this in 2026-05-28.
//
// Reference: watcher/github-pr/pkg/filename.go ComputeTitle (PR-shaped variant
// with maxSlugLen, maxTitleLen, taskSuffix knobs). Release titles are short and
// deterministic — those knobs are not needed here.
func ComputeTaskTitle(release Release) string {
	return fmt.Sprintf(
		"Release %s-%s %s",
		release.Repo.Owner,
		release.Repo.Name,
		release.ShortSHA(),
	)
}
