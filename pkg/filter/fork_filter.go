// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

// NewForkFilter is the trust-gate predicate for forked repositories.
//
// Non-forks always pass (Release.Fork == false is a no-op for this filter).
// For forks, it is a POSITIVE-OPT-IN gate sourced from
// `.maintainer.yaml: release.allowFork` — mirroring how NewAutoReleaseFilter
// requires an explicit `release.autoRelease: true` opt-in. A fork with
// `autoRelease: true` but no (or false) `allowFork` is skipped with reason
// "fork" — release history and CHANGELOG on a fork are usually pointed at
// the upstream repo, not the fork owner's own release stream, so releasing
// from a fork by default is the wrong behaviour.
//
// This filter used to be unreachable: ListRepos/mapGitHubRepos dropped forks
// during repo *listing*, upstream of the whole TaskCreationFilter chain, so
// a fork with autoRelease: true never even reached this gate — and because
// the drop happened silently at listing time, nothing logged why the fork
// never released (found on bborbe/tts-mcp; cost ~40min to diagnose). Moving
// the fork decision into the filter chain means it's observable via
// Metrics.IncFilterSkipped("fork") plus the watcher's per-skip glog line
// (see pkg/watcher.go processRepos), the same way every other gate already is.
//
// Release.Fork is sourced once per cycle from GitHubClient.ListRepos
// (Repo.Fork). Release.AllowFork is sourced from
// GitHubClient.GetMaintainerConfig (maintainerCfg.Release.AllowFork). Both
// are mirrored into filter.Release by the watcher's gatherer.
func NewForkFilter() TaskCreationFilter {
	return TaskCreationFilterFunc(func(release Release) string {
		if !release.Fork {
			return ""
		}
		if release.AllowFork {
			return ""
		}
		return "fork"
	})
}
