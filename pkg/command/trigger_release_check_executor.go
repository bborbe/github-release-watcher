// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import (
	"context"

	"github.com/bborbe/cqrs/base"
	cdb "github.com/bborbe/cqrs/cdb"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg"
	libkv "github.com/bborbe/kv"
	"github.com/golang/glog"
)

// NewTriggerReleaseCheckCommandExecutor creates a cdb.CommandObjectExecutorTx that
// consumes TriggerReleaseCheckCommand messages and drives the github-release
// watcher: unmarshal → validate → invoke w.Poll(ctx, cmd.Force) on the shared
// watcher instance.
//
// Exit-path mapping (per spec 067 § Desired Behavior 4):
//   - malformed payload (MarshalInto fails)    → cdb.ErrCommandObjectSkipped
//   - cmd.Validate(ctx) failure                → cdb.ErrCommandObjectSkipped
//   - w.Poll returns non-nil error             → wrapped error (transient, retried)
//   - w.Poll returns nil                       → nil, nil, nil (success)
//
// SendResultEnabled is false (spec Non-goal: fire-and-forget, no result topic).
// The executor does NOT increment any metrics — the Watcher.Poll call
// already owns IncPollCycle / IncPublished / IncReposScanned / IncFilterSkipped.
// The executor reads cmd.Force and forwards it to Watcher.Poll as the
// skipSHAUnchanged argument (spec 071): a forced cycle runs every
// filter EXCEPT SHAUnchangedFilter, so repos whose head SHA matches
// the cursor are reconsidered exactly once for that cycle. The
// cmd.Scope field is still reserved-unread (spec Non-goal: per-repo
// filter UX is a separate spec).
func NewTriggerReleaseCheckCommandExecutor(
	watcher pkg.Watcher,
) cdb.CommandObjectExecutorTx {
	return cdb.CommandObjectExecutorTxFunc(
		TriggerReleaseCheckCommandOperation,
		false, // SendResultEnabled = false
		func(ctx context.Context, tx libkv.Tx, commandObject cdb.CommandObject) (*base.EventID, base.Event, error) {
			return runTriggerReleaseCheck(ctx, tx, commandObject, watcher)
		},
	)
}

// runTriggerReleaseCheck is the work-loop for a single TriggerReleaseCheckCommand.
// Splitting it out from the constructor (a) keeps the constructor's
// closure short and (b) makes the function directly testable from
// the package's external _test.go (the constructor returns an interface,
// not a closure).
//
// cmd.Validate is invoked here as defense-in-depth: the sender already
// validates before publishing, but a buggy client that bypasses the
// HTTP handler could otherwise inject garbage. The framework's
// CommandObject.Validate only checks the wrapper (SchemaID + base.Command),
// not the typed payload.
func runTriggerReleaseCheck(
	ctx context.Context,
	_ libkv.Tx,
	commandObject cdb.CommandObject,
	watcher pkg.Watcher,
) (*base.EventID, base.Event, error) {
	cmd, err := unmarshalAndValidate(ctx, commandObject)
	if err != nil {
		return nil, nil, err
	}
	if err := watcher.Poll(ctx, cmd.Force); err != nil {
		// Transient: rate-limited, GitHub 5xx, cursor read error, etc.
		// Framework emits Failure on the result topic, Kafka redelivers.
		// The Watcher already logged per-cycle state; we just propagate.
		return nil, nil, errors.Wrapf(
			ctx, err, "poll cycle from trigger scope=%q force=%t", cmd.Scope, cmd.Force,
		)
	}
	glog.V(2).Infof(
		"trigger executor: poll cycle complete scope=%q force=%t",
		cmd.Scope, cmd.Force,
	)
	return nil, nil, nil
}

// unmarshalAndValidate decodes the CommandObject payload into a typed
// TriggerReleaseCheckCommand and runs Validate as defense-in-depth. Any
// failure here is a deliberate, non-retryable skip.
func unmarshalAndValidate(
	ctx context.Context,
	commandObject cdb.CommandObject,
) (TriggerReleaseCheckCommand, error) {
	var cmd TriggerReleaseCheckCommand
	if err := commandObject.Command.Data.MarshalInto(ctx, &cmd); err != nil {
		return cmd, errors.Wrapf(
			ctx,
			cdb.ErrCommandObjectSkipped,
			"malformed TriggerReleaseCheckCommand: %v",
			err,
		)
	}
	if err := cmd.Validate(ctx); err != nil {
		return cmd, errors.Wrapf(
			ctx,
			cdb.ErrCommandObjectSkipped,
			"validate TriggerReleaseCheckCommand: %v",
			err,
		)
	}
	return cmd, nil
}
