// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	stderrors "errors"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg/filter"
	"github.com/golang/glog"
)

//counterfeiter:generate -o ../mocks/watcher.go --fake-name Watcher . Watcher

// Watcher polls GitHub for repos with non-empty ## Unreleased and publishes
// CreateTaskCommands to Kafka for github-releaser-agent to consume.
type Watcher interface {
	// Poll runs one scan cycle. Safe to call repeatedly on an interval.
	//
	// skipSHAUnchanged controls whether the SHA-unchanged dedup gate is
	// composed into this cycle's filter chain. The poll-interval loop
	// always passes false (today's behaviour: repos whose head SHA equals
	// the recorded cursor are silently skipped). The /trigger HTTP path
	// passes true when the operator requested ?force=true (spec 071) so
	// every repo is reconsidered even when the cursor already matches —
	// every other filter (allowlist, empty-unreleased, auto-release)
	// still runs.
	Poll(ctx context.Context, skipSHAUnchanged bool) error
}

// NewWatcher wires the watcher's collaborators.
//
// Owner = single GitHub org per watcher instance (multi-org = multiple deployments).
// taskCreationFilter is the cycle-invariant chain (scope + empty_unreleased +
// auto_release); SHAUnchangedFilter is composed in per cycle since it needs a
// fresh CursorReader.
func NewWatcher(
	ghClient GitHubClient,
	publisher TaskPublisher,
	metrics Metrics,
	cursorPath string,
	owner string,
	taskCreationFilter filter.TaskCreationFilter,
) Watcher {
	return &watcher{
		ghClient:           ghClient,
		publisher:          publisher,
		metrics:            metrics,
		cursorPath:         cursorPath,
		owner:              owner,
		taskCreationFilter: taskCreationFilter,
	}
}

type watcher struct {
	ghClient           GitHubClient
	publisher          TaskPublisher
	metrics            Metrics
	cursorPath         string
	owner              string
	taskCreationFilter filter.TaskCreationFilter
}

// Poll implements Watcher. When skipSHAUnchanged is true the cycle filter
// chain omits SHAUnchangedFilter; every other filter is unaffected (spec 071).
// One cycle:
//  1. Load cursor (cold-start safe)
//  2. ListRepos(owner) — abort cycle on rate_limited / github_error (no cursor save)
//  3. For each repo (sequential):
//     a. GetMasterSHA — abort cycle on rate_limited; prune on transient error
//     b. GetChangelogContent → ParseChangelog → ChangelogSummary
//     c. GetMaintainerConfig
//     d. Build Release struct
//     e. taskCreationFilter.Skip(release) — bump filter metric on returned label
//     f. publisher.PublishCreate(release) — update cursor on true return
//  4. SaveCursor (skip on abort)
//  5. IncPollCycle("success")
func (w *watcher) Poll(ctx context.Context, skipSHAUnchanged bool) error {
	cursorState, err := LoadCursor(ctx, w.cursorPath)
	if err != nil {
		return errors.Wrapf(ctx, err, "load cursor path=%s", w.cursorPath)
	}

	repos, err := w.ghClient.ListRepos(ctx, w.owner)
	if err != nil {
		if stderrors.Is(err, ErrRateLimited) {
			w.metrics.IncPollCycle("rate_limited")
			glog.Warningf("poll cycle aborted: rate limited during ListRepos owner=%s", w.owner)
			return nil
		}
		w.metrics.IncPollCycle("github_error")
		glog.Warningf("poll cycle aborted: ListRepos owner=%s err=%v", w.owner, err)
		return nil
	}
	w.metrics.IncReposScanned(len(repos))

	// Compose cycle-specific SHAUnchangedFilter into the chain unless the
	// caller forced the cycle (spec 071). All other filters in
	// w.taskCreationFilter are unaffected on both code paths.
	cycleFilter := filter.TaskCreationFilters{w.taskCreationFilter}
	if !skipSHAUnchanged {
		cycleFilter = append(
			cycleFilter,
			filter.NewSHAUnchangedFilter(NewCursorReader(cursorState)),
		)
	}

	abortReason := w.processRepos(ctx, cursorState, repos, cycleFilter)
	if abortReason != "" {
		w.metrics.IncPollCycle(abortReason)
		// Do NOT save cursor on abort — next cycle resumes from same state.
		return nil
	}

	if err := SaveCursor(ctx, w.cursorPath, cursorState); err != nil {
		// Per spec failure-modes: cursor save error post-publish is best-effort.
		// Tasks were already published; controller dedup absorbs re-emit next cycle.
		glog.Warningf("save cursor failed path=%s err=%v", w.cursorPath, err)
	}
	w.metrics.IncPollCycle("success")
	return nil
}

