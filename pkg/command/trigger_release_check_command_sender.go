// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import (
	"context"

	"github.com/bborbe/cqrs/base"
	cdb "github.com/bborbe/cqrs/cdb"
	cqrsiam "github.com/bborbe/cqrs/iam"
	"github.com/bborbe/errors"
	"github.com/golang/glog"

	lib "github.com/bborbe/maintainer"
)

//counterfeiter:generate -o ../../mocks/trigger_release_check_command_sender.go --fake-name TriggerReleaseCheckCommandSender . TriggerReleaseCheckCommandSender

// TriggerReleaseCheckCommandSender sends TriggerReleaseCheckCommand payloads to
// Kafka. Calls Validate before publishing — a validation error is
// returned without touching Kafka.
type TriggerReleaseCheckCommandSender interface {
	SendCommand(ctx context.Context, cmd TriggerReleaseCheckCommand) error
}

// NewTriggerReleaseCheckCommandSender creates a TriggerReleaseCheckCommandSender.
// The commandCreator and initiator are injected at construction time per
// the cqrs/docs/producing-commands.md "Factory Wiring" pattern (matches
// trading/frontend/command's reference impl) — built once at wiring, reused
// across every SendCommand call. The commandObjectSender wraps the Kafka
// sync producer.
func NewTriggerReleaseCheckCommandSender(
	commandCreator base.CommandCreator,
	initiator cqrsiam.Initiator,
	commandObjectSender cdb.CommandObjectSender,
) TriggerReleaseCheckCommandSender {
	return &triggerReleaseCheckCommandSender{
		commandCreator:      commandCreator,
		initiator:           initiator,
		commandObjectSender: commandObjectSender,
	}
}

type triggerReleaseCheckCommandSender struct {
	commandCreator      base.CommandCreator
	initiator           cqrsiam.Initiator
	commandObjectSender cdb.CommandObjectSender
}

func (s *triggerReleaseCheckCommandSender) SendCommand(
	ctx context.Context,
	cmd TriggerReleaseCheckCommand,
) error {
	if err := cmd.Validate(ctx); err != nil {
		return errors.Wrapf(ctx, err, "validate TriggerReleaseCheckCommand")
	}
	event, err := base.ParseEvent(ctx, cmd)
	if err != nil {
		return errors.Wrapf(ctx, err, "parse TriggerReleaseCheckCommand event")
	}
	commandObject := cdb.CommandObject{
		Command: s.commandCreator.NewCommand(
			TriggerReleaseCheckCommandOperation,
			s.initiator,
			"",
			event,
		),
		SchemaID: lib.GithubReleaserV1SchemaID,
	}
	if err := s.commandObjectSender.SendCommandObject(ctx, commandObject); err != nil {
		return errors.Wrapf(ctx, err, "send TriggerReleaseCheckCommand to Kafka")
	}
	glog.V(2).
		Infof("trigger sender: published op=%s scope=%q force=%t", TriggerReleaseCheckCommandOperation, cmd.Scope, cmd.Force)
	return nil
}
