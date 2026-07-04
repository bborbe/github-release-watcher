// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command maintainer-watcher-github-release polls a configured GitHub owner for
// repos with non-empty ## Unreleased in CHANGELOG.md and publishes one
// CreateTaskCommand to Kafka per affected repo so github-releaser-agent picks
// it up automatically.
//
// See [[Build github-release watcher]] for scope + DoD; [[Watcher Writing Guide]]
// for the producer-side contract; [[Agent Task File Contract]] for the
// frontmatter/body shape this watcher emits.
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/auth"
	"github.com/bborbe/github-release-watcher/pkg/factory"
	"github.com/bborbe/github-release-watcher/pkg/filter"
	libhttp "github.com/bborbe/http"
	libkafka "github.com/bborbe/kafka"
	"github.com/bborbe/run"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	lib "github.com/bborbe/maintainer"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	Listen         string           `required:"false" arg:"listen"          env:"LISTEN"          usage:"HTTP listen address (healthz/readiness/metrics)"                                                 default:":9090"`
	Stage          string           `required:"true"  arg:"stage"           env:"STAGE"           usage:"Deployment stage (dev|prod)"`
	Owner          string           `required:"true"  arg:"owner"           env:"OWNER"           usage:"GitHub owner / org to scan (e.g. bborbe)"`
	RepoAllowlist  string           `required:"false" arg:"repo-allowlist"  env:"REPO_ALLOWLIST"  usage:"Comma-separated host-qualified repo allowlist (host/owner/repo); empty = allow-all within owner"`
	PollInterval   string           `required:"false" arg:"poll-interval"   env:"POLL_INTERVAL"   usage:"Poll interval (Go duration)"                                                                     default:"10m"`
	CursorPath     string           `required:"false" arg:"cursor-path"     env:"CURSOR_PATH"     usage:"Cursor persistence path (mount a PVC)"                                                           default:"/data/cursor.json"`
	KafkaBrokers   libkafka.Brokers `required:"true"  arg:"kafka-brokers"   env:"KAFKA_BROKERS"   usage:"Comma-separated Kafka broker list"`
	AppID          int64            `required:"false" arg:"app-id"          env:"APP_ID"          usage:"GitHub App ID (preferred auth path)"`
	InstallationID int64            `required:"false" arg:"installation-id" env:"INSTALLATION_ID" usage:"GitHub App Installation ID"`
	PEMKey         string           `required:"false" arg:"pem-key"         env:"PEM_KEY"         usage:"GitHub App PEM key (populated from k8s Secret)"                                                                              display:"length"`

	// TopicPrefix selects the Kafka topic prefix used for CQRS topic construction
	// (e.g. "develop" / "master"); independent of Stage, which remains the
	// deployment-stage identifier used for image tags and other non-topic
	// purposes. Empty means unprefixed topics.
	TopicPrefix base.TopicPrefix `required:"false" arg:"topic-prefix" env:"TOPIC_PREFIX" usage:"Kafka topic prefix for CQRS topic construction"`

	TriggerHandler http.Handler
}

