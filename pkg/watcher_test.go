// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"

	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/bborbe/github-release-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/maintainer/lib/maintainerconfig"
)

var _ = Describe("pkg.Watcher.Poll", func() {
	var (
		ctx        context.Context
		ghClient   *mocks.GitHubClient
		publisher  *mocks.TaskPublisher
		metrics    *mocks.Metrics
		cursorPath string
		tmpDir     string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "watcher-poll-*")
		Expect(err).NotTo(HaveOccurred())
		cursorPath = filepath.Join(tmpDir, "cursor.json")

		ghClient = &mocks.GitHubClient{}
		publisher = &mocks.TaskPublisher{}
		metrics = &mocks.Metrics{}
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir) // #nosec G104 -- best-effort temp dir cleanup
	})

	Describe("Poll publishes one task per non-skipped repo and saves cursor", func() {
		var staticFilters filter.TaskCreationFilter

		BeforeEach(func() {
			staticFilters = filter.TaskCreationFilters{
				filter.NewRepoAllowlistFilter(nil),
				filter.NewEmptyUnreleasedFilter(),
				filter.NewAutoReleaseFilter(),
			}

			ghClient.ListReposReturns([]pkg.Repo{
				{Owner: "bborbe", Name: "docker-utils", DefaultBranch: "master"},
				{Owner: "bborbe", Name: "empty-repo", DefaultBranch: "main"},
			}, nil)

			ghClient.GetMasterSHAStub = func(_ context.Context, r pkg.Repo) (string, error) {
				if r.Name == "docker-utils" {
					return "d630ef3526cfc57fbdccd9ba53c5c3a02945e407", nil
				}
				return "abc123def456789", nil
			}

			ghClient.GetChangelogContentStub = func(_ context.Context, r pkg.Repo) ([]byte, error) {
				if r.Name == "docker-utils" {
					return []byte("## Unreleased\n\n- entry one\n\n## v1.7.7\n"), nil
				}
				// empty-repo: no unreleased entries
				return []byte("## Unreleased\n\n## v0.0.1\n"), nil
			}

			ghClient.GetMaintainerConfigStub = func(_ context.Context, r pkg.Repo) (maintainerconfig.MaintainerConfig, error) {
				return maintainerconfig.MaintainerConfig{
					Release: maintainerconfig.ReleaseConfig{AutoRelease: true},
				}, nil
			}

			publisher.PublishCreateReturns(true)
		})

		It("Poll publishes one task per non-skipped repo and saves cursor", func() {
			w := pkg.NewWatcher(
				ghClient,
				publisher,
				metrics,
				cursorPath,
				"bborbe",
				staticFilters,
			)

			Expect(w.Poll(ctx, false)).To(Succeed())

			// Only docker-utils passes through (empty-repo skipped by EmptyUnreleasedFilter)
			Expect(publisher.PublishCreateCallCount()).To(Equal(1))
			_, release := publisher.PublishCreateArgsForCall(0)
			Expect(release.Repo.Name).To(Equal("docker-utils"))
			Expect(release.HeadSHA).To(Equal("d630ef3526cfc57fbdccd9ba53c5c3a02945e407"))
			Expect(release.CurrentVersion).To(Equal("v1.7.7"))
			Expect(release.UnreleasedBullets).To(Equal(1))

			// Cursor file was written
			_, err := os.Stat(cursorPath)
			Expect(err).NotTo(HaveOccurred())

			loaded, err := pkg.LoadCursor(ctx, cursorPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(
				loaded.Repos["github.com/bborbe/docker-utils"].LastSeenMasterSHA,
			).To(Equal("d630ef3526cfc57fbdccd9ba53c5c3a02945e407"))
			// empty-repo was skipped — no cursor entry
			_, exists := loaded.Repos["github.com/bborbe/empty-repo"]
			Expect(exists).To(BeFalse())

			// Metrics
			Expect(metrics.IncPollCycleCallCount()).To(Equal(1))
			Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("success"))
			Expect(metrics.IncFilterSkippedCallCount()).To(Equal(1))
			Expect(metrics.IncFilterSkippedArgsForCall(0)).To(Equal("empty_unreleased"))
			Expect(metrics.IncReposScannedCallCount()).To(Equal(1))
			Expect(metrics.IncReposScannedArgsForCall(0)).To(Equal(2))
		})
	})

	Describe("Poll aborts cycle and skips cursor save on ListRepos rate-limit", func() {
		BeforeEach(func() {
			ghClient.ListReposReturns(nil, pkg.ErrRateLimited)
		})

		It("Poll aborts cycle and skips cursor save on ListRepos rate-limit", func() {
			staticFilters := filter.TaskCreationFilters{
				filter.NewRepoAllowlistFilter(nil),
				filter.NewEmptyUnreleasedFilter(),
				filter.NewAutoReleaseFilter(),
			}
			w := pkg.NewWatcher(
				ghClient,
				publisher,
				metrics,
				cursorPath,
				"bborbe",
				staticFilters,
			)

			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(metrics.IncPollCycleCallCount()).To(Equal(1))
			Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("rate_limited"))
			Expect(publisher.PublishCreateCallCount()).To(Equal(0))

			// Cursor file was NOT written
			_, err := os.Stat(cursorPath)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Describe("Poll aborts cycle and skips cursor save on ListRepos github_error", func() {
		BeforeEach(func() {
			ghClient.ListReposReturns(nil, stderrors.New("500 internal error"))
		})

		It("Poll aborts cycle and skips cursor save on ListRepos github_error", func() {
			staticFilters := filter.TaskCreationFilters{
				filter.NewRepoAllowlistFilter(nil),
				filter.NewEmptyUnreleasedFilter(),
				filter.NewAutoReleaseFilter(),
			}
			w := pkg.NewWatcher(
				ghClient,
				publisher,
				metrics,
				cursorPath,
				"bborbe",
				staticFilters,
			)

			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(metrics.IncPollCycleCallCount()).To(Equal(1))
			Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("github_error"))
			Expect(publisher.PublishCreateCallCount()).To(Equal(0))

			_, err := os.Stat(cursorPath)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Describe(
		"Poll prunes individual repos with transient GetMasterSHA errors and continues",
		func() {
			BeforeEach(func() {
				ghClient.ListReposReturns([]pkg.Repo{
					{Owner: "bborbe", Name: "failing-repo", DefaultBranch: "master"},
					{Owner: "bborbe", Name: "good-repo", DefaultBranch: "main"},
				}, nil)

				ghClient.GetMasterSHAStub = func(_ context.Context, r pkg.Repo) (string, error) {
					if r.Name == "failing-repo" {
						return "", stderrors.New("transient network error")
					}
					return "abc123def456789", nil
				}

				ghClient.GetChangelogContentStub = func(_ context.Context, r pkg.Repo) ([]byte, error) {
					return []byte("## Unreleased\n\n- fix bug\n\n## v2.0.0\n"), nil
				}

				ghClient.GetMaintainerConfigReturns(
					maintainerconfig.MaintainerConfig{
						Release: maintainerconfig.ReleaseConfig{AutoRelease: true},
					},
					nil,
				)
				publisher.PublishCreateReturns(true)
			})

			It(
				"Poll prunes individual repos with transient GetMasterSHA errors and continues",
				func() {
					staticFilters := filter.TaskCreationFilters{
						filter.NewRepoAllowlistFilter(nil),
						filter.NewEmptyUnreleasedFilter(),
						filter.NewAutoReleaseFilter(),
					}
					w := pkg.NewWatcher(
						ghClient,
						publisher,
						metrics,
						cursorPath,
						"bborbe",
						staticFilters,
					)

					Expect(w.Poll(ctx, false)).To(Succeed())

					Expect(metrics.IncPollCycleCallCount()).To(Equal(1))
					Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("success"))

					// Only good-repo was published
					Expect(publisher.PublishCreateCallCount()).To(Equal(1))
					_, release := publisher.PublishCreateArgsForCall(0)
					Expect(release.Repo.Name).To(Equal("good-repo"))

					// Cursor saved with only good-repo's entry
					loaded, err := pkg.LoadCursor(ctx, cursorPath)
					Expect(err).NotTo(HaveOccurred())
					_, exists := loaded.Repos["github.com/bborbe/good-repo"]
					Expect(exists).To(BeTrue())
					_, exists = loaded.Repos["github.com/bborbe/failing-repo"]
					Expect(exists).To(BeFalse())
				},
			)
		},
	)

	Describe("Poll aborts mid-cycle on per-repo rate-limit during GetChangelogContent", func() {
		BeforeEach(func() {
			ghClient.ListReposReturns([]pkg.Repo{
				{Owner: "bborbe", Name: "rate-limited-repo", DefaultBranch: "master"},
				{Owner: "bborbe", Name: "other-repo", DefaultBranch: "main"},
			}, nil)

			ghClient.GetMasterSHAStub = func(_ context.Context, r pkg.Repo) (string, error) {
				if r.Name == "rate-limited-repo" {
					return "sha1abc123", nil
				}
				return "sha2def456", nil
			}

			ghClient.GetChangelogContentStub = func(_ context.Context, r pkg.Repo) ([]byte, error) {
				if r.Name == "rate-limited-repo" {
					return nil, pkg.ErrRateLimited
				}
				return []byte("## Unreleased\n\n- bugfix\n\n## v1.0.0\n"), nil
			}

			ghClient.GetMaintainerConfigReturns(
				maintainerconfig.MaintainerConfig{
					Release: maintainerconfig.ReleaseConfig{AutoRelease: true},
				},
				nil,
			)
		})

		It("Poll aborts mid-cycle on per-repo rate-limit during GetChangelogContent", func() {
			staticFilters := filter.TaskCreationFilters{
				filter.NewRepoAllowlistFilter(nil),
				filter.NewEmptyUnreleasedFilter(),
				filter.NewAutoReleaseFilter(),
			}
			w := pkg.NewWatcher(
				ghClient,
				publisher,
				metrics,
				cursorPath,
				"bborbe",
				staticFilters,
			)

			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(metrics.IncPollCycleCallCount()).To(Equal(1))
			Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("rate_limited"))

			// Neither repo published (cycle aborted mid-way)
			Expect(publisher.PublishCreateCallCount()).To(Equal(0))

			// No cursor file
			_, err := os.Stat(cursorPath)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Describe("Poll updates cursor only for repos that successfully publish", func() {
		BeforeEach(func() {
			ghClient.ListReposReturns([]pkg.Repo{
				{Owner: "bborbe", Name: "docker-utils", DefaultBranch: "master"},
			}, nil)

			ghClient.GetMasterSHAReturns("d630ef3526cfc57fbdccd9ba53c5c3a02945e407", nil)
			ghClient.GetChangelogContentReturns(
				[]byte("## Unreleased\n\n- entry\n\n## v1.7.7\n"),
				nil,
			)
			ghClient.GetMaintainerConfigReturns(
				maintainerconfig.MaintainerConfig{
					Release: maintainerconfig.ReleaseConfig{AutoRelease: true},
				},
				nil,
			)

			// Simulate Kafka send failure — PublishCreate returns false
			publisher.PublishCreateReturns(false)
		})

		It("Poll updates cursor only for repos that successfully publish", func() {
			staticFilters := filter.TaskCreationFilters{
				filter.NewRepoAllowlistFilter(nil),
				filter.NewEmptyUnreleasedFilter(),
				filter.NewAutoReleaseFilter(),
			}
			w := pkg.NewWatcher(
				ghClient,
				publisher,
				metrics,
				cursorPath,
				"bborbe",
				staticFilters,
			)

			Expect(w.Poll(ctx, false)).To(Succeed())

			// Poll itself succeeds
			Expect(metrics.IncPollCycleCallCount()).To(Equal(1))
			Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("success"))

			// Cursor file exists (the Poll itself ran to completion)
			_, err := os.Stat(cursorPath)
			Expect(err).NotTo(HaveOccurred())

			// But cursor has no entry for docker-utils (publish failed)
			loaded, err := pkg.LoadCursor(ctx, cursorPath)
			Expect(err).NotTo(HaveOccurred())
			_, exists := loaded.Repos["github.com/bborbe/docker-utils"]
			Expect(exists).To(BeFalse())
		})
	})

	Describe(
		"Poll(ctx, true) reconsiders repos whose head SHA matches the cursor (spec 071)",
		func() {
			var staticFilters filter.TaskCreationFilter

			BeforeEach(func() {
				staticFilters = filter.TaskCreationFilters{
					filter.NewRepoAllowlistFilter(nil),
					filter.NewEmptyUnreleasedFilter(),
					filter.NewAutoReleaseFilter(),
				}

				ghClient.ListReposReturns([]pkg.Repo{
					{Owner: "bborbe", Name: "docker-utils", DefaultBranch: "master"},
				}, nil)

				ghClient.GetMasterSHAReturns("d630ef3526cfc57fbdccd9ba53c5c3a02945e407", nil)
				ghClient.GetChangelogContentReturns(
					[]byte("## Unreleased\n\n- entry one\n\n## v1.7.7\n"), nil,
				)
				ghClient.GetMaintainerConfigReturns(
					maintainerconfig.MaintainerConfig{
						Release: maintainerconfig.ReleaseConfig{AutoRelease: true},
					}, nil,
				)
				publisher.PublishCreateReturns(true)

				// Pre-populate cursor so docker-utils' LastSeenMasterSHA == headSHA.
				// The next cycle's SHAUnchangedFilter (when composed) skips this repo.
				Expect(pkg.SaveCursor(ctx, cursorPath, &pkg.Cursor{
					Repos: map[string]*pkg.RepoState{
						"github.com/bborbe/docker-utils": {
							LastSeenMasterSHA: "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
						},
					},
				})).To(Succeed())
			})

			It("Poll(ctx, false) skips docker-utils via sha_unchanged", func() {
				w := pkg.NewWatcher(
					ghClient, publisher, metrics, cursorPath, "bborbe", staticFilters,
				)

				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(publisher.PublishCreateCallCount()).To(Equal(0))
				Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("success"))

				// At least one filter skip recorded sha_unchanged.
				found := false
				for i := 0; i < metrics.IncFilterSkippedCallCount(); i++ {
					if metrics.IncFilterSkippedArgsForCall(i) == "sha_unchanged" {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "expected sha_unchanged in IncFilterSkipped labels")

				// Cursor file still exists.
				_, err := os.Stat(cursorPath)
				Expect(err).NotTo(HaveOccurred())
			})

			It("Poll(ctx, true) publishes docker-utils despite cursor match", func() {
				w := pkg.NewWatcher(
					ghClient, publisher, metrics, cursorPath, "bborbe", staticFilters,
				)

				Expect(w.Poll(ctx, true)).To(Succeed())

				Expect(publisher.PublishCreateCallCount()).To(Equal(1))
				_, release := publisher.PublishCreateArgsForCall(0)
				Expect(release.Repo.Name).To(Equal("docker-utils"))

				// No filter skip recorded sha_unchanged.
				for i := 0; i < metrics.IncFilterSkippedCallCount(); i++ {
					Expect(metrics.IncFilterSkippedArgsForCall(i)).
						NotTo(Equal("sha_unchanged"),
							"sha_unchanged should not appear when forced")
				}

				Expect(metrics.IncPollCycleArgsForCall(0)).To(Equal("success"))

				// Cursor save invariant: file still exists and the entry equals
				// the head SHA (cursor save still runs on forced cycles).
				loaded, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(
					loaded.Repos["github.com/bborbe/docker-utils"].LastSeenMasterSHA,
				).To(Equal("d630ef3526cfc57fbdccd9ba53c5c3a02945e407"))
			})
		},
	)

	Describe("Poll filter-chain composition (spec 071)", func() {
		var staticFilters filter.TaskCreationFilter

		BeforeEach(func() {
			staticFilters = filter.TaskCreationFilters{
				filter.NewRepoAllowlistFilter(nil),
				filter.NewEmptyUnreleasedFilter(),
				filter.NewAutoReleaseFilter(),
			}

			ghClient.ListReposReturns([]pkg.Repo{
				{Owner: "bborbe", Name: "docker-utils", DefaultBranch: "master"},
			}, nil)
			ghClient.GetMasterSHAReturns("d630ef3526cfc57fbdccd9ba53c5c3a02945e407", nil)
			ghClient.GetChangelogContentReturns(
				[]byte("## Unreleased\n\n- entry one\n\n## v1.7.7\n"), nil,
			)
			ghClient.GetMaintainerConfigReturns(
				maintainerconfig.MaintainerConfig{
					Release: maintainerconfig.ReleaseConfig{AutoRelease: true},
				}, nil,
			)
			publisher.PublishCreateReturns(true)

			Expect(pkg.SaveCursor(ctx, cursorPath, &pkg.Cursor{
				Repos: map[string]*pkg.RepoState{
					"github.com/bborbe/docker-utils": {
						LastSeenMasterSHA: "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
					},
				},
			})).To(Succeed())
		})

		It("Poll(ctx, false) composes SHAUnchangedFilter into the cycle chain", func() {
			w := pkg.NewWatcher(
				ghClient, publisher, metrics, cursorPath, "bborbe", staticFilters,
			)
			Expect(w.Poll(ctx, false)).To(Succeed())

			seen := false
			for i := 0; i < metrics.IncFilterSkippedCallCount(); i++ {
				if metrics.IncFilterSkippedArgsForCall(i) == "sha_unchanged" {
					seen = true
					break
				}
			}
			Expect(seen).To(BeTrue(),
				"sha_unchanged label proves SHAUnchangedFilter ran")
		})

		It("Poll(ctx, true) excludes SHAUnchangedFilter from the cycle chain", func() {
			w := pkg.NewWatcher(
				ghClient, publisher, metrics, cursorPath, "bborbe", staticFilters,
			)
			Expect(w.Poll(ctx, true)).To(Succeed())

			for i := 0; i < metrics.IncFilterSkippedCallCount(); i++ {
				Expect(metrics.IncFilterSkippedArgsForCall(i)).
					NotTo(Equal("sha_unchanged"))
			}
		})
	})
})
