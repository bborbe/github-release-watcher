// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"strings"

	repoallowlist "github.com/bborbe/maintainer/lib/repoallowlist"
)

// ParseRepoAllowlist parses a comma-separated allowlist string into a slice
// of host-qualified repo keys (e.g. "github.com/bborbe/disk-status").
// Whitespace trimmed; empty entries skipped. nil on empty input (allow-all).
//
// Carried verbatim from watcher/github-pr — domain-agnostic.
func ParseRepoAllowlist(raw string) []string {
	if raw == "" {
		return nil
	}
	var result []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			result = append(result, entry)
		}
	}
	return result
}

// NewRepoAllowlistFilter returns a TaskCreationFilter that returns "scope"
// for Releases whose RepoKey is not in the allowlist. An empty allowlist
// never skips (allow-all).
func NewRepoAllowlistFilter(allowlist []string) TaskCreationFilter {
	return &repoAllowlistFilter{allowlist: allowlist}
}

type repoAllowlistFilter struct {
	allowlist []string
}

func (f *repoAllowlistFilter) Skip(release Release) string {
	if !repoallowlist.IsAllowed(f.allowlist, release.RepoKey) {
		return "scope"
	}
	return ""
}
