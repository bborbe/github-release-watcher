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

	// epochs wires GetCommitEpoch to a fixed tag→committer-date table, so a
	// test can make API order and true recency disagree on purpose.
	epochs := func(table map[string]int64) {
		ghClient.GetCommitEpochStub = func(_ context.Context, _ pkg.Repo, ref string) (int64, error) {
			return table[ref], nil
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		ghClient = new(mocks.GitHubClient)
		dispatcher = new(mocks.BuildDispatcher)
		backstop = pkg.NewTagBackstop(ghClient, dispatcher)
	})

	It("dispatches the newest tag with the poll trigger on a cold cursor", func() {
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.2.2"}, nil)

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
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.2.2"}, nil)

		tag, err := backstop.Poll(ctx, "bborbe/example", "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("dispatches a tag whose commit is newer than the last built one", func() {
		ghClient.ListTagsReturns([]string{"v1.3.0", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.3.0": 200, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, "bborbe/example", "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.3.0"))
	})

	It("falls back to a later entry when API order and true recency disagree", func() {
		// The ordering decision's core fixture: GitHub returned an older tag
		// first and the newest one deeper in the page, so taking the first
		// entry would have dispatched a stale build.
		ghClient.ListTagsReturns([]string{"v1.2.2", "v1.3.0", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.2": 50, "v1.3.0": 200, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, "bborbe/example", "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.3.0"),
			"the first entry is a candidate, not an answer")
	})

	It("stays silent when no tag's commit is newer than the last built one", func() {
		// Same commit under two tags: nothing new to build.
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.2.3-hotfix"}, nil)
		epochs(map[string]int64{"v1.2.3": 100, "v1.2.3-hotfix": 100})

		tag, err := backstop.Poll(ctx, "bborbe/example", "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("dispatches the newest tag when the cursor names a tag that no longer exists", func() {
		// A deleted or rewound tag must not wedge the backstop forever.
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.2.2"}, nil)

		tag, err := backstop.Poll(ctx, "bborbe/example", "v1.1.0")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.2.3"))
	})

	It("stays silent when the repo has no tags", func() {
		ghClient.ListTagsReturns(nil, nil)

		tag, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("propagates a dispatch failure without recording the tag", func() {
		ghClient.ListTagsReturns([]string{"v1.2.3"}, nil)
		dispatcher.DispatchReturns(errors.Errorf(ctx, "cluster unreachable"))

		tag, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).To(HaveOccurred())
		Expect(tag).To(BeEmpty(), "a failed dispatch must not be recorded as handled")
	})

	It("propagates a tag-listing failure", func() {
		ghClient.ListTagsReturns(nil, errors.Errorf(ctx, "rate limited"))

		_, err := backstop.Poll(ctx, "bborbe/example", "")
		Expect(err).To(HaveOccurred())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("propagates an epoch lookup failure", func() {
		ghClient.ListTagsReturns([]string{"v1.3.0", "v1.2.3"}, nil)
		ghClient.GetCommitEpochReturns(0, errors.Errorf(ctx, "boom"))

		_, err := backstop.Poll(ctx, "bborbe/example", "v1.2.3")
		Expect(err).To(HaveOccurred())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
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
