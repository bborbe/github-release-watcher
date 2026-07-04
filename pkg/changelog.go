// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// ChangelogSummary is the parsed result of reading a CHANGELOG.md.
//
// Populated by ParseChangelog. The fields drive:
//   - UnreleasedBullets → empty_unreleased_filter (skip if 0)
//   - UnreleasedIsFirst → planning-phase precondition (the agent enforces this;
//     watcher does not skip on it — the agent's escalation path is better
//     placement for that operator feedback)
//   - LatestVersion → Release.CurrentVersion (semver base for next bump)
type ChangelogSummary struct {
	UnreleasedBullets int    // count of "^- " lines under the first non-version "## " heading
	UnreleasedIsFirst bool   // true if the first non-version "## " heading is the first "## " heading overall
	LatestVersion     string // first "## vX.Y.Z" or "## X.Y.Z" header found; "" if none
}

var versionHeaderRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

func isVersionHeader(heading string) (string, bool) {
	versionText := heading[3:] // strip "## "
	if versionHeaderRe.MatchString(versionText) {
		return versionText, true
	}
	return "", false
}

// ParseChangelog parses a CHANGELOG.md byte slice into a ChangelogSummary.
//
// Behaviour:
//   - Counts "^- " bullet lines directly under the first non-version "## " heading
//     (the unreleased section) until the next "## " heading
//   - The unreleased section is detected structurally: the first "## " heading that
//     is NOT a version header (## X.Y.Z or ## vX.Y.Z) is treated as unreleased. This
//     accepts the literal "## Unreleased" plus common author variants such as
//     "## unreleased", "## Unreleased changes", "## WIP", and "## Next"
//   - Determines whether the unreleased section is the first "## " heading
//   - Extracts the first "## X.Y.Z" or "## vX.Y.Z" heading as LatestVersion
//
// Implementation reference: the /github-unreleased-repo-watcher slash command (Phase 1
// prototype) does the equivalent in bash via awk + grep. The Go port lives here so it
// is unit-testable.
func ParseChangelog(content []byte) ChangelogSummary {
	if len(content) == 0 {
		return ChangelogSummary{}
	}

	var inUnreleased bool
	var seenAnyH2 bool
	var unreleasedOpened bool
	var unreleasedIsFirstH2 bool
	var unreleasedBullets int
	var latestVersion string

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()

		// Not a heading
		if !strings.HasPrefix(line, "## ") {
			if inUnreleased && strings.HasPrefix(line, "- ") {
				unreleasedBullets++
			}
			continue
		}

		// H2 heading — classify structurally: version-header OR unreleased.
		heading := strings.TrimRight(line, " \t")
		isFirstH2 := !seenAnyH2 // snapshot BEFORE setting seenAnyH2
		seenAnyH2 = true
		if v, ok := isVersionHeader(heading); ok {
			if latestVersion == "" {
				latestVersion = v
			}
			inUnreleased = false
			continue
		}
		// Non-version H2 → only the FIRST non-version H2 opens the unreleased section.
		// A later non-version heading (e.g. "## Next" after "## Unreleased") transitions
		// the parser out of unreleased state so its bullets do not double-count
		// (spec § Failure Modes row 4). Version headings do NOT consume the
		// first-non-version slot: a "## WIP" that appears after one or more version
		// headings still opens the unreleased section and its bullets ARE counted
		// (see test fixtureVersionThenWIPBullets — load-bearing, not a quirk).
		if !unreleasedOpened {
			if isFirstH2 {
				unreleasedIsFirstH2 = true
			}
			inUnreleased = true
			unreleasedOpened = true
		} else {
			inUnreleased = false
		}
	}

	return ChangelogSummary{
		UnreleasedBullets: unreleasedBullets,
		UnreleasedIsFirst: unreleasedIsFirstH2,
		LatestVersion:     latestVersion,
	}
}
