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

// exampleRepo is the production shape: a host-qualified key (Repo.Key()) is
// what the cursor is stored under, and the struct is what Poll now takes.
var exampleRepo = pkg.Repo{Owner: "bborbe", Name: "example"}

var _ = Describe("TagBackstop", func() {
	var (
		ctx        context.Context
		ghClient   *mocks.GitHubClient
		dispatcher *mocks.BuildDispatcher
		backstop   pkg.TagBackstop
	)

	// epochs wires GetCommitEpoch to a fixed tag→committer-date table, so a
	// test can make API order and true recency disagree on purpose. An unknown
	// ref FAILS the test rather than defaulting to 0: a silent zero would stand
	// in for a wrong-ref lookup and let a broken comparison pass.
	epochs := func(table map[string]int64) {
		ghClient.GetCommitEpochStub = func(_ context.Context, _ pkg.Repo, ref string) (int64, error) {
			epoch, ok := table[ref]
			Expect(ok).To(BeTrue(), "unexpected GetCommitEpoch ref %q", ref)
			return epoch, nil
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
		epochs(map[string]int64{"v1.2.3": 200, "v1.2.2": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.2.3"))

		Expect(dispatcher.DispatchCallCount()).To(Equal(1))
		_, req, trigger := dispatcher.DispatchArgsForCall(0)
		Expect(req.Repo).To(Equal("bborbe/example"))
		Expect(req.Tag).To(Equal("v1.2.3"))
		Expect(trigger).To(Equal(build.TriggerPoll),
			"the backstop must be distinguishable from the webhook path in logs")
	})

	It("passes the Repo through and dispatches the bare owner/name", func() {
		// Regression, found live on dev at v0.12.1 and not by these specs: the
		// backstop used to take repo.Key() and re-split it with repoFromKey,
		// which cuts on the FIRST "/". "github.com/bborbe/go-skeleton" therefore
		// parsed as Owner="github.com", Name="bborbe/go-skeleton", every GitHub
		// call asked for /repos/github.com/bborbe/go-skeleton/... , the 404 was
		// mapped to an empty tag list, and the backstop reported "nothing to do"
		// every cycle — the silent dead path this whole change exists to remove.
		// Both halves are asserted: the client sees the struct untouched, and the
		// build pipeline sees the bare form its own repoFromKey expects.
		ghClient.ListTagsReturns([]string{"v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.3": 200})

		_, err := backstop.Poll(ctx, exampleRepo, "")
		Expect(err).NotTo(HaveOccurred())

		_, listed := ghClient.ListTagsArgsForCall(0)
		Expect(listed).To(Equal(exampleRepo),
			"the Repo must reach the client intact, never round-tripped through a string")

		Expect(dispatcher.DispatchCallCount()).To(Equal(1))
		_, req, _ := dispatcher.DispatchArgsForCall(0)
		Expect(req.Repo).To(Equal("bborbe/example"),
			"build.Request.Repo is re-split by repoFromKey and must be the bare owner/name")
	})

	It("stays silent for a tag it already handled", func() {
		// Without this the backstop would re-dispatch every repo's tag on every
		// cycle and inflate filter_skipped_total{reason="image_exists"}.
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.2.2"}, nil)

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("dispatches a tag whose commit is newer than the last built one", func() {
		ghClient.ListTagsReturns([]string{"v1.3.0", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.3.0": 200, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.3.0"))
	})

	It("falls back to a later entry when API order and true recency disagree", func() {
		// The ordering decision's core fixture: GitHub returned an older tag
		// first and the newest one deeper in the page, so taking the first
		// entry would have dispatched a stale build.
		ghClient.ListTagsReturns([]string{"v1.2.2", "v1.3.0", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.2": 50, "v1.3.0": 200, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.3.0"),
			"the first entry is a candidate, not an answer")
	})

	It("dispatches the tag with the greatest epoch, not the first one past the cursor", func() {
		// Two entries both beat the cursor. Dispatching the first would advance
		// the cursor to v1.2.4 and then let the index-0 shortcut mask v1.3.0 on
		// every later cycle, so the newest build would never fire.
		ghClient.ListTagsReturns([]string{"v1.2.4", "v1.3.0", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.4": 100, "v1.3.0": 200, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.3.0"))
	})

	It("stays silent when the cursor is the first entry and a newer tag sits deeper", func() {
		// Pins the accepted trade-off documented on the index-0 shortcut: the
		// one-call steady state trusts API ordering, so a reordered page with the
		// cursor first is not scanned. Asserted so the limit is deliberate and
		// visible rather than discovered later.
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.3.0"}, nil)
		epochs(map[string]int64{"v1.2.3": 100, "v1.3.0": 200})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(ghClient.GetCommitEpochCallCount()).To(Equal(0),
			"the steady-state shortcut must not pay for an epoch call")
	})

	It("dispatches a new tag name on an already-built commit", func() {
		// The image is keyed by tag name, so v1.2.4 on v1.2.3's commit has no
		// image yet and must build. tags[0] is deliberately not the cursor, so
		// the index-0 shortcut cannot hide the epoch comparison.
		ghClient.ListTagsReturns([]string{"v1.2.4", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.4": 100, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.2.4"))
		Expect(ghClient.GetCommitEpochCallCount()).To(BeNumerically(">", 0),
			"the tie must be decided by comparing epochs, not by an early return")
	})

	It("stays silent when every other tag sits on an older commit", func() {
		ghClient.ListTagsReturns([]string{"v1.2.2", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.2": 50, "v1.2.3": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("stops the scan between entries when the context is cancelled mid-scan", func() {
		// Cancelling on entry proves nothing about the loop; this cancels after
		// the first candidate resolves and asserts the remaining tags are never
		// looked up.
		scanCtx, cancel := context.WithCancel(context.Background())
		ghClient.ListTagsReturns([]string{"v1.2.2", "v1.2.1", "v1.2.3"}, nil)
		candidateCalls := 0
		ghClient.GetCommitEpochStub = func(_ context.Context, _ pkg.Repo, ref string) (int64, error) {
			if ref == "v1.2.3" {
				return 100, nil
			}
			candidateCalls++
			if candidateCalls == 1 {
				cancel()
			}
			return 50, nil
		}

		_, err := backstop.Poll(scanCtx, exampleRepo, "v1.2.3")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cancelled"))
		Expect(candidateCalls).To(Equal(1),
			"the scan must stop before resolving the remaining entries")
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("stops the fallback scan when the context is cancelled", func() {
		// The scan is the only path that can spend a whole page of API calls, so
		// a cancelled cycle must stop there rather than keep going.
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		ghClient.ListTagsReturns([]string{"v1.2.2", "v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.2": 50, "v1.2.3": 100})

		_, err := backstop.Poll(cancelled, exampleRepo, "v1.2.3")
		Expect(err).To(HaveOccurred())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("dispatches the newest tag when the cursor names a tag that no longer exists", func() {
		// A deleted or rewound tag must not wedge the backstop forever.
		ghClient.ListTagsReturns([]string{"v1.2.3", "v1.2.2"}, nil)
		epochs(map[string]int64{"v1.2.3": 200, "v1.2.2": 100})

		tag, err := backstop.Poll(ctx, exampleRepo, "v1.1.0")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(Equal("v1.2.3"))
	})

	It("stays silent when the repo has no tags", func() {
		ghClient.ListTagsReturns(nil, nil)

		tag, err := backstop.Poll(ctx, exampleRepo, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("propagates a dispatch failure without recording the tag", func() {
		ghClient.ListTagsReturns([]string{"v1.2.3"}, nil)
		epochs(map[string]int64{"v1.2.3": 200})
		dispatcher.DispatchReturns(errors.Errorf(ctx, "cluster unreachable"))

		tag, err := backstop.Poll(ctx, exampleRepo, "")
		Expect(err).To(HaveOccurred())
		Expect(tag).To(BeEmpty(), "a failed dispatch must not be recorded as handled")
	})

	It("propagates a tag-listing failure", func() {
		ghClient.ListTagsReturns(nil, errors.Errorf(ctx, "rate limited"))

		_, err := backstop.Poll(ctx, exampleRepo, "")
		Expect(err).To(HaveOccurred())
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("propagates a failure resolving the cursor's epoch", func() {
		ghClient.ListTagsReturns([]string{"v1.3.0", "v1.2.3"}, nil)
		ghClient.GetCommitEpochReturns(0, errors.Errorf(ctx, "boom"))

		_, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("epoch last built tag="))
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})

	It("propagates a failure resolving a candidate tag's epoch", func() {
		// Distinct from the cursor failure above: the two wrap different
		// messages, and only one of them means the cursor is unusable.
		ghClient.ListTagsReturns([]string{"v1.3.0", "v1.2.3"}, nil)
		ghClient.GetCommitEpochStub = func(_ context.Context, _ pkg.Repo, ref string) (int64, error) {
			if ref == "v1.2.3" {
				return 100, nil
			}
			return 0, errors.Errorf(ctx, "boom")
		}

		_, err := backstop.Poll(ctx, exampleRepo, "v1.2.3")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("epoch candidate tag="))
		Expect(dispatcher.DispatchCallCount()).To(Equal(0))
	})
})

var _ = Describe("NewTagBackstop", func() {
	It("returns a no-op backstop when there is no dispatcher", func() {
		backstop := pkg.NewTagBackstop(new(mocks.GitHubClient), nil)

		tag, err := backstop.Poll(context.Background(), exampleRepo, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tag).To(BeEmpty())
	})
})
