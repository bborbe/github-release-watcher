// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/github-release-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg.Cursor", func() {
	var (
		ctx    context.Context
		tmpDir string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "cursor-release-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir) // #nosec G104 -- best-effort temp dir cleanup
	})

	Describe("LoadCursor", func() {
		Context("file is missing", func() {
			It("LoadCursor returns cold-start empty cursor when file is missing", func() {
				path := filepath.Join(tmpDir, "nonexistent.json")
				cursor, err := pkg.LoadCursor(ctx, path)
				Expect(err).NotTo(HaveOccurred())
				Expect(cursor).NotTo(BeNil())
				Expect(cursor.Repos).NotTo(BeNil())
				Expect(cursor.Repos).To(BeEmpty())
			})
		})

		Context("file has corrupt JSON", func() {
			It("LoadCursor returns error on corrupt JSON", func() {
				path := filepath.Join(tmpDir, "corrupt.json")
				Expect(os.WriteFile(path, []byte("not json"), 0600)).To(Succeed())
				cursor, err := pkg.LoadCursor(ctx, path)
				Expect(err).To(HaveOccurred())
				Expect(cursor).To(BeNil())
			})
		})

		Context("file has repos: null", func() {
			It("LoadCursor handles repos: null by initializing the map", func() {
				path := filepath.Join(tmpDir, "repos-null.json")
				Expect(os.WriteFile(path, []byte(`{"repos":null}`), 0600)).To(Succeed())
				cursor, err := pkg.LoadCursor(ctx, path)
				Expect(err).NotTo(HaveOccurred())
				Expect(cursor).NotTo(BeNil())
				Expect(cursor.Repos).NotTo(BeNil())
				Expect(cursor.Repos).To(BeEmpty())
			})
		})
	})

	Describe("SaveCursor", func() {
		Context("target directory does not exist", func() {
			It("SaveCursor returns error when target directory does not exist", func() {
				path := filepath.Join(tmpDir, "missing-dir", "cursor.json")
				cursor := &pkg.Cursor{Repos: make(map[string]*pkg.RepoState)}
				err := pkg.SaveCursor(ctx, path, cursor)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("valid cursor", func() {
			It("SaveCursor does atomic write — no .tmp file remains after success", func() {
				path := filepath.Join(tmpDir, "atomic.json")
				cursor := &pkg.Cursor{
					Repos: map[string]*pkg.RepoState{
						"github.com/bborbe/test": {LastSeenMasterSHA: "abc123"},
					},
				}
				Expect(pkg.SaveCursor(ctx, path, cursor)).To(Succeed())

				// .tmp file must not exist after successful rename
				_, err := os.Stat(path + ".tmp")
				Expect(os.IsNotExist(err)).To(BeTrue())
			})
		})
	})

	Describe("SaveCursor + LoadCursor round-trip", func() {
		It("SaveCursor + LoadCursor round-trip preserves Repos map", func() {
			path := filepath.Join(tmpDir, "roundtrip.json")
			original := &pkg.Cursor{
				Repos: map[string]*pkg.RepoState{
					"github.com/bborbe/docker-utils": {
						LastSeenMasterSHA: "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
					},
					"github.com/bborbe/disk-status": {
						LastSeenMasterSHA: "102b3b1abcdef0000000000000000000000000a0",
					},
				},
			}
			Expect(pkg.SaveCursor(ctx, path, original)).To(Succeed())

			loaded, err := pkg.LoadCursor(ctx, path)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Repos).To(HaveLen(2))
			Expect(
				loaded.Repos["github.com/bborbe/docker-utils"].LastSeenMasterSHA,
			).To(Equal("d630ef3526cfc57fbdccd9ba53c5c3a02945e407"))
			Expect(
				loaded.Repos["github.com/bborbe/disk-status"].LastSeenMasterSHA,
			).To(Equal("102b3b1abcdef0000000000000000000000000a0"))
		})
	})
})
