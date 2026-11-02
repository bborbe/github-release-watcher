// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"slices"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg/build"
	"github.com/golang/glog"
)

//counterfeiter:generate -o ../mocks/tag_backstop.go --fake-name TagBackstop . TagBackstop

// TagBackstop recovers a build whose tag-push webhook never arrived.
//
// It runs inside the existing poll cycle rather than as a second ticker beside
// POLL_INTERVAL: the cycle already carries the quota gate (QUOTA_MIN_REMAINING)
// and the per-repo loop, and a parallel loop would double the scan's GitHub API
// cost — which is precisely what the design record prices option C as
// ("Low-med … costs GitHub API quota").
type TagBackstop interface {
	// Poll dispatches a build for repo's newest tag when that tag has not been
	// dispatched before. A tag need not carry a GitHub Release — the newest is
	// chosen by committer epoch, not by the Releases API. lastBuiltTag is the
	// cursor's record of the last tag this backstop handled. It returns the tag
	// it dispatched, or "" when there was nothing new to do — the caller
	// persists a non-empty return so the next cycle stays silent.
	Poll(ctx context.Context, repoKey, lastBuiltTag string) (string, error)
}

// NewTagBackstop wires the backstop. A nil dispatcher yields a backstop that
// does nothing, which is what run-once (no cluster, no Job API) needs — the
// alternative, a nil check at every call site, hides a real wiring mistake
// behind the same silence.
func NewTagBackstop(ghClient GitHubClient, dispatcher build.Dispatcher) TagBackstop {
	if dispatcher == nil {
		return NoopTagBackstop{}
	}
	return &tagBackstop{ghClient: ghClient, dispatcher: dispatcher}
}

// NoopTagBackstop dispatches nothing. Used where no cluster is reachable.
type NoopTagBackstop struct{}

// Poll implements TagBackstop and always reports nothing to do.
func (NoopTagBackstop) Poll(context.Context, string, string) (string, error) {
	return "", nil
}

type tagBackstop struct {
	ghClient   GitHubClient
	dispatcher build.Dispatcher
}

func (b *tagBackstop) Poll(ctx context.Context, repoKey, lastBuiltTag string) (string, error) {
	repo, ok := repoFromKey(repoKey)
	if !ok {
		return "", errors.Errorf(ctx, "malformed repo key %q", repoKey)
	}
	tag, err := b.newestUnbuiltTag(ctx, repo, lastBuiltTag)
	if err != nil {
		return "", errors.Wrapf(ctx, err, "resolve newest tag repo=%s", repoKey)
	}
	if tag == "" {
		return "", nil
	}
	if err := b.dispatcher.Dispatch(
		ctx,
		build.Request{Repo: repoKey, Tag: tag},
		build.TriggerPoll,
	); err != nil {
		return "", errors.Wrapf(ctx, err, "backstop dispatch repo=%s tag=%s", repoKey, tag)
	}
	glog.V(2).Infof(
		"github-release-watcher backstop handled repo=%s ref=%s", repoKey, build.TagRefPrefix+tag,
	)
	return tag, nil
}

// newestUnbuiltTag returns the tag to dispatch, or "" when nothing is at least
// as new as lastBuiltTag.
//
// The ordering rule is the whole decision here. GitHub returns ListTags
// newest-first in practice but guarantees nothing, so the first entry is a
// candidate, not an answer: its committer epoch must match or beat
// lastBuiltTag's, and when it does not, the rest of the page is scanned. Semver sorting is
// deliberately NOT used — it drops non-semver tags (this fleet carries
// "probe-version") and ranks a prerelease below its own release, so a re-cut
// vX.Y.Z-rc1 would sort under vX.Y.Z and never dispatch.
func (b *tagBackstop) newestUnbuiltTag(
	ctx context.Context,
	repo Repo,
	lastBuiltTag string,
) (string, error) {
	tags, err := b.ghClient.ListTags(ctx, repo)
	if err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return "", nil
	}
	candidate := tags[0]
	if candidate == lastBuiltTag {
		// Already handled. Silent on purpose: this is the steady state for
		// every repo on every cycle, and logging it would bury the one cycle
		// where the backstop actually fired.
		//
		// This shortcut ASSUMES the first entry is the newest, which is the one
		// assumption the scan below exists to defend against — and it is the
		// price of the one-call steady state. Verifying it would mean an epoch
		// call per entry on every cycle forever. The assumption is sound in
		// practice (GitHub returns tags newest-first) and the failure it would
		// hide — a newer tag sitting deeper while the cursor happens to be
		// first — needs the API to reorder, which no observed response has done.
		return "", nil
	}
	if lastBuiltTag == "" || !slices.Contains(tags, lastBuiltTag) {
		// No baseline yet, or the cursor names a tag that no longer exists
		// (deleted or rewound). Dispatching the newest self-heals the cursor,
		// and the dispatcher is idempotent when the image already exists.
		return candidate, nil
	}
	lastEpoch, err := b.ghClient.GetCommitEpoch(ctx, repo, lastBuiltTag)
	if err != nil {
		return "", errors.Wrapf(ctx, err, "epoch last built tag=%s", lastBuiltTag)
	}
	// Pick the MAXIMUM epoch on the page, not merely the first entry that beats
	// the cursor. Trusting the first entry here would reintroduce the very
	// assumption this function exists to defend against: on a partially
	// reordered page the first at-least-as-new tag would be dispatched, the
	// cursor would advance to it, and the genuinely newest tag deeper in the
	// page would then be masked by the index-0 shortcut on every later cycle.
	//
	// >= rather than >: a NEW tag name on an already-built commit still needs a
	// build. The image is keyed by tag name, so a re-tag of the same commit
	// (v1.2.4 on v1.2.3's commit) has no image yet, and a strict > would drop
	// it silently — the same class of missed tag this backstop exists to
	// recover. Dispatching it also advances the cursor, so the tie does not
	// become a repeated-scan steady state.
	//
	// Cost: this loop runs only when a tag other than the cursor is on the page,
	// i.e. on the dispatch path, never in the steady state. It is bounded at one
	// page (100 entries) and each iteration is one GetCommitEpoch round trip, so
	// the context check below is what stops a cancelled cycle from spending the
	// shared token on the rest of the page.
	best := ""
	var bestEpoch int64
	for _, tag := range tags {
		select {
		case <-ctx.Done():
			return "", errors.Wrapf(ctx, ctx.Err(), "scan tags cancelled")
		default:
		}
		if tag == lastBuiltTag {
			continue
		}
		epoch, err := b.ghClient.GetCommitEpoch(ctx, repo, tag)
		if err != nil {
			return "", errors.Wrapf(ctx, err, "epoch candidate tag=%s", tag)
		}
		if epoch >= lastEpoch && (best == "" || epoch > bestEpoch) {
			best, bestEpoch = tag, epoch
		}
	}
	if best == "" {
		// Nothing on the page is at-or-newer than the cursor. Reached when a tag
		// exists whose commit predates the cursor (a probe tag cut on an old
		// commit), and it re-runs every cycle until that tag leaves the page —
		// logged so the cost is visible rather than silent.
		glog.V(2).Infof(
			"github-release-watcher backstop scanned %d tags repo=%s and found none newer than %s",
			len(tags), repo.Key(), lastBuiltTag,
		)
	}
	return best, nil
}
