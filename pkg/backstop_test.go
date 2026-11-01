// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/build"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TagBackstop", func() {
	var (
		ctx        context.Context
		ghClient   *mocks.GitHubClient
		dispatcher *mocks.BuildDispatcher
		backstop   pkg.TagBackstop
	)

	BeforeEach(func() {
		ctx = context.Background()
		ghClient = new(mocks.GitHubClient)
		dispatcher = new(mocks.BuildDispatcher)
		backstop = pkg.NewTagBackstop(ghClient, dispatcher)
	})

	It("dispatches the newest release tag with the poll trigger", func() {
		ghClient.GetLatestReleaseTagReturns("v1.2.3", nil)

		tag, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.2.3"))

		Expect(dispatcher.DispatchCallCount()).To(Equal(1))
		_, req, trigger := dispatcher.DispatchArgsForCall(0)
		Expect(req.Repo).To(Equal("bborbe/example"))
		Expect(req.Tag).To(Equal("v1.2.3"))
		Expect(trigger).To(Equal(build.TriggerPoll),
			"the backstop must be distinguishable from the webhook path in logs")
	})

	It("stays silent for a tag it already handled", func() {
		// Without this the backstop would re-dispatch every repo's tag on every
		// cycle and inflate filter_skipped_total{reason="image_exists"}.
		ghClient.GetLatestReleaseTagReturns("v1.2.3", nil)

		tag, err := backstop.Poll(ctx, "bborbe/example", "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("stays silent when the repo has published no release", func() {
		ghClient.GetLatestReleaseTagReturns("", nil)

		tag, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("propagates a dispatch failure without recording the tag", func() {
		ghClient.GetLatestReleaseTagReturns("v1.2.3", nil)
		dispatcher.DispatchReturns(errors.Errorf(ctx, "cluster unreachable"))

		tag, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).To(HaveOccurred())
		Expect(tag).To(BeEmpty(), "a failed dispatch must not be recorded as handled")
	})

	It("propagates a GitHub lookup failure", func() {
		ghClient.GetLatestReleaseTagReturns("", errors.Errorf(ctx, "rate limited"))

		_, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("NewTagBackstop", func() {
	It("returns a no-op backstop when there is no dispatcher", func() {
		backstop := pkg.NewTagBackstop(new(mocks.GitHubClient), nil)

		tag, err := backstop.Poll(context.Background(), "bborbe/example", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
	})
})
