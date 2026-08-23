// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import "github.com/prometheus/client_golang/prometheus"

//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics

// Metrics is the four observable counters required by [[Watcher Writing Guide]] §
// Required observability.
type Metrics interface {
	// IncPollCycle — result: "success" | "rate_limited" | "github_error"
	IncPollCycle(result string)

	// IncPublished — status: "create" | "error"
	IncPublished(status string)

	// IncReposScanned — increment by N repos scanned in the cycle (cardinality: none).
	IncReposScanned(n int)

	// IncFilterSkipped — reason: "empty_unreleased" | "auto_release" | "sha_unchanged" | "scope" | "fork"
	IncFilterSkipped(reason string)

	// IncWebhookDelivery increments the webhook delivery counter with the given result label.
	// result: "success", "skip"
	IncWebhookDelivery(result string)

	// IncWebhookSignatureRejected increments the webhook signature-rejection counter.
	IncWebhookSignatureRejected()

	// ObserveWebhookDispatchLatency records the dispatch latency of a webhook delivery.
	ObserveWebhookDispatchLatency(seconds float64)

	// IncWebhookSkipped increments the webhook skip counter.
	// reason: "no_release_files" | "debounced"
	IncWebhookSkipped(reason string)

	// SetRateLimitRemaining records the primary rate-limit window's remaining
	// requests (shared App token). The alert rule on this gauge fires BEFORE
	// quota exhaustion, catching the 2026-08-23 silent fleet-wide stall.
	SetRateLimitRemaining(remaining int)
}

const metricNamespace = "github_release_watcher"

// NewMetrics returns the Prometheus-backed Metrics implementation registered
// against the supplied Registerer. Pass nil for the default registry.
// Pre-initialises every label combination so Prometheus exposes a zero series
// before the first event fires.
func NewMetrics(registerer prometheus.Registerer) Metrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	m := &prometheusMetrics{
		pollCycleTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "poll_cycle_total",
			Help:      "Total poll cycles by result.",
		}, []string{"result"}),
		publishedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "published_total",
			Help:      "Total task-publish attempts by status.",
		}, []string{"status"}),
		reposScannedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "repos_scanned_total",
			Help:      "Total number of repos scanned across all poll cycles.",
		}),
		filterSkippedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "filter_skipped_total",
			Help:      "Total releases filtered out by reason.",
		}, []string{"reason"}),
		webhookDeliveriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "webhook_deliveries_total",
			Help:      "Total GitHub webhook deliveries by result.",
		}, []string{"result"}),
		webhookSignatureRejectionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "webhook_signature_rejections_total",
			Help:      "Total GitHub webhook payloads rejected for an invalid HMAC signature.",
		}),
		webhookDispatchLatencySeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "webhook_dispatch_latency_seconds",
			Help:      "Latency of dispatching a GitHub webhook delivery to Kafka.",
			Buckets:   prometheus.DefBuckets,
		}),
		webhookSkippedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "webhook_skipped_total",
			Help:      "Total GitHub webhook push deliveries skipped by reason.",
		}, []string{"reason"}),
		rateLimitRemaining: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "rate_limit_remaining",
			Help:      "Requests remaining in the GitHub primary rate-limit window (shared App token). Zero until the first API response populates it.",
		}),
	}
	registerer.MustRegister(
		m.pollCycleTotal,
		m.publishedTotal,
		m.reposScannedTotal,
		m.filterSkippedTotal,
		m.webhookDeliveriesTotal,
		m.webhookSignatureRejectionsTotal,
		m.webhookDispatchLatencySeconds,
		m.webhookSkippedTotal,
		m.rateLimitRemaining,
	)
	for _, r := range []string{"success", "rate_limited", "github_error"} {
		m.pollCycleTotal.WithLabelValues(r).Add(0)
	}
	for _, s := range []string{"create", "error"} {
		m.publishedTotal.WithLabelValues(s).Add(0)
	}
	for _, r := range []string{"empty_unreleased", "auto_release", "sha_unchanged", "scope", "fork"} {
		m.filterSkippedTotal.WithLabelValues(r).Add(0)
	}
	for _, r := range []string{"success", "skip"} {
		m.webhookDeliveriesTotal.WithLabelValues(r).Add(0)
	}
	for _, r := range []string{"no_release_files", "debounced"} {
		m.webhookSkippedTotal.WithLabelValues(r).Add(0)
	}
	return m
}

type prometheusMetrics struct {
	pollCycleTotal     *prometheus.CounterVec
	publishedTotal     *prometheus.CounterVec
	reposScannedTotal  prometheus.Counter
	filterSkippedTotal *prometheus.CounterVec

	webhookDeliveriesTotal          *prometheus.CounterVec
	webhookSignatureRejectionsTotal prometheus.Counter
	webhookDispatchLatencySeconds   prometheus.Histogram
	webhookSkippedTotal             *prometheus.CounterVec
	rateLimitRemaining              prometheus.Gauge
}

func (m *prometheusMetrics) IncPollCycle(result string) {
	m.pollCycleTotal.WithLabelValues(result).Inc()
}

func (m *prometheusMetrics) IncPublished(status string) {
	m.publishedTotal.WithLabelValues(status).Inc()
}

func (m *prometheusMetrics) IncReposScanned(n int) {
	m.reposScannedTotal.Add(float64(n))
}

func (m *prometheusMetrics) IncFilterSkipped(reason string) {
	m.filterSkippedTotal.WithLabelValues(reason).Inc()
}

func (m *prometheusMetrics) IncWebhookDelivery(result string) {
	m.webhookDeliveriesTotal.WithLabelValues(result).Inc()
}

func (m *prometheusMetrics) IncWebhookSignatureRejected() {
	m.webhookSignatureRejectionsTotal.Inc()
}

func (m *prometheusMetrics) IncWebhookSkipped(reason string) {
	m.webhookSkippedTotal.WithLabelValues(reason).Inc()
}

func (m *prometheusMetrics) SetRateLimitRemaining(remaining int) {
	m.rateLimitRemaining.Set(float64(remaining))
}

func (m *prometheusMetrics) ObserveWebhookDispatchLatency(seconds float64) {
	m.webhookDispatchLatencySeconds.Observe(seconds)
}
