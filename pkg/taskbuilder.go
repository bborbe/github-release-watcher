// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"fmt"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
)

// BuildCreateCommand assembles the CreateTaskCommand for a Release.
func BuildCreateCommand(release Release, cfg TaskConfig) task.CreateCommand {
	taskIDStr := DeriveTaskID(release.Repo.Owner, release.Repo.Name, release.HeadSHA).String()
	return task.CreateCommand{
		Title:          ComputeTaskTitle(release),
		TargetVault:    cfg.TargetVault,
		TaskIdentifier: agentlib.TaskIdentifier(taskIDStr),
		Frontmatter:    buildFrontmatter(release, taskIDStr, cfg),
		Body:           buildTaskBody(release),
	}
}

func buildFrontmatter(release Release, taskIDStr string, cfg TaskConfig) agentlib.TaskFrontmatter {
	return agentlib.TaskFrontmatter{
		"task_type":       "github-release",
		"assignee":        "github-releaser-agent",
		"phase":           "planning",
		"status":          "in_progress",
		"stage":           cfg.Stage,
		"task_identifier": taskIDStr,
		"title":           ComputeTaskTitle(release),
		"repo":            fmt.Sprintf("%s/%s", release.Repo.Owner, release.Repo.Name),
		"clone_url": fmt.Sprintf(
			"git@github.com:%s/%s.git",
			release.Repo.Owner,
			release.Repo.Name,
		),
		"ref":             release.HeadSHA,
		"current_version": release.CurrentVersion,
	}
}

func buildTaskBody(release Release) string {
	owner := release.Repo.Owner
	name := release.Repo.Name
	return fmt.Sprintf(
		"# Release: %s/%s\n\n**Current version:** %s\n**HEAD:** %s\n**Changelog:** https://github.com/%s/%s/blob/master/CHANGELOG.md\n**Repo:** [%s/%s](https://github.com/%s/%s)\n",
		owner,
		name,
		release.CurrentVersion,
		release.ShortSHA(),
		owner,
		name,
		owner,
		name,
		owner,
		name,
	)
}
