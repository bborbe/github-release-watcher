// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/command"
	libhttp "github.com/bborbe/http"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// WebhookHeader is a typed GitHub webhook header name, so header-name typos
// are caught by the type system.
type WebhookHeader string

const (
	WebhookSignatureHeader WebhookHeader = "X-Hub-Signature-256"
	WebhookEventHeader     WebhookHeader = "X-GitHub-Event"
)

// AvailableWebhookHeaders lists the webhook header names the handler reads.
var AvailableWebhookHeaders = []WebhookHeader{WebhookSignatureHeader, WebhookEventHeader}

//counterfeiter:generate -o ../../mocks/webhook_metrics.go --fake-name WebhookMetrics . WebhookMetrics

// WebhookMetrics is the narrow slice of watcher metrics the webhook handler
// records. pkg.Metrics satisfies it structurally, keeping this handler free
// of the pkg import — mirroring trigger_handler's thin dependency set.
type WebhookMetrics interface {
	IncWebhookDelivery(result string)
	IncWebhookSignatureRejected()
	ObserveWebhookDispatchLatency(seconds float64)
	// IncWebhookSkipped counts push deliveries that did not publish a
	// release-check. reason: "no_release_files" | "debounced".
	IncWebhookSkipped(reason string)
}

// WebhookHandler handles POST /webhook/github-release.
// The handler is intentionally thin, mirroring TriggerReleaseCheckHandler:
// verify the GitHub HMAC signature, extract the repo from the push event,
// publish a TriggerReleaseCheckCommand to Kafka, and return 202. All GitHub
// API access, filter evaluation, and trust logic stays in the in-pod command
// consumer (shared with /trigger), so the allowlist/empty-unreleased gates
// and the release-check dedup apply to webhook deliveries unchanged.
type WebhookHandler = libhttp.WithError

// NewWebhookHandler returns a handler that publishes a TriggerReleaseCheckCommand
// for each signature-verified push webhook delivery that (a) touches a
// release-relevant file (CHANGELOG.md / .maintainer.yaml) and (b) passes the
// per-repo debounce. All other pushes are acked and skipped.
func NewWebhookHandler(
	sender command.TriggerReleaseCheckCommandSender,
	secret string,
	metrics WebhookMetrics,
	clock libtime.CurrentDateTimeGetter,
	debouncer pkg.Debouncer,
) WebhookHandler {
	return &webhookHandler{
		sender:    sender,
		secret:    secret,
		metrics:   metrics,
		clock:     clock,
		debouncer: debouncer,
	}
}

type webhookHandler struct {
	sender    command.TriggerReleaseCheckCommandSender
	secret    string
	metrics   WebhookMetrics
	clock     libtime.CurrentDateTimeGetter
	debouncer pkg.Debouncer
}

func (h *webhookHandler) ServeHTTP(
	ctx context.Context,
	resp http.ResponseWriter,
	req *http.Request,
) error {
	start := h.clock.Now()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "read webhook body"),
			http.StatusBadRequest,
		)
	}
	if err := verifyWebhookSignature(
		ctx,
		h.secret,
		req.Header.Get(string(WebhookSignatureHeader)),
		body,
	); err != nil {
		h.metrics.IncWebhookSignatureRejected()
		return libhttp.WrapWithStatusCode(err, http.StatusUnauthorized)
	}

	event := req.Header.Get(string(WebhookEventHeader))
	if event == "ping" {
		return writeWebhookAck(resp)
	}
	if event != "push" {
		glog.V(2).Infof("webhook ignored event=%s", event)
		return writeWebhookAck(resp)
	}

	var payload webhookPushEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "parse webhook payload"),
			http.StatusBadRequest,
		)
	}
	if payload.Repository.FullName == "" {
		return libhttp.WrapWithStatusCode(
			errors.Errorf(ctx, "webhook payload missing repository.full_name"),
			http.StatusBadRequest,
		)
	}

	tag, dispatch := h.classify(payload)
	if !dispatch {
		return writeWebhookAck(resp)
	}

	// Dispatch the repo-scoped payload — the executor's Poll now narrows the
	// scan to this single repo (scoped poll), so one push costs ~3 API calls,
	// not a full fleet scan.
	if err := h.sender.SendCommand(ctx, command.TriggerReleaseCheckCommand{
		Scope: payload.Repository.FullName,
		Tag:   tag,
	}); err != nil {
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "send TriggerReleaseCheckCommand"),
			http.StatusBadGateway,
		)
	}

	h.metrics.IncWebhookDelivery("success")
	h.metrics.ObserveWebhookDispatchLatency(h.clock.Now().Sub(start).Duration().Seconds())
	glog.V(2).Infof(
		"webhook accepted repo=%s ref=%s",
		payload.Repository.FullName,
		payload.Ref,
	)
	return writeWebhookDispatched(resp)
}

