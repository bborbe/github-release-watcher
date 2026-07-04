// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/bborbe/github-release-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/maintainer/maintainerconfig"
)

var _ = Describe("pkg.GitHubClient", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		cancel()
	})

	Describe("ListRepos", func() {
		Context("user owner with pagination", func() {
			It("paginates and filters archived/forks", func() {
				var serverURL string
				var requestCount int
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requestCount++
						switch r.URL.Path {
						case "/users/bborbe":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"login":"bborbe","type":"User"}`)
						case "/users/bborbe/repos":
							page := r.URL.Query().Get("page")
							w.Header().Set("Content-Type", "application/json")
							if page == "1" {
								w.Header().
									Set("Link", "<"+serverURL+"/users/bborbe/repos?page=2>; rel=\"next\"")
								fmt.Fprintf(
									w,
									`[{"name":"docker-utils","default_branch":"master","archived":false,"fork":false,"owner":{"login":"bborbe"}},{"name":"old-stuff","default_branch":"master","archived":true,"fork":false,"owner":{"login":"bborbe"}},{"name":"a-fork","default_branch":"main","archived":false,"fork":true,"owner":{"login":"bborbe"}}]`,
								)
							} else {
								fmt.Fprintf(w, `[{"name":"disk-status","default_branch":"main","archived":false,"fork":false,"owner":{"login":"bborbe"}}]`)
							}
						default:
							w.WriteHeader(http.StatusNotFound)
							fmt.Fprintf(w, "unexpected route: %s", r.URL.Path)
						}
					}),
				)
				defer server.Close()
				serverURL = server.URL

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				repos, err := client.ListRepos(ctx, "bborbe")
				Expect(err).NotTo(HaveOccurred())
				Expect(repos).To(HaveLen(2))
				Expect(repos[0].Name).To(Equal("docker-utils"))
				Expect(repos[0].DefaultBranch).To(Equal("master"))
				Expect(repos[1].Name).To(Equal("disk-status"))
				Expect(repos[1].DefaultBranch).To(Equal("main"))
				// 3 requests: user + page1 + page2
				Expect(requestCount).To(Equal(3))
			})
		})

		Context("rate limited", func() {
			It("returns ErrRateLimited on 403 + X-RateLimit-Remaining: 0", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.Header().Set("X-RateLimit-Reset", "9999999999")
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprintf(
							w,
							`{"message":"API rate limit exceeded","documentation_url":"https://docs.github.com/rest/overview/rate-limiting-for-the-rest-api"}`,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				_, err = client.ListRepos(ctx, "bborbe")
				Expect(err).To(MatchError(pkg.ErrRateLimited))
			})
		})

		Context("org owner", func() {
			It("calls ListByOrg", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/users/testorg":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"login":"testorg","type":"Organization"}`)
						case "/orgs/testorg/repos":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(
								w,
								`[{"name":"org-repo","default_branch":"main","archived":false,"fork":false,"owner":{"login":"testorg"}}]`,
							)
						default:
							w.WriteHeader(http.StatusNotFound)
							fmt.Fprintf(w, "unexpected route: %s", r.URL.Path)
						}
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				repos, err := client.ListRepos(ctx, "testorg")
				Expect(err).NotTo(HaveOccurred())
				Expect(repos).To(HaveLen(1))
				Expect(repos[0].Name).To(Equal("org-repo"))
			})
		})
	})

	Describe("GetMasterSHA", func() {
		Context("happy path", func() {
			It("returns the branch HEAD commit SHA", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.URL.Path).To(Equal("/repos/bborbe/docker-utils/branches/master"))
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"master","commit":{"sha":"d630ef3526cfc57fbdccd9ba53c5c3a02945e407"}}`,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				sha, err := client.GetMasterSHA(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "docker-utils", DefaultBranch: "master"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(sha).To(Equal("d630ef3526cfc57fbdccd9ba53c5c3a02945e407"))
			})
		})

		Context("empty DefaultBranch", func() {
			It("returns wrapped error and does not make HTTP request", func() {
				var requestCount int
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requestCount++
						w.WriteHeader(http.StatusOK)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				_, err = client.GetMasterSHA(
					ctx,
					pkg.Repo{Owner: "x", Name: "y", DefaultBranch: ""},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("empty DefaultBranch"))
				Expect(requestCount).To(Equal(0))
			})
		})
	})

	Describe("GetChangelogContent", func() {
		Context("file not found (404)", func() {
			It("returns (nil, nil)", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.URL.Path).To(Equal("/repos/bborbe/x/contents/CHANGELOG.md"))
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprintf(w, `{"message":"Not Found"}`)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				content, err := client.GetChangelogContent(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "x", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(BeNil())
			})
		})

		Context("file exists (200)", func() {
			It("returns decoded bytes", func() {
				fixture := "## Unreleased\n\n- new\n"
				encoded := base64.StdEncoding.EncodeToString([]byte(fixture))

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.URL.Path).To(Equal("/repos/bborbe/repo/contents/CHANGELOG.md"))
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"CHANGELOG.md","path":"CHANGELOG.md","size":%d,"encoding":"base64","content":"%s"}`,
							len(fixture),
							encoded,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				content, err := client.GetChangelogContent(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(content).NotTo(BeNil())
				Expect(string(content)).To(Equal(fixture))
			})
		})

		Context("file larger than 1 MiB", func() {
			It("rejects files larger than 1 MiB before decoding", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						// size > 1 MiB, content empty to avoid unnecessary bytes
						fmt.Fprintf(
							w,
							`{"name":"CHANGELOG.md","path":"CHANGELOG.md","size":2000000,"encoding":"base64","content":""}`,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				content, err := client.GetChangelogContent(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("too large"))
				Expect(content).To(BeNil())
			})
		})

		Context("rate limited", func() {
			It("returns ErrRateLimited on rate limit error", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.Header().Set("X-RateLimit-Reset", "9999999999")
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprintf(w, `{"message":"API rate limit exceeded"}`)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				content, err := client.GetChangelogContent(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).To(MatchError(pkg.ErrRateLimited))
				Expect(content).To(BeNil())
			})
		})
	})

	Describe("GetMaintainerConfig", func() {
		Context("file not found (404)", func() {
			It("returns zero-value config and nil error on HTTP 404", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.URL.Path).To(Equal("/repos/bborbe/x/contents/.maintainer.yaml"))
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprintf(w, `{"message":"Not Found"}`)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "x", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg).To(Equal(maintainerconfig.MaintainerConfig{}))
			})
		})

		Context("file is empty", func() {
			It("returns zero-value config and nil error when file is empty", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":0,"encoding":"base64","content":""}`,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg).To(Equal(maintainerconfig.MaintainerConfig{}))
			})
		})

		Context("release key absent", func() {
			It("returns zero-value Release.AutoRelease when release key is absent", func() {
				yamlContent := "pr-reviewer:\n  enabled: true\n"
				encoded := base64.StdEncoding.EncodeToString([]byte(yamlContent))

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":%d,"encoding":"base64","content":"%s"}`,
							len(yamlContent),
							encoded,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Release.AutoRelease).To(BeFalse())
			})
		})

		Context("release.autoRelease: false", func() {
			It("returns AutoRelease=false when release.autoRelease is explicitly false", func() {
				yamlContent := "release:\n  autoRelease: false\n"
				encoded := base64.StdEncoding.EncodeToString([]byte(yamlContent))

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":%d,"encoding":"base64","content":"%s"}`,
							len(yamlContent),
							encoded,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Release.AutoRelease).To(BeFalse())
			})
		})

		Context("release.autoRelease: true", func() {
			It("returns AutoRelease=true when release.autoRelease is true", func() {
				yamlContent := "release:\n  autoRelease: true\n"
				encoded := base64.StdEncoding.EncodeToString([]byte(yamlContent))

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":%d,"encoding":"base64","content":"%s"}`,
							len(yamlContent),
							encoded,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Release.AutoRelease).To(BeTrue())
			})
		})

		Context("malformed YAML", func() {
			It("surfaces wrapped error on malformed YAML", func() {
				invalidYAML := "{invalid"
				encoded := base64.StdEncoding.EncodeToString([]byte(invalidYAML))

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":%d,"encoding":"base64","content":"%s"}`,
							len(invalidYAML),
							encoded,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("parse .maintainer.yaml"))
				Expect(cfg).To(Equal(maintainerconfig.MaintainerConfig{}))
			})
		})

		Context("unknown top-level keys", func() {
			It("ignores unknown top-level keys", func() {
				yamlContent := "pr-reviewer:\n  enabled: true\nbuild-fix:\n  channel: stable\nrelease:\n  autoRelease: true\n"
				encoded := base64.StdEncoding.EncodeToString([]byte(yamlContent))

				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":%d,"encoding":"base64","content":"%s"}`,
							len(yamlContent),
							encoded,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Release.AutoRelease).To(BeTrue())
			})
		})

		Context("rate limited", func() {
			It("returns ErrRateLimited on rate-limit response", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.Header().Set("X-RateLimit-Reset", "9999999999")
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprintf(w, `{"message":"API rate limit exceeded"}`)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).To(MatchError(pkg.ErrRateLimited))
				Expect(cfg).To(Equal(maintainerconfig.MaintainerConfig{}))
			})
		})

		Context("HTTP 500", func() {
			It("returns wrapped error on HTTP 500 response", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusInternalServerError)
						fmt.Fprintf(w, `{"message":"server error"}`)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("get .maintainer.yaml"))
				Expect(cfg).To(Equal(maintainerconfig.MaintainerConfig{}))
			})
		})

		Context("oversize file", func() {
			It("returns wrapped error on oversize response", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`{"name":"maintainer.yaml","path":".maintainer.yaml","size":2000000,"encoding":"base64","content":""}`,
						)
					}),
				)
				defer server.Close()

				client := pkg.NewGitHubClient(server.Client())
				err := pkg.SetBaseURL(client, server.URL+"/")
				Expect(err).NotTo(HaveOccurred())

				cfg, err := client.GetMaintainerConfig(
					ctx,
					pkg.Repo{Owner: "bborbe", Name: "repo", DefaultBranch: "main"},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("too large"))
				Expect(cfg).To(Equal(maintainerconfig.MaintainerConfig{}))
			})
		})
	})
})
