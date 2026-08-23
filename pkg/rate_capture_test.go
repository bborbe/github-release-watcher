// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rateCapturingTransport", func() {
	It("captures X-RateLimit-Remaining from each response", func() {
		var captured int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Both calls are void in net/http; the header value is asserted below
			// via the captured gauge value.
			w.Header().Set("X-RateLimit-Remaining", "7342")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tr := &rateCapturingTransport{
			inner: srv.Client().Transport,
			set:   func(n int) { captured = n },
		}
		req, err := http.NewRequest("GET", srv.URL, nil)
		Expect(err).NotTo(HaveOccurred())
		resp, err := tr.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		Expect(captured).To(Equal(7342))
		Expect(strconv.Itoa(captured)).To(Equal("7342"))
	})
})

var _ = Describe("rateCapturingTransport edge cases", func() {
	It("captures the header even on a non-2xx response", func() {
		var captured int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Both calls are void in net/http; the 403 status is asserted below.
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, "rate limited", http.StatusForbidden)
		}))
		defer srv.Close()

		tr := &rateCapturingTransport{
			inner: srv.Client().Transport,
			set:   func(n int) { captured = n },
		}
		req, err := http.NewRequest("GET", srv.URL, nil)
		Expect(err).NotTo(HaveOccurred())
		resp, err := tr.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))

		Expect(captured).To(Equal(0))
	})
})

var _ = Describe("rateCapturingTransport error path", func() {
	It("preserves the previous captured value when the inner transport errors", func() {
		var captured = 42
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			}),
			set: func(n int) { captured = n },
		}
		req, err := http.NewRequest("GET", "https://api.github.com/x", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = tr.RoundTrip(req)
		Expect(err).To(HaveOccurred())
		Expect(captured).To(Equal(42))
	})
})

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
