// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command_test

import (
	"context"
	"encoding/json"

	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/github-release-watcher/pkg/command"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TriggerReleaseCheckCommandOperation", func() {
	It("has expected string value", func() {
		Expect(command.TriggerReleaseCheckCommandOperation).
			To(Equal(base.CommandOperation("trigger-release-check")))
	})

	It("passes cqrs operation regex validation", func() {
		// Boundary test: catches renames that violate the
		// `^[a-z][a-z-]*$` cqrs wire-string regex (e.g. underscores,
		// leading digit, uppercase). Per agent/lib precedent every
		// CommandOperation constant gets this check.
		Expect(command.TriggerReleaseCheckCommandOperation.Validate(context.Background())).
			To(Succeed())
	})
})

var _ = Describe("TriggerReleaseCheckCommand", func() {
	It("round-trips through JSON with both fields set", func() {
		cmd := command.TriggerReleaseCheckCommand{
			Scope: "bborbe/repo",
			Force: true,
		}
		data, err := json.Marshal(cmd)
		Expect(err).To(BeNil())

		var got command.TriggerReleaseCheckCommand
		Expect(json.Unmarshal(data, &got)).To(Succeed())
		Expect(got.Scope).To(Equal(cmd.Scope))
		Expect(got.Force).To(Equal(cmd.Force))
	})

	It("omits scope and force when zero (omitempty)", func() {
		cmd := command.TriggerReleaseCheckCommand{}
		data, err := json.Marshal(cmd)
		Expect(err).To(BeNil())
		jsonStr := string(data)
		Expect(jsonStr).NotTo(ContainSubstring("\"scope\""))
		Expect(jsonStr).NotTo(ContainSubstring("\"force\""))
	})

	It("JSON contains scope and force keys when set", func() {
		cmd := command.TriggerReleaseCheckCommand{
			Scope: "bborbe/repo",
			Force: true,
		}
		data, err := json.Marshal(cmd)
		Expect(err).To(BeNil())
		jsonStr := string(data)
		Expect(jsonStr).To(ContainSubstring(`"scope"`))
		Expect(jsonStr).To(ContainSubstring(`"force"`))
	})
})

var _ = Describe("TriggerReleaseCheckCommand.Validate", func() {
	It("accepts the empty payload {}", func() {
		cmd := command.TriggerReleaseCheckCommand{}
		Expect(cmd.Validate(context.Background())).To(Succeed())
	})

	It("accepts a populated payload (Scope and Force)", func() {
		cmd := command.TriggerReleaseCheckCommand{
			Scope: "bborbe/repo",
			Force: true,
		}
		Expect(cmd.Validate(context.Background())).To(Succeed())
	})
})
