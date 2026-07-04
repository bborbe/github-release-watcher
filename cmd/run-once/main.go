// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command maintainer-watcher-github-release-run-once runs a single GitHub release
// poll cycle then exits. Intended for local smoke-testing against a real repo.
// No HTTP server, no poll loop.
package main

import (
	"context"
	"net/http"
	"os"

	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/auth"
	"github.com/bborbe/github-release-watcher/pkg/factory"
	"github.com/bborbe/github-release-watcher/pkg/filter"
	libkafka "github.com/bborbe/kafka"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	app := NewApplication()
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

// NewApplication creates an Application with default dependencies.
func NewApplication() *Application {
	return &Application{
		CreateWatcher: factory.CreateWatcher,
		CreateProducer: func(ctx context.Context, brokers libkafka.Brokers, name string) (libkafka.SyncProducer, error) {
			return libkafka.NewSyncProducerWithName(ctx, brokers, name)
		},
	}
}

type Application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	Stage          string           `required:"true"  arg:"stage"           env:"STAGE"           usage:"Deployment stage (dev|prod)"`
	Owner          string           `required:"true"  arg:"owner"           env:"OWNER"           usage:"GitHub owner / org to scan (e.g. bborbe)"`
	RepoAllowlist  string           `required:"false" arg:"repo-allowlist"  env:"REPO_ALLOWLIST"  usage:"Comma-separated host-qualified repo allowlist (host/owner/repo); empty = allow-all within owner"`
	CursorPath     string           `required:"false" arg:"cursor-path"     env:"CURSOR_PATH"     usage:"Cursor persistence path"                                                                         default:"/data/cursor.json"`
	KafkaBrokers   libkafka.Brokers `required:"true"  arg:"kafka-brokers"   env:"KAFKA_BROKERS"   usage:"Comma-separated Kafka broker list"`
	AppID          int64            `required:"false" arg:"app-id"          env:"APP_ID"          usage:"GitHub App ID (preferred auth path)"`
	InstallationID int64            `required:"false" arg:"installation-id" env:"INSTALLATION_ID" usage:"GitHub App Installation ID"`
	PEMKey         string           `required:"false" arg:"pem-key"         env:"PEM_KEY"         usage:"GitHub App PEM key (populated from k8s Secret)"                                                                              display:"length"`
	// TopicPrefix selects the Kafka topic prefix used for CQRS topic construction
	// (e.g. "develop" / "master"); independent of Stage. Empty means unprefixed topics.
	TopicPrefix    base.TopicPrefix `required:"false" arg:"topic-prefix"    env:"TOPIC_PREFIX"    usage:"Kafka topic prefix for CQRS topic construction"`
	CreateWatcher  WatcherFactory
	CreateProducer ProducerFactory
}

// WatcherFactory creates a Watcher. Matches factory.CreateWatcher's signature
// exactly so tests can substitute a mock-returning closure.
type WatcherFactory func(
	httpClient *http.Client,
	sender task.CreateCommandSender,
	cursorPath string,
	owner string,
	taskCreationFilter filter.TaskCreationFilter,
	metrics pkg.Metrics,
	stage string,
) pkg.Watcher

// ProducerFactory creates a Kafka sync producer. Matches
// libkafka.NewSyncProducerWithName so tests can stub with a fake producer
// (e.g. sarama mock) without opening a real network connection.
type ProducerFactory func(
	ctx context.Context,
	brokers libkafka.Brokers,
	name string,
) (libkafka.SyncProducer, error)

func (a *Application) Run(ctx context.Context, _ libsentry.Client) error {
	if a.RepoAllowlist == "" {
		return errors.Errorf(
			ctx,
			"REPO_ALLOWLIST must be non-empty: set at least one host/owner/repo entry",
		)
	}
	allowlist := filter.ParseRepoAllowlist(a.RepoAllowlist)
	glog.V(2).Infof("repo-allowlist count=%d", len(allowlist))

	httpClient, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
		AppID:          a.AppID,
		InstallationID: a.InstallationID,
		PEMKey:         []byte(a.PEMKey),
	})
	if err != nil {
		return errors.Wrap(ctx, err, "resolve auth")
	}
	defer httpClient.CloseIdleConnections()

	syncProducer, err := a.CreateProducer(
		ctx, a.KafkaBrokers, "maintainer-watcher-github-release-run-once",
	)
	if err != nil {
		return errors.Wrap(ctx, err, "create sync producer")
	}
	defer func() {
		if cerr := syncProducer.Close(); cerr != nil {
			glog.Warningf("close kafka sync producer: %v", cerr)
		}
	}()

	metrics := pkg.NewMetrics(prometheus.NewRegistry())
	sender := factory.CreateKafkaSender(syncProducer, a.TopicPrefix)
	staticFilters := factory.CreateStaticFilters(allowlist)

	w := a.CreateWatcher(
		httpClient,
		sender,
		a.CursorPath,
		a.Owner,
		staticFilters,
		metrics,
		a.Stage,
	)

	if err := w.Poll(ctx, false); err != nil {
		return errors.Wrap(ctx, err, "poll failed")
	}
	return nil
}
