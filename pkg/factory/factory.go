// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the maintainer-watcher-github-release binary.
package factory

import (
	"context"
	"net/http"

	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	cqrsiam "github.com/bborbe/cqrs/iam"
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/command"
	"github.com/bborbe/github-release-watcher/pkg/filter"
	"github.com/bborbe/github-release-watcher/pkg/handler"
	libkafka "github.com/bborbe/kafka"
	libkv "github.com/bborbe/kv"
	"github.com/bborbe/log"
	"github.com/bborbe/run"

	lib "github.com/bborbe/maintainer"
)

// CreateKafkaSender constructs a typed create-task command sender backed by a Kafka sync producer.
func CreateKafkaSender(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) task.CreateCommandSender {
	sender := cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory)
	return task.NewCreateCommandSender(sender, "")
}

// CreateStaticFilters builds the cycle-invariant filter chain (scope +
// empty_unreleased + auto_release gate). SHAUnchangedFilter is composed in per
// cycle inside Watcher.Poll because it needs a fresh CursorReader.
//
// Shared by main.go and cmd/run-once/main.go so adding a new filter only
// touches one place.
func CreateStaticFilters(allowlist []string) filter.TaskCreationFilter {
	return filter.TaskCreationFilters{
		filter.NewRepoAllowlistFilter(allowlist),
		filter.NewEmptyUnreleasedFilter(),
		filter.NewAutoReleaseFilter(),
	}
}

// CreateWatcher wires all dependencies and returns a ready-to-use Watcher.
//
// Pure composition — no I/O. The Kafka sync producer and the HTTP-resolved
// task sender are constructed by main.go (so the caller controls connection
// lifecycle + cleanup). The HTTP client is constructed by pkg/auth before
// this is called. taskCreationFilter is built by CreateStaticFilters.
//
// Reference: watcher/github-pr/pkg/factory/factory.go follows the same
// no-I/O-in-factory pattern.
func CreateWatcher(
	httpClient *http.Client,
	sender task.CreateCommandSender,
	cursorPath string,
	owner string,
	taskCreationFilter filter.TaskCreationFilter,
	metrics pkg.Metrics,
	stage string,
	targetVault string,
) pkg.Watcher {
	ghClient := pkg.NewGitHubClient(httpClient)
	publisher := pkg.NewTaskPublisher(
		sender,
		metrics,
		pkg.TaskConfig{Stage: stage, TargetVault: targetVault},
	)
	return pkg.NewWatcher(
		ghClient,
		publisher,
		metrics,
		cursorPath,
		owner,
		taskCreationFilter,
	)
}

// CreateTriggerReleaseCheckCommandSender constructs a typed trigger-release-check
// command sender backed by a Kafka sync producer. This is the HTTP-side
// sender: the /trigger handler publishes TriggerReleaseCheckCommand messages
// through it.
//
// CommandCreator and Initiator are built once here and reused across every
// SendCommand call (per cqrs/docs/producing-commands.md "Factory Wiring";
// matches trading/frontend/command's reference impl).
func CreateTriggerReleaseCheckCommandSender(
	ctx context.Context,
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) command.TriggerReleaseCheckCommandSender {
	return command.NewTriggerReleaseCheckCommandSender(
		base.NewCommandCreator(base.RequestIDChannel(ctx)),
		cqrsiam.Initiator("watcher-github-release"),
		cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory),
	)
}

// CreateTriggerReleaseCheckHandler wires the thin CQRS handler that publishes a
// TriggerReleaseCheckCommand to Kafka for each /trigger request.
// All poll-cycle work lives in the in-pod command consumer (see
// pkg/command.NewTriggerReleaseCheckCommandExecutor).
func CreateTriggerReleaseCheckHandler(
	sender command.TriggerReleaseCheckCommandSender,
) handler.TriggerReleaseCheckHandler {
	return handler.NewTriggerReleaseCheckHandler(sender)
}

// CreateCommandConsumer wires a run.Func that consumes TriggerReleaseCheckCommand
// messages from the github-release watcher's request topic and runs them through
// the shared Watcher.Poll(ctx) pipeline.
//
// The function is pure composition: no business logic, no conditionals.
// It uses cdb.RunCommandConsumerTxDefault (auto-wraps the transaction) per
// the go-cqrs/auto-tx-wrapper-no-manual-wrap rule — do NOT manually wrap
// the executor with kv.NewTransactionMiddleware.
func CreateCommandConsumer(
	saramaClientProvider libkafka.SaramaClientProvider,
	syncProducer libkafka.SyncProducer,
	db libkv.DB,
	watcher pkg.Watcher,
	topicPrefix base.TopicPrefix,
) run.Func {
	executors := cdb.CommandObjectExecutorTxs{
		command.NewTriggerReleaseCheckCommandExecutor(watcher),
	}
	return cdb.RunCommandConsumerTxDefault(
		saramaClientProvider,
		syncProducer,
		db,
		lib.GithubReleaserV1SchemaID,
		topicPrefix,
		false, // ignoreUnsupported
		executors,
	)
}