// classify decides whether a parsed push delivery should publish a command and
// returns the bare tag when it names one. It records the skip metric and log
// line for every delivery it rejects, so ServeHTTP stays a thin transport shell.
//
// A tag push arrives as ref=refs/tags/<tag> with an EMPTY commits array, so the
// release-file gate would drop every one of them with reason=no_release_files.
// Tag pushes are the trigger for the build path ([[Release-to-Kaniko Build
// Trigger Design]] § Decision 1), so they bypass that gate and carry the tag.
//
// Only branch pushes that touch a release-relevant file can change the release
// state (CHANGELOG.md bullets / .maintainer.yaml autoRelease). Anything else is
// acked and skipped — a repo whose autocommit daemon pushes
// obsidian-openclaw-style every few seconds but never touches these files now
// emits ZERO release-checks (the storm that exhausted the rate limit).
//
// Debounce is per repo for branch pushes. A tag push debounces on repo+tag
// instead: a release cut lands seconds after the merge push that produced it,
// and a repo-only key would swallow exactly the tag event the build path exists
// to see.
func (h *webhookHandler) classify(payload webhookPushEvent) (string, bool) {
	tag, isTagPush := parseTagRef(payload.Ref)

	if !isTagPush && !touchesReleaseFiles(payload) {
		h.metrics.IncWebhookSkipped("no_release_files")
		glog.V(2).Infof(
			"webhook skipped repo=%s reason=no-release-files",
			payload.Repository.FullName,
		)
		return "", false
	}

	debounceKey := payload.Repository.FullName
	if isTagPush {
		debounceKey = payload.Repository.FullName + "@" + tag
	}
	if !h.debouncer.Allow(debounceKey) {
		h.metrics.IncWebhookSkipped("debounced")
		glog.V(2).Infof(
			"webhook skipped repo=%s reason=debounced",
			payload.Repository.FullName,
		)
		return "", false
	}
	return tag, true
}

// webhookPushEvent is the subset of a GitHub push event the handler reads:
// the ref (branch/tag), the repository's full name, and each commit's
// modified/added file lists (used to detect release-relevant pushes).
type webhookPushEvent struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Commits []struct {
		Modified []string `json:"modified"`
		Added    []string `json:"added"`
	} `json:"commits"`
}

// releaseFiles are the files whose modification can change a repo's release
// state: CHANGELOG.md bullets and the .maintainer.yaml autoRelease opt-in.
var releaseFiles = map[string]struct{}{
	"CHANGELOG.md":     {},
	".maintainer.yaml": {},
}

// tagRefPrefix is the ref prefix GitHub uses for tag pushes.
const tagRefPrefix = "refs/tags/"

// parseTagRef reports whether ref names a tag and, if so, the bare tag
// ("v1.2.3" for "refs/tags/v1.2.3"). A branch ref, an empty ref, or the bare
// "refs/tags/" prefix with no tag returns false.
func parseTagRef(ref string) (string, bool) {
	if !strings.HasPrefix(ref, tagRefPrefix) {
		return "", false
	}
	tag := strings.TrimPrefix(ref, tagRefPrefix)
	if tag == "" {
		return "", false
	}
	return tag, true
}

// touchesReleaseFiles reports whether any commit in the push touched a
// release-relevant file. A push whose commits carry no such file cannot have
// changed the release state, so the webhook path needs no release-check for it.
func touchesReleaseFiles(p webhookPushEvent) bool {
	for _, commit := range p.Commits {
		for _, f := range commit.Modified {
			if _, ok := releaseFiles[f]; ok {
				return true
			}
		}
		for _, f := range commit.Added {
			if _, ok := releaseFiles[f]; ok {
				return true
			}
		}
	}
	return false
}

// verifyWebhookSignature checks the X-Hub-Signature-256 header ("sha256=<hex>")
// against an HMAC-SHA256 of the raw body, in constant time. An empty configured
// secret rejects everything (fail closed).
func verifyWebhookSignature(
	ctx context.Context,
	secret string,
	provided string,
	body []byte,
) error {
	if secret == "" {
		return errors.Errorf(ctx, "webhook secret not configured")
	}
	if provided == "" {
		return errors.Errorf(ctx, "missing webhook signature header")
	}
	_, sigHex, found := strings.Cut(provided, "=")
	if !found || sigHex == "" {
		return errors.Errorf(ctx, "malformed webhook signature header")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sigHex)) {
		return errors.Errorf(ctx, "invalid webhook signature")
	}
	return nil
}

// writeWebhookAck acknowledges a handled-but-not-dispatched delivery (ping,
// unsupported event) with 200 so GitHub does not retry.
func writeWebhookAck(resp http.ResponseWriter) error {
	resp.WriteHeader(http.StatusOK)
	return nil
}

// writeWebhookDispatched returns 202 with {"status":"accepted"} once the
// TriggerReleaseCheckCommand has been published to Kafka.
func writeWebhookDispatched(resp http.ResponseWriter) error {
	resp.Header().Set("Content-Type", "application/json")
	resp.WriteHeader(http.StatusAccepted)
	return json.NewEncoder(resp).Encode(map[string]string{"status": "accepted"})
}
