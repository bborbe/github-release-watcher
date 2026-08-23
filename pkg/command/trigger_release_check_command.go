// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import (
	"context"

	"github.com/bborbe/cqrs/base"
)

// TriggerReleaseCheckCommandOperation is the Kafka command operation for
// triggering a github-release poll cycle. Wire string: "trigger-release-check".
const TriggerReleaseCheckCommandOperation base.CommandOperation = "trigger-release-check"

// TriggerReleaseCheckCommand is the payload for TriggerReleaseCheckCommandOperation.
// It is published to the github-release watcher's request topic by the /trigger
// HTTP handler (empty Scope) or the /webhook/github-release push handler
// (Scope = "owner/repo"), and consumed by the in-pod command consumer.
//
// Scope narrows the poll to a single repo (the webhook path) so a push costs
// ~3 API calls instead of a full fleet scan; empty Scope runs the full cycle.
// Force is wired (spec 071): when true, the consuming executor invokes
// Watcher.Poll with skipSHAUnchanged=true so the cycle's filter chain omits
// SHAUnchangedFilter — repos whose head SHA matches the cursor are
// reconsidered exactly once for that cycle. Every other filter (allowlist,
// empty-unreleased, auto-release) still runs.
type TriggerReleaseCheckCommand struct {
	Scope string `json:"scope,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// Validate enforces the command's schema rules. The empty payload {} is
// still accepted: Force defaults to false (engages SHAUnchangedFilter, the
// canonical poll-loop behaviour), and Scope may be empty (full scan) or
// "owner/repo" (webhook-triggered single-repo scan).
func (cmd TriggerReleaseCheckCommand) Validate(_ context.Context) error {
	return nil
}
