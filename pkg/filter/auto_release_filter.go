// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

// NewAutoReleaseFilter is the trust-gate predicate sourced from
// `.maintainer.yaml: release.autoRelease`. It emits the skip label
// "auto_release" for every Release whose backing config did NOT opt in —
// i.e., it is a POSITIVE-OPT-IN gate: only `autoRelease: true` passes.
//
// The skip-label string is intentionally retained as "auto_release"
// (rather than renamed to e.g. "not_opted_in") so existing Prometheus
// dashboards and alerts keyed on that label keep working. The metric
// semantics shift from "this repo is handled by the dark-factory auto-
// release daemon, skip" to "this repo did not opt into maintainer-bot
// auto-release, skip" — both legitimate "do not emit" reasons; the label
// surface is the same.
//
// Release.AutoRelease is sourced once per cycle by
// GitHubClient.GetMaintainerConfig and mirrored into filter.Release by
// the watcher's gatherer.
func NewAutoReleaseFilter() TaskCreationFilter {
	return TaskCreationFilterFunc(func(release Release) string {
		if release.AutoRelease {
			return ""
		}
		return "auto_release"
	})
}