//nolint:funlen // wires Run from validated config — extracting any chunk hurts readability without reducing complexity. 82 lines, 2 over the 80-line cap.
func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	pollInterval, err := time.ParseDuration(a.PollInterval)
	if err != nil {
		return errors.Wrapf(ctx, err, "parse poll interval %q", a.PollInterval)
	}

	allowlist := filter.ParseRepoAllowlist(a.RepoAllowlist)
	if len(allowlist) == 0 {
		glog.V(2).Infof("repo-allowlist count=0 (allow-all within owner=%s)", a.Owner)
	} else {
		glog.V(2).Infof("repo-allowlist count=%d", len(allowlist))
	}

	httpClient, err := auth.ResolveGitHubClient(ctx, auth.Credentials{
		AppID:          a.AppID,
		InstallationID: a.InstallationID,
		PEMKey:         []byte(a.PEMKey),
	})
	if err != nil {
		return errors.Wrap(ctx, err, "resolve auth")
	}
	defer httpClient.CloseIdleConnections()

	syncProducer, err := libkafka.NewSyncProducerWithName(
		ctx, a.KafkaBrokers, "maintainer-watcher-github-release",
	)
	if err != nil {
		return errors.Wrap(ctx, err, "create sync producer")
	}
	defer func() {
		if cerr := syncProducer.Close(); cerr != nil {
			glog.Warningf("close kafka sync producer: %v", cerr)
		}
	}()

	metrics := pkg.NewMetrics(nil)
	sender := factory.CreateKafkaSender(syncProducer, a.TopicPrefix)
	staticFilters := factory.CreateStaticFilters(allowlist)

	w := factory.CreateWatcher(
		httpClient,
		sender,
		a.CursorPath,
		a.Owner,
		staticFilters,
		metrics,
		a.Stage,
	)

	// HTTP-side sender backs the /trigger handler.
	triggerReleaseCheckSender := factory.CreateTriggerReleaseCheckCommandSender(
		ctx,
		syncProducer,
		a.TopicPrefix,
	)
	triggerHandler := factory.CreateTriggerReleaseCheckHandler(triggerReleaseCheckSender)
	a.TriggerHandler = libhttp.NewJSONErrorHandler(triggerHandler)

	// In-pod command consumer: third run.Func alongside poll + HTTP.
	// session-scoped offset store — replays the request topic from OffsetOldest
	// on pod restart; safe because the downstream CreateTaskCommand is idempotent
	// via the derived task_id.
	saramaClientProvider := libkafka.NewSaramaClientProviderNew(a.KafkaBrokers)
	db := pkg.NewMemDB()
	commandConsumer := factory.CreateCommandConsumer(
		saramaClientProvider,
		syncProducer,
		db,
		w, // shared with the poll-interval loop
		a.TopicPrefix,
	)

	glog.V(2).Infof(
		"maintainer-watcher-github-release starting stage=%s owner=%s interval=%s listen=%s schema=%s",
		a.Stage, a.Owner, a.PollInterval, a.Listen, lib.GithubReleaserV1SchemaID,
	)

	poll := func(ctx context.Context) error {
		glog.V(2).Infof("poll cycle start stage=%s", a.Stage)
		// skipSHAUnchanged=false: the interval-driven loop is the canonical
		// dedup-engaged path. force=true comes exclusively from the HTTP
		// /trigger handler's command publish (spec 071).
		return w.Poll(ctx, false)
	}

	// Order: poll → HTTP → command consumer (spec 067 AC 9: three run.Funcs).
	return run.CancelOnFirstFinish(ctx,
		a.pollLoop(poll, pollInterval),
		a.createHTTPServer(),
		commandConsumer,
	)
}

// createHTTPServer serves the mandatory triple (/healthz, /readiness, /metrics)
// per coding-guidelines/go-k8s-binary-conventions.md plus /trigger, which
// publishes a TriggerReleaseCheckCommand to Kafka for in-pod consumption by
// the command consumer (see factory.CreateCommandConsumer).
func (a *application) createHTTPServer() run.Func {
	return func(ctx context.Context) error {
		router := mux.NewRouter()
		router.Path("/healthz").Handler(libhttp.NewPrintHandler("OK"))
		router.Path("/readiness").Handler(libhttp.NewPrintHandler("OK"))
		router.Path("/metrics").Handler(promhttp.Handler())
		router.Path("/trigger").Handler(a.TriggerHandler)
		router.Path("/resetcursor/{repo:.+}").
			Handler(libhttp.NewDangerousHandlerWrapper(pkg.NewResetCursorHandler(a.CursorPath)))
		router.Path("/setcursor/{repo:.+}").
			Handler(libhttp.NewDangerousHandlerWrapper(pkg.NewSetCursorHandler(a.CursorPath)))
		glog.V(2).Infof("http server listening on %s", a.Listen)
		return libhttp.NewServer(a.Listen, router).Run(ctx)
	}
}

func (a *application) pollLoop(poll run.Func, interval time.Duration) run.Func {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Fire one cycle immediately on start, then on each tick.
		if err := poll(ctx); err != nil {
			glog.Errorf("initial poll: %v", err)
		}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if err := poll(ctx); err != nil {
					glog.Errorf("poll: %v", err)
				}
			}
		}
	}
}
