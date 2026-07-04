// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// Release is the watcher's per-repo observation — everything needed to
// (a) decide whether to emit a task and (b) populate the task frontmatter
// for github-releaser-agent.
//
// Built per-poll by Watcher from GitHubClient queries. Populated in this
// order so partial failures degrade gracefully:
//  1. Repo (from scope filter + ListRepos)
//  2. HeadSHA (from GetMasterSHA)
//  3. UnreleasedBullets + CurrentVersion (from ParseChangelog on GetChangelogContent)
//  4. AutoRelease (from GetMaintainerConfig — zero-value if .maintainer.yaml absent)
type Release struct {
	Repo              Repo
	HeadSHA           string // full SHA of master HEAD
	CurrentVersion    string // latest "## vX.Y.Z" header from CHANGELOG; "v0.0.0" if none
	UnreleasedBullets int    // count of "^- " lines under "## Unreleased"
	AutoRelease       bool   // .maintainer.yaml: release.autoRelease — true means the repo is opted into maintainer-bot auto-release (gate input)
}

// ShortSHA returns the first 7 chars of HeadSHA, used in task title + filename.
func (r Release) ShortSHA() string {
	if len(r.HeadSHA) < 7 {
		return r.HeadSHA
	}
	return r.HeadSHA[:7]
}