// processRepos iterates repos sequentially (spec § Non-goals: per-repo parallelism is agent territory).
// Returns "" on success, "github_error" or "rate_limited" if the cycle should abort and skip cursor save.
//
// Per-repo error policy (spec failure-modes):
//   - Cycle-aborting (return early): rate_limited at any layer; 5xx during ListRepos (handled in Poll above).
//   - Per-repo prune (continue loop): GetMasterSHA / GetChangelogContent / GetMaintainerConfig transient
//     non-rate-limit error — log via glog.V(2).Infof so operator can grep "repo dropped from cycle".
func (w *watcher) processRepos(
	ctx context.Context,
	cursorState *Cursor,
	repos []Repo,
	cycleFilter filter.TaskCreationFilter,
) string {
	for _, repo := range repos {
		select {
		case <-ctx.Done():
			glog.V(2).Infof("poll cancelled during processRepos at repo=%s", repo.Key())
			return ""
		default:
		}

		release, abortReason, dropped := w.gatherRelease(ctx, repo)
		if abortReason != "" {
			return abortReason
		}
		if dropped {
			continue
		}

		filterInput := filter.Release{
			RepoKey:           repo.Key(),
			HeadSHA:           release.HeadSHA,
			UnreleasedBullets: release.UnreleasedBullets,
			AutoRelease:       release.AutoRelease,
		}
		if reason := cycleFilter.Skip(filterInput); reason != "" {
			w.metrics.IncFilterSkipped(reason)
			continue
		}

		if w.publisher.PublishCreate(ctx, release) {
			if cursorState.Repos == nil {
				cursorState.Repos = make(map[string]*RepoState)
			}
			cursorState.Repos[repo.Key()] = &RepoState{LastSeenMasterSHA: release.HeadSHA}
		}
	}
	return ""
}

// gatherRelease fetches HeadSHA, ChangelogContent, MaintainerConfig for one repo.
// Returns (release, "", false) on success.
// Returns ({}, "rate_limited"|"github_error", false) when the whole cycle should abort.
// Returns ({}, "", true) when this repo should be silently pruned from the cycle.
func (w *watcher) gatherRelease(ctx context.Context, repo Repo) (Release, string, bool) {
	headSHA, err := w.ghClient.GetMasterSHA(ctx, repo)
	if err != nil {
		if stderrors.Is(err, ErrRateLimited) {
			return Release{}, "rate_limited", false
		}
		glog.V(2).
			Infof("repo dropped from cycle: owner=%s repo=%s err=%v", repo.Owner, repo.Name, err)
		return Release{}, "", true
	}
	content, err := w.ghClient.GetChangelogContent(ctx, repo)
	if err != nil {
		if stderrors.Is(err, ErrRateLimited) {
			return Release{}, "rate_limited", false
		}
		glog.V(2).
			Infof("repo dropped from cycle: owner=%s repo=%s err=%v", repo.Owner, repo.Name, err)
		return Release{}, "", true
	}
	maintainerCfg, err := w.ghClient.GetMaintainerConfig(ctx, repo)
	if err != nil {
		if stderrors.Is(err, ErrRateLimited) {
			return Release{}, "rate_limited", false
		}
		glog.V(2).
			Infof("repo dropped from cycle: owner=%s repo=%s err=%v", repo.Owner, repo.Name, err)
		return Release{}, "", true
	}
	summary := ParseChangelog(content)
	currentVersion := summary.LatestVersion
	if currentVersion == "" {
		currentVersion = "v0.0.0"
	}
	return Release{
		Repo:              repo,
		HeadSHA:           headSHA,
		CurrentVersion:    currentVersion,
		UnreleasedBullets: summary.UnreleasedBullets,
		AutoRelease:       maintainerCfg.Release.AutoRelease,
	}, "", false
}

// CursorReader adapter — wraps *Cursor to satisfy filter.CursorReader without
// introducing the import cycle (filter cannot import pkg).
type cursorReader struct{ c *Cursor }

// NewCursorReader exposes a filter-compatible read view over a Cursor.
func NewCursorReader(c *Cursor) filter.CursorReader {
	return &cursorReader{c: c}
}

func (r *cursorReader) LastSeenSHA(repoKey string) string {
	if r.c == nil || r.c.Repos == nil {
		return ""
	}
	if state, ok := r.c.Repos[repoKey]; ok && state != nil {
		return state.LastSeenMasterSHA
	}
	return ""
}
