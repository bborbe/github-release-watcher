// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"sync"
	"time"

	libtime "github.com/bborbe/time"
)

// Debouncer collapses repeated triggers for the same key within a minimum
// interval, so a burst of events (e.g. the obsidian-openclaw autocommit daemon
// pushing ~3/min) produces at most one accepted trigger per interval.
type Debouncer interface {
	// Allow reports whether a trigger for key is permitted now. Returns false
	// (deny) when a previous trigger for the same key happened less than the
	// configured interval ago; records the trigger time on approval. The
	// caller records the deny reason for traceability (the webhook handler
	// increments webhook_skipped_total{reason="debounced"}).
	Allow(key string) bool
}

type debouncer struct {
	interval time.Duration
	clock    libtime.CurrentDateTimeGetter
	mu       sync.Mutex
	last     map[string]libtime.DateTime
}

// NewDebouncer returns a Debouncer that allows a trigger for key at most once
// per interval. The clock is injected so tests can fix the current time
// (libtime go-time rule: never time.Now() in business logic).
func NewDebouncer(interval time.Duration, clock libtime.CurrentDateTimeGetter) Debouncer {
	return &debouncer{
		interval: interval,
		clock:    clock,
		last:     map[string]libtime.DateTime{},
	}
}

func (d *debouncer) Allow(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.clock.Now()
	if last, ok := d.last[key]; ok && now.Sub(last).Duration() < d.interval {
		return false
	}
	d.last[key] = now
	return true
}
