// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pkg provides the core domain types and logic for the
// maintainer-watcher-github-release service:
//
//   - Repo + Release types — what the watcher observes
//   - GitHubClient interface — what it queries upstream
//   - Cursor — per-repo head-SHA dedup persisted to disk
//   - TaskPublisher — builds the CreateTaskCommand for github-releaser-agent
//   - Watcher — the Poll loop tying it all together
//
// See [[Watcher Writing Guide]] for the producer-side contract and
// [[Agent Task File Contract]] for the frontmatter/body shape this
// watcher emits.
//
// Reference implementations the prompts should mirror:
//   - watcher/github-pr/pkg/  — PR-scan analogue (time-based cursor)
//   - watcher/github-build/pkg/  — per-repo state-machine cursor analogue
package pkg

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6@v6.12.2 -generate
