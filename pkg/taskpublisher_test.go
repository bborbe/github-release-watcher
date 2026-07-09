// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"errors"
	"strings"

	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeCreateCommandSender struct {
	sendErr      error
	capturedCmds []task.CreateCommand
}

func (f *fakeCreateCommandSender) SendCommand(_ context.Context, cmd task.CreateCommand) error {
	f.capturedCmds = append(f.capturedCmds, cmd)
	return f.sendErr
}

var _ = Describe("pkg.BuildCreateCommand", func() {
	It(
		"BuildCreateCommand produces frontmatter task_type github-release for bborbe/docker-utils d630ef3",
		func() {
			release := pkg.Release{
				Repo: pkg.Repo{
					Owner:         "bborbe",
					Name:          "docker-utils",
					DefaultBranch: "master",
				},
				HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
				CurrentVersion:    "v1.7.7",
				UnreleasedBullets: 5,
				AutoRelease:       false,
			}
			cmd := pkg.BuildCreateCommand(
				release,
				pkg.TaskConfig{Stage: "dev", TargetVault: "agent"},
			)

			Expect(cmd.TargetVault).To(Equal("agent"))
			Expect(cmd.Frontmatter["task_type"]).To(Equal("github-release"))
			Expect(cmd.Frontmatter["assignee"]).To(Equal("github-releaser-agent"))
			Expect(cmd.Frontmatter["phase"]).To(Equal("planning"))
			Expect(cmd.Frontmatter["status"]).To(Equal("in_progress"))
			Expect(cmd.Frontmatter["stage"]).To(Equal("dev"))
			Expect(cmd.Frontmatter["repo"]).To(Equal("bborbe/docker-utils"))
			Expect(cmd.Frontmatter["clone_url"]).To(Equal("git@github.com:bborbe/docker-utils.git"))
			Expect(cmd.Frontmatter["ref"]).To(Equal("d630ef3526cfc57fbdccd9ba53c5c3a02945e407"))
			Expect(cmd.Frontmatter["current_version"]).To(Equal("v1.7.7"))
			Expect(cmd.Frontmatter["task_identifier"]).To(Equal(
				pkg.DeriveTaskID("bborbe", "docker-utils", "d630ef3526cfc57fbdccd9ba53c5c3a02945e407").
					String(),
			))
			Expect(string(cmd.TaskIdentifier)).To(Equal(cmd.Frontmatter["task_identifier"]))
			Expect(cmd.Title).To(Equal("Release bborbe-docker-utils d630ef3"))
		},
	)

	It("BuildCreateCommand leaves TargetVault empty when TaskConfig.TargetVault is unset (default path)", func() {
		release := pkg.Release{
			Repo: pkg.Repo{
				Owner:         "bborbe",
				Name:          "docker-utils",
				DefaultBranch: "master",
			},
			HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			CurrentVersion:    "v1.7.7",
			UnreleasedBullets: 5,
			AutoRelease:       false,
		}
		cmd := pkg.BuildCreateCommand(release, pkg.TaskConfig{Stage: "dev"})

		Expect(cmd.TargetVault).To(BeEmpty())
	})

	It("BuildCreateCommand body is operator-readable header without bullet content", func() {
		release := pkg.Release{
			Repo: pkg.Repo{
				Owner:         "bborbe",
				Name:          "docker-utils",
				DefaultBranch: "master",
			},
			HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			CurrentVersion:    "v1.7.7",
			UnreleasedBullets: 5,
			AutoRelease:       false,
		}
		cmd := pkg.BuildCreateCommand(release, pkg.TaskConfig{Stage: "dev"})

		Expect(cmd.Body).To(HavePrefix("# Release: bborbe/docker-utils\n\n"))
		Expect(cmd.Body).To(ContainSubstring("**Current version:** v1.7.7"))
		Expect(cmd.Body).To(ContainSubstring("**HEAD:** d630ef3"))
		Expect(
			cmd.Body,
		).To(ContainSubstring("https://github.com/bborbe/docker-utils/blob/master/CHANGELOG.md"))
		Expect(strings.Contains(cmd.Body, "\n- ")).To(BeFalse())
	})

	It("BuildCreateCommand stamps the stage from TaskConfig", func() {
		release := pkg.Release{
			Repo: pkg.Repo{
				Owner:         "bborbe",
				Name:          "docker-utils",
				DefaultBranch: "master",
			},
			HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			CurrentVersion:    "v1.7.7",
			UnreleasedBullets: 5,
			AutoRelease:       false,
		}
		cmd := pkg.BuildCreateCommand(release, pkg.TaskConfig{Stage: "prod"})

		Expect(cmd.Frontmatter["stage"]).To(Equal("prod"))
	})

	It("BuildCreateCommand same inputs produce identical commands", func() {
		release := pkg.Release{
			Repo: pkg.Repo{
				Owner:         "bborbe",
				Name:          "docker-utils",
				DefaultBranch: "master",
			},
			HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			CurrentVersion:    "v1.7.7",
			UnreleasedBullets: 5,
			AutoRelease:       false,
		}
		cfg := pkg.TaskConfig{Stage: "dev"}

		cmd1 := pkg.BuildCreateCommand(release, cfg)
		cmd2 := pkg.BuildCreateCommand(release, cfg)

		Expect(cmd1.Frontmatter).To(Equal(cmd2.Frontmatter))
		Expect(cmd1.TaskIdentifier).To(Equal(cmd2.TaskIdentifier))
	})
})

var _ = Describe("pkg.TaskPublisher", func() {
	It("PublishCreate returns true and calls IncPublished(\"create\") on send success", func() {
		fakeSender := &fakeCreateCommandSender{}
		fakeMetrics := new(mocks.Metrics)
		publisher := pkg.NewTaskPublisher(fakeSender, fakeMetrics, pkg.TaskConfig{Stage: "dev"})

		release := pkg.Release{
			Repo: pkg.Repo{
				Owner:         "bborbe",
				Name:          "docker-utils",
				DefaultBranch: "master",
			},
			HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			CurrentVersion:    "v1.7.7",
			UnreleasedBullets: 5,
			AutoRelease:       false,
		}

		result := publisher.PublishCreate(context.Background(), release)

		Expect(result).To(BeTrue())
		Expect(fakeMetrics.IncPublishedCallCount()).To(Equal(1))
		Expect(fakeMetrics.IncPublishedArgsForCall(0)).To(Equal("create"))
		Expect(fakeSender.capturedCmds).To(HaveLen(1))
		Expect(fakeSender.capturedCmds[0].Frontmatter["task_type"]).To(Equal("github-release"))
	})

	It("PublishCreate returns false and calls IncPublished(\"error\") on send failure", func() {
		fakeSender := &fakeCreateCommandSender{
			sendErr: errors.New("kafka send failed"),
		}
		fakeMetrics := new(mocks.Metrics)
		publisher := pkg.NewTaskPublisher(fakeSender, fakeMetrics, pkg.TaskConfig{Stage: "dev"})

		release := pkg.Release{
			Repo: pkg.Repo{
				Owner:         "bborbe",
				Name:          "docker-utils",
				DefaultBranch: "master",
			},
			HeadSHA:           "d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			CurrentVersion:    "v1.7.7",
			UnreleasedBullets: 5,
			AutoRelease:       false,
		}

		result := publisher.PublishCreate(context.Background(), release)

		Expect(result).To(BeFalse())
		Expect(fakeMetrics.IncPublishedCallCount()).To(Equal(1))
		Expect(fakeMetrics.IncPublishedArgsForCall(0)).To(Equal("error"))
	})
})
