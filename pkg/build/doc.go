// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package build dispatches an in-cluster image build for a release tag.
//
// It is the trigger half of the release-to-image loop described in
// [[Release-to-Kaniko Build Trigger Design]] § Decision 1 (webhook + poll
// hybrid) and § Decision 4 (fail-closed on registry unreachable).
//
// The package owns dispatch only. The Job's *definition* stays in the target
// repository's `hack/build-job.yaml`, which this package fetches at the tag,
// renders, and applies — so the build recipe remains the repo's own artifact
// and per-repo variation needs no watcher change.
//
// The package deliberately imports nothing from `pkg` (mirroring
// `pkg/filter`): every collaborator is a narrow interface declared here, and
// the production adapters live in `pkg` / `pkg/factory`.
//
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6@v6.12.2 -generate
package build
