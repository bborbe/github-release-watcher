// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg/command"
	libhttp "github.com/bborbe/http"
	libparse "github.com/bborbe/parse"
	"github.com/golang/glog"
)

//counterfeiter:generate -o ../../mocks/trigger_release_check_handler.go --fake-name TriggerReleaseCheckHandler . TriggerReleaseCheckHandler

// TriggerReleaseCheckHandler handles POST /trigger.
// The handler is intentionally minimal: parse the optional ?force=<bool>
// query parameter via libparse.ParseBoolDefault (spec 071), build a
// TriggerReleaseCheckCommand carrying the parsed Force value, publish
// it to Kafka via the injected sender, and return HTTP 202. The Scope
// field stays reserved-unread (spec Non-goal: per-repo filter UX is a
// separate spec). All scan-cycle work is owned by the in-pod command
// consumer.
type TriggerReleaseCheckHandler = libhttp.WithError

// NewTriggerReleaseCheckHandler returns a handler that publishes a
// TriggerReleaseCheckCommand to Kafka for each /trigger request and
// returns 202. The sender is the only collaborator.
func NewTriggerReleaseCheckHandler(
	sender command.TriggerReleaseCheckCommandSender,
) TriggerReleaseCheckHandler {
	return &triggerReleaseCheckHandler{
		sender: sender,
	}
}

type triggerReleaseCheckHandler struct {
	sender command.TriggerReleaseCheckCommandSender
}

func (h *triggerReleaseCheckHandler) ServeHTTP(
	ctx context.Context,
	resp http.ResponseWriter,
	req *http.Request,
) error {
	force := libparse.ParseBoolDefault(
		ctx,
		req.URL.Query().Get("force"),
		false,
	)
	if err := h.sender.SendCommand(ctx, command.TriggerReleaseCheckCommand{Force: force}); err != nil {
		// 502 BadGateway over 500/503: upstream Kafka is the proximate cause,
		// not this service. 500 implies an unexpected handler bug; 503 implies
		// this service is unhealthy. Kafka publish failure is neither — it's
		// an upstream gateway dependency, so 502 is the most accurate signal
		// for operators + observability tools.
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "send TriggerReleaseCheckCommand"),
			http.StatusBadGateway,
		)
	}

	glog.V(2).Infof(
		"trigger accepted op=%s force=%t",
		command.TriggerReleaseCheckCommandOperation, force,
	)
	return writeAccepted(resp)
}

// writeAccepted emits the 202 response with body {"status":"accepted"}.
func writeAccepted(resp http.ResponseWriter) error {
	resp.Header().Set("Content-Type", "application/json")
	resp.WriteHeader(http.StatusAccepted)
	return json.NewEncoder(resp).Encode(map[string]interface{}{
		"status": "accepted",
	})
}
