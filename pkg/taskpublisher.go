// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	task "github.com/bborbe/agent/command/task"
	"github.com/golang/glog"
)

// TaskConfig groups per-task envelope settings (stage routing).
type TaskConfig struct {
	Stage string // "dev" or "prod" — frontmatter `stage`
	// TargetVault routes the CreateTaskCommand to a specific vault controller
	// (matched verbatim against the controller's VAULT_NAME). Empty leaves
	// TargetVault unset, so the controller's legacy default-vault fallback
	// applies — preserving pre-TARGET_VAULT behaviour.
	TargetVault string
}

//counterfeiter:generate -o ../mocks/task_publisher.go --fake-name TaskPublisher . TaskPublisher

// TaskPublisher builds the CreateTaskCommand per [[Agent Task File Contract]] and
// sends it via the supplied CreateCommandSender. Returns true on successful send.
type TaskPublisher interface {
	PublishCreate(ctx context.Context, release Release) bool
}

// NewTaskPublisher returns a TaskPublisher that wraps the given sender + metrics.
func NewTaskPublisher(
	sender task.CreateCommandSender,
	metrics Metrics,
	cfg TaskConfig,
) TaskPublisher {
	return &taskPublisher{sender: sender, metrics: metrics, cfg: cfg}
}

type taskPublisher struct {
	sender  task.CreateCommandSender
	metrics Metrics
	cfg     TaskConfig
}

func (p *taskPublisher) PublishCreate(ctx context.Context, release Release) bool {
	cmd := BuildCreateCommand(release, p.cfg)

	if err := p.sender.SendCommand(ctx, cmd); err != nil {
		glog.Errorf(
			"publish create-task failed repo=%s sha=%s taskID=%s err=%v",
			release.Repo.Key(),
			release.HeadSHA,
			string(cmd.TaskIdentifier),
			err,
		)
		p.metrics.IncPublished("error")
		return false
	}
	glog.V(2).Infof(
		"published CreateTaskCommand repo=%s sha=%s taskID=%s stage=%s",
		release.Repo.Key(),
		release.HeadSHA,
		string(cmd.TaskIdentifier),
		p.cfg.Stage,
	)
	p.metrics.IncPublished("create")
	return true
}
