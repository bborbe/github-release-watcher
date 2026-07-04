// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"net/http"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
)

// NewResetCursorHandler returns an HTTP handler that deletes the cursor entry
// for the {repo} URL variable (e.g. "github.com/bborbe/maintainer"), so the
// next poll treats the repo as first-seen and re-emits a release task.
//
// Wrap with libhttp.NewDangerousHandlerWrapper at the call site to require a
// passphrase — the bare handler does not enforce auth.
//
// Mirrors watcher/github-build/pkg/reset_handler.go: absent repo → 404 (the
// operator targeted a repo the watcher has not seen, almost always a typo).
//
// Race: a concurrent Poll may overwrite the reset; operator should retry if the
// next poll log doesn't show a re-emit for the target repo.
func NewResetCursorHandler(cursorPath string) http.Handler {
	return libhttp.NewErrorHandler(
		libhttp.WithErrorFunc(
			func(ctx context.Context, resp http.ResponseWriter, req *http.Request) error {
				repoKey := mux.Vars(req)["repo"]
				if repoKey == "" {
					return libhttp.WrapWithStatusCode(
						errors.Errorf(ctx, "missing {repo} path variable"),
						http.StatusBadRequest,
					)
				}
				cursor, err := LoadCursor(ctx, cursorPath)
				if err != nil {
					return errors.Wrapf(ctx, err, "load cursor for reset")
				}
				if _, ok := cursor.Repos[repoKey]; !ok {
					return libhttp.WrapWithStatusCode(
						errors.Errorf(ctx, "repo not found in cursor: %s", repoKey),
						http.StatusNotFound,
					)
				}
				delete(cursor.Repos, repoKey)
				if err := SaveCursor(ctx, cursorPath, cursor); err != nil {
					return errors.Wrapf(ctx, err, "save cursor after reset")
				}
				glog.Warningf("cursor reset for repo=%s", repoKey)
				_, _ = libhttp.WriteAndGlog(resp, "cursor reset for "+repoKey)
				return nil
			},
		),
	)
}

// NewSetCursorHandler returns an HTTP handler that sets the cursor's last-seen
// master SHA for the {repo} URL variable to the `sha` query value. Setting it
// to a SHA *older* than current master HEAD makes the next poll see HEAD as
// advanced and re-emit a release task; setting it to the current HEAD
// suppresses re-emit. More precise than reset when pinning a specific baseline.
//
// Wrap with libhttp.NewDangerousHandlerWrapper at the call site to require a
// passphrase — the bare handler does not enforce auth.
//
// Route: /setcursor/{repo:.+}?sha=<value>. Creates the repo entry if absent.
func NewSetCursorHandler(cursorPath string) http.Handler {
	return libhttp.NewErrorHandler(
		libhttp.WithErrorFunc(
			func(ctx context.Context, resp http.ResponseWriter, req *http.Request) error {
				repoKey := mux.Vars(req)["repo"]
				if repoKey == "" {
					return libhttp.WrapWithStatusCode(
						errors.Errorf(ctx, "missing {repo} path variable"),
						http.StatusBadRequest,
					)
				}
				sha := req.URL.Query().Get("sha")
				if sha == "" {
					return libhttp.WrapWithStatusCode(
						errors.Errorf(ctx, "missing sha query parameter"),
						http.StatusBadRequest,
					)
				}
				cursor, err := LoadCursor(ctx, cursorPath)
				if err != nil {
					return errors.Wrapf(ctx, err, "load cursor for set")
				}
				var previous string
				if existing := cursor.Repos[repoKey]; existing != nil {
					previous = existing.LastSeenMasterSHA
				}
				cursor.Repos[repoKey] = &RepoState{LastSeenMasterSHA: sha}
				if err := SaveCursor(ctx, cursorPath, cursor); err != nil {
					return errors.Wrapf(ctx, err, "save cursor after set")
				}
				glog.Warningf("cursor set for repo=%s sha=%s previous=%s", repoKey, sha, previous)
				_, _ = libhttp.WriteAndGlog(
					resp,
					"cursor set for "+repoKey+" sha="+sha+" previous="+previous,
				)
				return nil
			},
		),
	)
}
