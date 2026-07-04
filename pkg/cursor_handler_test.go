// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/gorilla/mux"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResetCursorHandler", func() {
	var (
		ctx        context.Context
		tmpDir     string
		cursorPath string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()
		cursorPath = filepath.Join(tmpDir, "cursor.json")
	})

	routeAndServe := func(target string) *httptest.ResponseRecorder {
		router := mux.NewRouter()
		router.Path("/resetcursor/{repo:.+}").Handler(pkg.NewResetCursorHandler(cursorPath))
		req := httptest.NewRequest(http.MethodPost, target, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	writeCursor := func(repos map[string]*pkg.RepoState) {
		Expect(pkg.SaveCursor(ctx, cursorPath, &pkg.Cursor{Repos: repos})).To(Succeed())
	}

	It("deletes an existing entry and returns 200", func() {
		writeCursor(map[string]*pkg.RepoState{
			"github.com/bborbe/foo": {LastSeenMasterSHA: "abc123"},
			"github.com/bborbe/bar": {LastSeenMasterSHA: "def456"},
		})

		rec := routeAndServe("/resetcursor/github.com/bborbe/foo")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("github.com/bborbe/foo"))

		reloaded, err := pkg.LoadCursor(ctx, cursorPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Repos).NotTo(HaveKey("github.com/bborbe/foo"))
		Expect(reloaded.Repos).To(HaveKey("github.com/bborbe/bar"))
	})

	It("returns 404 when the repo is not in the cursor", func() {
		writeCursor(map[string]*pkg.RepoState{
			"github.com/bborbe/other": {LastSeenMasterSHA: "abc123"},
		})

		rec := routeAndServe("/resetcursor/github.com/bborbe/missing")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Body.String()).To(ContainSubstring("repo not found in cursor"))
	})

	It("returns 500 when the cursor file is corrupt", func() {
		Expect(os.WriteFile(cursorPath, []byte("not-json"), 0600)).To(Succeed())

		rec := routeAndServe("/resetcursor/github.com/bborbe/foo")
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})
})

var _ = Describe("SetCursorHandler", func() {
	var (
		ctx        context.Context
		tmpDir     string
		cursorPath string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()
		cursorPath = filepath.Join(tmpDir, "cursor.json")
	})

	routeAndServe := func(target string) *httptest.ResponseRecorder {
		router := mux.NewRouter()
		router.Path("/setcursor/{repo:.+}").Handler(pkg.NewSetCursorHandler(cursorPath))
		req := httptest.NewRequest(http.MethodPost, target, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	writeCursor := func(repos map[string]*pkg.RepoState) {
		Expect(pkg.SaveCursor(ctx, cursorPath, &pkg.Cursor{Repos: repos})).To(Succeed())
	}

	It("sets the last-seen SHA for an existing repo and reports the previous value", func() {
		writeCursor(map[string]*pkg.RepoState{
			"github.com/bborbe/foo": {LastSeenMasterSHA: "old111"},
		})

		rec := routeAndServe("/setcursor/github.com/bborbe/foo?sha=new222")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("sha=new222"))
		Expect(rec.Body.String()).To(ContainSubstring("previous=old111"))

		reloaded, err := pkg.LoadCursor(ctx, cursorPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Repos["github.com/bborbe/foo"].LastSeenMasterSHA).To(Equal("new222"))
	})

	It("creates a cursor entry when the repo was previously absent", func() {
		writeCursor(map[string]*pkg.RepoState{})

		rec := routeAndServe("/setcursor/github.com/bborbe/bar?sha=deadbeef")
		Expect(rec.Code).To(Equal(http.StatusOK))

		reloaded, err := pkg.LoadCursor(ctx, cursorPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Repos["github.com/bborbe/bar"].LastSeenMasterSHA).To(Equal("deadbeef"))
	})

	It("returns 400 when the sha query parameter is absent", func() {
		rec := routeAndServe("/setcursor/github.com/bborbe/foo")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 400 when the sha query parameter is empty", func() {
		rec := routeAndServe("/setcursor/github.com/bborbe/foo?sha=")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 500 when the cursor file is corrupt", func() {
		Expect(os.WriteFile(cursorPath, []byte("not-json"), 0600)).To(Succeed())

		rec := routeAndServe("/setcursor/github.com/bborbe/foo?sha=new222")
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})
})
