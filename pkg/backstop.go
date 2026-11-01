// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

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
	// Poll dispatches a build for repo's newest release tag when that tag has
	// not been dispatched before. lastBuiltTag is the cursor's record of the
	// last tag this backstop handled. It returns the tag it dispatched, or ""
	// when there was nothing new to do — the caller persists a non-empty
	// return so the next cycle stays silent.
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
	tag, err := b.ghClient.GetLatestReleaseTag(ctx, repo)
	if err != nil {
		return "", errors.Wrapf(ctx, err, "resolve latest release tag repo=%s", repoKey)
	}
	if tag == "" {
		return "", nil
	}
	if tag == lastBuiltTag {
		// Already handled. Silent on purpose: this is the steady state for
		// every repo on every cycle, and logging it would bury the one cycle
		// where the backstop actually fired.
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
