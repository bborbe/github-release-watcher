// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"os"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// DefaultCursorPath is the default cursor persistence location.
// k8s mounts /data as a PVC; main.go binds CURSOR_PATH=DefaultCursorPath.
const DefaultCursorPath = "/data/cursor.json"

// Cursor is the per-repo head-SHA dedup state.
//
// Shape rationale (vs watcher/github-pr's time-based cursor):
//   - Release watcher only emits a task when master HEAD advances on a repo
//     with non-empty ## Unreleased. The relevant "last seen" is per-repo master SHA.
//   - No PR-update-time scan (no upstream "since" filter for repo head moves).
//   - Per-repo map mirrors watcher/github-build's shape.
//
// Concurrency: not safe for concurrent use. The Watcher loads at poll start
// and saves at poll end (single goroutine).
type Cursor struct {
	Repos map[string]*RepoState `json:"repos"` // key: "owner/repo"
}

// RepoState is the cursor entry per repo.
type RepoState struct {
	LastSeenMasterSHA string `json:"last_seen_master_sha"`
}

// LoadCursor reads cursor state from path.
// Missing file → fresh empty cursor (cold start is valid).
// Corrupt file → error (caller should refuse to proceed; mirrors github-build policy).
//
// Reference: watcher/github-pr/pkg/cursor.go LoadCursor (time-based variant),
// watcher/github-build/pkg/cursor.go LoadCursor (per-repo state-machine variant).
func LoadCursor(ctx context.Context, path string) (*Cursor, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is config-controlled
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("cursor file not found, cold-start path=%s", path)
			return &Cursor{Repos: make(map[string]*RepoState)}, nil
		}
		return nil, errors.Wrapf(ctx, err, "read cursor file path=%s", path)
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errors.Wrapf(ctx, err, "unmarshal cursor file path=%s", path)
	}
	if c.Repos == nil {
		c.Repos = make(map[string]*RepoState)
	}
	return &c, nil
}

// SaveCursor persists cursor state to path atomically via temp file + rename.
func SaveCursor(ctx context.Context, path string, c *Cursor) error {
	data, err := json.Marshal(c)
	if err != nil {
		return errors.Wrapf(ctx, err, "marshal cursor state path=%s", path)
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil { // #nosec G306 -- intentional 0600
		return errors.Wrapf(ctx, err, "write cursor tmp path=%s", path)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return errors.Wrapf(ctx, err, "rename cursor tmp path=%s", path)
	}
	return nil
}
