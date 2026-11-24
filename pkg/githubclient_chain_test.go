// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/bborbe/github-release-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var _ = Describe("NewGitHubClient transport chain", func() {
	It("retries a rate-limit response instead of returning it", func() {
		var calls int
		httpClient := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Header:     http.Header{"Retry-After": []string{"0"}},
						Body:       io.NopCloser(strings.NewReader("")),
						Request:    req,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("[]")),
					Request:    req,
				}, nil
			}),
		}

		pkg.NewGitHubClient(httpClient, "")

		req, err := http.NewRequest(http.MethodGet, "https://api.github.com/users/o/repos", nil)
		Expect(err).To(BeNil())
		resp, err := httpClient.Transport.RoundTrip(req)
		Expect(err).To(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(calls).To(Equal(2))
	})

	It("routes API calls through the configured base URL", func() {
		var gotHost string
		httpClient := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotHost = req.URL.Host
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(
						strings.NewReader(`{"total_count":0,"repositories":[]}`),
					),
					Header:  http.Header{},
					Request: req,
				}, nil
			}),
		}

		ghClient := pkg.NewGitHubClient(httpClient, "http://stub.invalid/")

		_, err := ghClient.ListRepos(context.Background(), "o")
		Expect(err).To(BeNil())
		Expect(gotHost).To(Equal("stub.invalid"))
	})
})
