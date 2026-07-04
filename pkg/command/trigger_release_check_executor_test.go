// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command_test

import (
	"context"

	"github.com/bborbe/cqrs/base"
	cdb "github.com/bborbe/cqrs/cdb"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/command"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/maintainer/lib"
)

// outcome is the three-state exit-path classifier for the table-driven test.
// success:    no error, Watcher.Poll invoked once.
// skipped:    errors.Is(err, cdb.ErrCommandObjectSkipped) — non-retryable.
// wrappedErr: err is non-nil and NOT ErrCommandObjectSkipped — transient.
type outcome int

const (
	outcomeSuccess outcome = iota
	outcomeSkipped
	outcomeWrappedErr
)

func mustParseEvent(cmd command.TriggerReleaseCheckCommand) base.Event {
	evt, err := base.ParseEvent(context.Background(), cmd)
	Expect(err).NotTo(HaveOccurred())
	return evt
}

func newCommandObject(cmd command.TriggerReleaseCheckCommand) cdb.CommandObject {
	return cdb.CommandObject{
		Command: base.Command{
			Operation: command.TriggerReleaseCheckCommandOperation,
			Data:      mustParseEvent(cmd),
		},
		SchemaID: lib.GithubReleaserV1SchemaID,
	}
}

var _ = Describe("NewTriggerReleaseCheckCommandExecutor", func() {
	var (
		ctx     context.Context
		watcher *mocks.Watcher
	)

	BeforeEach(func() {
		ctx = context.Background()
		watcher = new(mocks.Watcher)
	})

	// NOTE: validate-fail is NOT exercised as a table entry below. Reason:
	// the github-release command's Validate is empty (Scope is still
	// reserved-unread and Force is just a boolean — nothing to reject
	// today). The only
	// way to force a framework-level "CommandObject.Validate" failure in
	// a unit test is to construct a CommandObject whose
	// Command.Operation doesn't match the executor's expected operation
	// — and the executor's HandleCommand is invoked directly in this
	// test, so that mismatch is a no-op here. The end-to-end validate-fail
	// coverage is owned by the integration test in prompt 4
	// (consumer-level test against a real cdb.RunCommandConsumerTxDefault
	// with an out-of-schema message).
	DescribeTable("exit-path mapping",
		func(
			configure func(w *mocks.Watcher),
			obj cdb.CommandObject,
			expectOutcome outcome, // skipped | wrappedErr | success
		) {
			// Reset the watcher between entries — the table shares a
			// single fixture so we need to clear per-Entry state.
			*watcher = mocks.Watcher{}
			configure(watcher)

			_, _, err := command.RunTriggerReleaseCheck(ctx, nil, obj, watcher)

			switch expectOutcome {
			case outcomeSkipped:
				Expect(err).To(HaveOccurred(), "expected ErrCommandObjectSkipped")
				Expect(errors.Is(err, cdb.ErrCommandObjectSkipped)).To(BeTrue(),
					"expected ErrCommandObjectSkipped, got %v", err)
			case outcomeWrappedErr:
				Expect(err).To(HaveOccurred(), "expected wrapped (transient) error")
				Expect(errors.Is(err, cdb.ErrCommandObjectSkipped)).To(BeFalse(),
					"transient errors must NOT be classified as Skipped, got %v", err)
				Expect(err.Error()).To(ContainSubstring("poll cycle from trigger"),
					"transient errors must be wrapped with poll-cycle context, got %v", err)
			case outcomeSuccess:
				Expect(err).NotTo(HaveOccurred(), "unexpected error: %v", err)
			}
			// spec AC 8: Watcher.Poll must be invoked exactly once per
			// valid command. The malformed-payload path short-circuits
			// in unmarshalAndValidate, so for outcomeSkipped Poll must
			// NOT have been called (0 == the default zero call count).
			if expectOutcome == outcomeSkipped {
				Expect(watcher.PollCallCount()).To(Equal(0),
					"skipped payloads must not invoke Watcher.Poll")
			} else {
				Expect(watcher.PollCallCount()).To(Equal(1),
					"valid payloads must invoke Watcher.Poll exactly once")
			}
		},
		Entry("valid command → success + PollCallCount==1",
			func(_ *mocks.Watcher) {},
			newCommandObject(command.TriggerReleaseCheckCommand{Scope: "bborbe/repo", Force: true}),
			outcomeSuccess,
		),
		Entry("malformed payload → skipped",
			// Force MarshalInto to fail: feed a CommandObject whose Data
			// is an Event with `scope` set to a JSON object — the
			// unmarshal step rejects "object into string".
			func(_ *mocks.Watcher) {},
			cdb.CommandObject{
				Command: base.Command{
					Operation: command.TriggerReleaseCheckCommandOperation,
					Data: base.Event{
						"scope": map[string]interface{}{"unexpected": "object"},
					},
				},
				SchemaID: lib.GithubReleaserV1SchemaID,
			},
			outcomeSkipped,
		),
		Entry("watcher returns error → wrapped err (not skipped)",
			func(w *mocks.Watcher) {
				w.PollReturns(errors.Errorf(ctx, "rate limited"))
			},
			newCommandObject(command.TriggerReleaseCheckCommand{}),
			outcomeWrappedErr,
		),
	)
})

