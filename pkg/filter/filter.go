// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package filter implements the TaskCreationFilter chain — predicates
// that decide whether a vault task should be created for a Release.
//
// See [[Watcher Writing Guide]] § Required components #4 (TaskCreationFilter chain)
// for the chain semantics. The release watcher's filters:
//
//  1. RepoAllowlistFilter — scope guard (allowlist via env)
//  2. EmptyUnreleasedFilter — skip if ## Unreleased has zero bullets
//  3. AutoReleaseFilter — pass only if .maintainer.yaml: release.autoRelease: true; skip otherwise (gate)
//  4. SHAUnchangedFilter — skip if cursor already recorded this master HEAD
package filter

//counterfeiter:generate -o ../../mocks/task_creation_filter.go --fake-name TaskCreationFilter . TaskCreationFilter

// Release is the filter-evaluation input.
// Mirrors pkg.Release as a local type to avoid an import cycle (pkg imports
// filter; filter cannot import pkg).
type Release struct {
	RepoKey           string // "github.com/owner/name" — for RepoAllowlistFilter
	HeadSHA           string // full SHA — for SHAUnchangedFilter
	UnreleasedBullets int    // for EmptyUnreleasedFilter
	AutoRelease       bool   // .maintainer.yaml: release.autoRelease — true means opted in to maintainer-bot auto-release
}

// TaskCreationFilter decides whether a single Release should be skipped
// (no vault task created). Implementations return the metric-label reason
// for the skip ("scope", "empty_unreleased", "auto_release", "sha_unchanged"),
// or "" if the Release should pass through.
//
// The string-returning shape removes the need for the watcher to re-evaluate
// each predicate after the chain votes skip — the chain itself bubbles up
// the reason the caller needs for `Metrics.IncFilterSkipped`.
type TaskCreationFilter interface {
	// Skip returns the skip reason (metric label) or "" to pass through.
	Skip(release Release) string
}

// TaskCreationFilterFunc adapts a function to the TaskCreationFilter interface.
type TaskCreationFilterFunc func(release Release) string

// Skip implements TaskCreationFilter for the function adapter.
func (f TaskCreationFilterFunc) Skip(release Release) string {
	return f(release)
}

// TaskCreationFilters is a slice composite: returns the first non-empty
// reason from its members. An empty slice never skips.
type TaskCreationFilters []TaskCreationFilter

// Skip returns the first non-empty reason from any contained filter.
// Short-circuits on first hit.
func (fs TaskCreationFilters) Skip(release Release) string {
	for _, f := range fs {
		if reason := f.Skip(release); reason != "" {
			return reason
		}
	}
	return ""
}
