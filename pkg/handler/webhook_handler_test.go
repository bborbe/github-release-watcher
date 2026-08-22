// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/handler"
	libhttp "github.com/bborbe/http"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const webhookTestSecret = "test-secret"

// signBody produces the X-Hub-Signature-256 value GitHub would send for a body.
func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

var _ = Describe("WebhookHandler", func() {
	var (
		ctx     context.Context
		sender  *mocks.TriggerReleaseCheckCommandSender
		metrics *mocks.WebhookMetrics
		h       http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		sender = new(mocks.TriggerReleaseCheckCommandSender)
		metrics = new(mocks.WebhookMetrics)
		h = libhttp.NewErrorHandler(
			handler.NewWebhookHandler(
				sender,
				webhookTestSecret,
				metrics,
				libtime.NewCurrentDateTime(),
			),
		)
	})

	// webhookRequest builds a signed POST /webhook/github-release with the given
	// GitHub event and raw payload body.
	webhookRequest := func(event, payload string) *http.Request {
		req := httptest.NewRequest("POST", "/webhook/github-release", strings.NewReader(payload))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", signBody(webhookTestSecret, []byte(payload)))
		return req
	}

	pushPayload := func(repo string) string {
		return `{"ref":"refs/heads/master","repository":{"full_name":"` + repo + `"}}`
	}

	Context("signature verification", func() {
		It(
			"rejects a missing signature with 401, no publish, increments rejection counter",
			func() {
				req := httptest.NewRequest(
					"POST",
					"/webhook/github-release",
					strings.NewReader(pushPayload("bborbe/repo")),
				)
				req.Header.Set("X-GitHub-Event", "push")
				resp := httptest.NewRecorder()
				h.ServeHTTP(resp, req)

				Expect(resp.Code).To(Equal(http.StatusUnauthorized))
				Expect(sender.SendCommandCallCount()).To(Equal(0))
				Expect(metrics.IncWebhookSignatureRejectedCallCount()).To(Equal(1))
			},
		)

		It(
			"rejects an invalid signature with 401, no publish, increments rejection counter",
			func() {
				payload := pushPayload("bborbe/repo")
				req := httptest.NewRequest(
					"POST",
					"/webhook/github-release",
					strings.NewReader(payload),
				)
				req.Header.Set("X-GitHub-Event", "push")
				req.Header.Set(
					"X-Hub-Signature-256",
					"sha256=0000000000000000000000000000000000000000000000000000000000000000",
				)
				resp := httptest.NewRecorder()
				h.ServeHTTP(resp, req)

				Expect(resp.Code).To(Equal(http.StatusUnauthorized))
				Expect(sender.SendCommandCallCount()).To(Equal(0))
				Expect(metrics.IncWebhookSignatureRejectedCallCount()).To(Equal(1))
			},
		)

		It("rejects everything when the secret is not configured (fail closed)", func() {
			closed := libhttp.NewErrorHandler(
				handler.NewWebhookHandler(sender, "", metrics, libtime.NewCurrentDateTime()),
			)
			req := webhookRequest("push", pushPayload("bborbe/repo"))
			resp := httptest.NewRecorder()
			closed.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusUnauthorized))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})
	})

	Context("event routing", func() {
		It("acks ping with 200 and no publish", func() {
			req := webhookRequest("ping", `{"zen":"keep it simple"}`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})

		It("acks an unsupported event with 200 and no publish", func() {
			req := webhookRequest("pull_request", `{}`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})
	})

	Context("push dispatch", func() {
		It("returns 202 and publishes the repo scope on push", func() {
			req := webhookRequest("push", pushPayload("bborbe/repo"))
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusAccepted))
			Expect(sender.SendCommandCallCount()).To(Equal(1))
			_, sentCmd := sender.SendCommandArgsForCall(0)
			Expect(sentCmd.Scope).To(Equal("bborbe/repo"))
			Expect(sentCmd.Force).To(BeFalse())
			Expect(metrics.IncWebhookDeliveryCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookDeliveryArgsForCall(0)).To(Equal("success"))
			Expect(metrics.ObserveWebhookDispatchLatencyCallCount()).To(Equal(1))
		})

		It("rejects a malformed payload with 400", func() {
			req := webhookRequest("push", `{not json`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusBadRequest))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})

		It("rejects a payload without repository.full_name with 400", func() {
			req := webhookRequest("push", `{"ref":"refs/heads/master","repository":{}}`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusBadRequest))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})

		It("returns 502 when the Kafka send fails", func() {
			sender.SendCommandReturns(errors.Errorf(ctx, "kafka error"))
			req := webhookRequest("push", pushPayload("bborbe/repo"))
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusBadGateway))
			Expect(sender.SendCommandCallCount()).To(Equal(1))
		})
	})
})