var _ = Describe("executor force flag plumbing (spec 071)", func() {
	var (
		ctx     context.Context
		watcher *mocks.Watcher
	)

	BeforeEach(func() {
		ctx = context.Background()
		watcher = &mocks.Watcher{}
	})

	It("force=true ⇒ Poll(ctx, true)", func() {
		commandObject := newCommandObject(
			command.TriggerReleaseCheckCommand{Force: true},
		)
		_, _, err := command.RunTriggerReleaseCheck(ctx, nil, commandObject, watcher)
		Expect(err).NotTo(HaveOccurred())

		Expect(watcher.PollCallCount()).To(Equal(1))
		_, skip := watcher.PollArgsForCall(0)
		Expect(skip).To(BeTrue())
	})

	It("force=false ⇒ Poll(ctx, false)", func() {
		commandObject := newCommandObject(
			command.TriggerReleaseCheckCommand{Force: false},
		)
		_, _, err := command.RunTriggerReleaseCheck(ctx, nil, commandObject, watcher)
		Expect(err).NotTo(HaveOccurred())

		Expect(watcher.PollCallCount()).To(Equal(1))
		_, skip := watcher.PollArgsForCall(0)
		Expect(skip).To(BeFalse())
	})

	// The error-wrap message and the success log line share the same
	// `force=%t` formatter at lines 73 and 77 of trigger_release_check_executor.go,
	// so asserting the wrap message is sufficient evidence the same
	// `force=<bool>` substring lands in the success log path. This avoids
	// inventing glog-capture test infrastructure the repo does not have.
	It("force=true wrap message contains force=true", func() {
		watcher.PollReturns(errors.Errorf(ctx, "rate limited"))
		commandObject := newCommandObject(
			command.TriggerReleaseCheckCommand{Force: true},
		)
		_, _, err := command.RunTriggerReleaseCheck(ctx, nil, commandObject, watcher)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("force=true"))
	})

	It("force=false wrap message contains force=false", func() {
		watcher.PollReturns(errors.Errorf(ctx, "rate limited"))
		commandObject := newCommandObject(
			command.TriggerReleaseCheckCommand{Force: false},
		)
		_, _, err := command.RunTriggerReleaseCheck(ctx, nil, commandObject, watcher)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("force=false"))
	})
})

var _ = Describe("executor crash recovery (spec 067 AC 19)", func() {
	// Proves at-least-once-via-idempotent-downstream: simulate a pod kill
	// mid-execution (context cancelled during watcher.Poll) and verify
	// that on retry the same Watcher.Poll call runs again from scratch
	// (i.e. PollCallCount==1 on the fresh Watcher — the framework's
	// redelivery is responsible for the second invocation overall).
	//
	// goleak: not used here (not a project dep) — rely on the
	// ctx-cancellation contract only.
	var (
		ctx     context.Context
		watcher *mocks.Watcher
	)

	BeforeEach(func() {
		ctx = context.Background()
		watcher = new(mocks.Watcher)
	})

	It("a killed invocation can be retried and Poll runs once on the fresh watcher", func() {
		// Round 1: simulate a real Watcher that respects context
		// cancellation. The stub honours ctx.Err() and returns the
		// context-cancelled error — same shape as a real watcher that
		// gets SIGKILL'd in mid-Poll.
		killedCtx, cancel := context.WithCancel(ctx)
		watcher.PollStub = func(c context.Context, _ bool) error {
			// Cancel mid-call, then return the context error like a real Watcher would.
			cancel()
			return c.Err()
		}

		cmd := command.TriggerReleaseCheckCommand{Scope: "bborbe/repo"}
		commandObject := newCommandObject(cmd)

		_, _, err := command.RunTriggerReleaseCheck(
			killedCtx, nil, commandObject, watcher,
		)
		Expect(err).To(HaveOccurred(),
			"killed invocation must return a transient error so Kafka redelivers")
		Expect(errors.Is(err, cdb.ErrCommandObjectSkipped)).To(BeFalse(),
			"killed invocation must NOT be classified as Skipped (transient, not deliberate)")
		Expect(err.Error()).To(ContainSubstring("poll cycle from trigger"),
			"killed invocation must be wrapped with poll-cycle context")
		Expect(watcher.PollCallCount()).To(Equal(1),
			"killed invocation must have called Poll once before failing")

		// Round 2: fresh context, fresh Watcher (PollReturns(nil)).
		// The same commandObject is reused (Kafka would redeliver it as-is).
		freshWatcher := new(mocks.Watcher)
		freshWatcher.PollReturns(nil)

		_, _, err = command.RunTriggerReleaseCheck(
			context.Background(), nil, commandObject, freshWatcher,
		)
		Expect(err).NotTo(HaveOccurred(), "retry must succeed: %v", err)
		Expect(freshWatcher.PollCallCount()).To(Equal(1),
			"retry must invoke Poll on the fresh Watcher exactly once")
	})
})
