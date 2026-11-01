// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseImageRef", func() {
	DescribeTable("splits registry, repository and tag",
		func(image, registry, repository, tag string) {
			ref, err := build.ParseImageRef(image)
			Expect(err).NotTo(HaveOccurred())
			Expect(ref.Registry).To(Equal(registry))
			Expect(ref.Repository).To(Equal(repository))
			Expect(ref.Tag).To(Equal(tag))
		},
		Entry("docker hub short form",
			"bborbe/example:v1.2.3", "registry-1.docker.io", "bborbe/example", "v1.2.3"),
		Entry("docker hub explicit",
			"docker.io/bborbe/example:v1.2.3", "registry-1.docker.io", "bborbe/example", "v1.2.3"),
		Entry("docker hub official image gets the library prefix",
			"alpine:3.20", "registry-1.docker.io", "library/alpine", "3.20"),
		Entry("other registry keeps its host",
			"ghcr.io/org/example:v1", "ghcr.io", "org/example", "v1"),
		Entry("registry with a port",
			"registry.example.com:5000/org/example:v1",
			"registry.example.com:5000", "org/example", "v1"),
		Entry("missing tag defaults to latest",
			"bborbe/example", "registry-1.docker.io", "bborbe/example", "latest"),
	)

	It("rejects a digest reference", func() {
		_, err := build.ParseImageRef("bborbe/example@sha256:abc")
		Expect(err).To(HaveOccurred())
	})

	It("rejects an empty reference", func() {
		_, err := build.ParseImageRef("  ")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("RegistryImageChecker", func() {
	var (
		ctx      context.Context
		server   *httptest.Server
		status   int
		checker  build.ImageChecker
		imageRef string
	)

	BeforeEach(func() {
		ctx = context.Background()
		status = http.StatusOK
		server = httptest.NewTLSServer(http.HandlerFunc(
			func(resp http.ResponseWriter, _ *http.Request) {
				resp.WriteHeader(status)
			},
		))
		checker = build.NewRegistryImageChecker(server.Client())
		imageRef = strings.TrimPrefix(server.URL, "https://") + "/bborbe/example:v1.2.3"
	})

	AfterEach(func() {
		server.Close()
	})

	It("reports an existing image", func() {
		exists, err := checker.Exists(ctx, imageRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("reports a missing image", func() {
		status = http.StatusNotFound
		exists, err := checker.Exists(ctx, imageRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("returns an error for a non-404 failure — never a silent 'absent'", func() {
		// This is the fail-closed contract: an unreadable registry must surface
		// as an error so the dispatcher skips rather than re-pushing a tag.
		status = http.StatusInternalServerError
		exists, err := checker.Exists(ctx, imageRef)
		Expect(err).To(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("returns an error when the registry is unreachable", func() {
		server.Close()
		_, err := checker.Exists(ctx, imageRef)
		Expect(err).To(HaveOccurred())
	})
})
