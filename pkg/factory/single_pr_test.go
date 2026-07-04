// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/factory"
	libhttp "github.com/bborbe/http"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateTriggerReleaseCheckHandler", func() {
	var sender *mocks.TriggerReleaseCheckCommandSender

	BeforeEach(func() {
		sender = new(mocks.TriggerReleaseCheckCommandSender)
	})

	It("returns a non-nil handler", func() {
		handler := factory.CreateTriggerReleaseCheckHandler(sender)
		Expect(handler).NotTo(BeNil())
	})

	It("handler responds to a request", func() {
		handler := factory.CreateTriggerReleaseCheckHandler(sender)
		wrapped := libhttp.NewErrorHandler(handler)
		sender.SendCommandReturns(nil)
		req := httptest.NewRequest("POST", "/trigger", nil)
		//nolint:contextcheck // test setup uses Background; safe in tests
		req = req.WithContext(context.Background())
		resp := httptest.NewRecorder()
		wrapped.ServeHTTP(resp, req)
		Expect(resp.Code).To(Equal(http.StatusAccepted))
		Expect(sender.SendCommandCallCount()).To(Equal(1))
	})
})
