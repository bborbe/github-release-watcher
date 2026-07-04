// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

//counterfeiter:generate -o ../../mocks/cursor_reader.go --fake-name CursorReader . CursorReader

// CursorReader is the minimal cursor read surface needed by SHAUnchangedFilter.
// Defined as a local interface (Hollywood principle) so the filter doesn't
// import pkg.Cursor — keeps filter package import-cycle-safe.
//
// Implementation: pkg.Cursor satisfies this via an adapter constructed in
// watcher.go before assembling the filter chain.
type CursorReader interface {
	// LastSeenSHA returns the recorded master HEAD for repoKey, or empty if unseen.
	LastSeenSHA(repoKey string) string
}

// NewSHAUnchangedFilter returns "sha_unchanged" when Release.HeadSHA equals
// the cursor's last-seen master SHA for the same repo. First-poll (cursor
// empty) always emits; subsequent polls only emit when master HEAD has
// advanced.
//
// This is the dedup property called out in [[Watcher Writing Guide]] §
// Required components #6 (Deterministic task_identifier). Two defenses against
// duplicate emit: (a) deterministic UUID5 → controller dedup; (b) this filter
// short-circuits the redundant work entirely.
func NewSHAUnchangedFilter(cursor CursorReader) TaskCreationFilter {
	return &shaUnchangedFilter{cursor: cursor}
}

type shaUnchangedFilter struct {
	cursor CursorReader
}

func (f *shaUnchangedFilter) Skip(release Release) string {
	if f.cursor.LastSeenSHA(release.RepoKey) == release.HeadSHA {
		return "sha_unchanged"
	}
	return ""
}
