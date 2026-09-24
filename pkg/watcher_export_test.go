// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// FilterSkipMessage re-exports the private filterSkipMessage for the external
// test package. The _test.go suffix keeps this file out of production builds.
var FilterSkipMessage = filterSkipMessage

// Compile-time guard: keep the public surface tightly aligned with the
// internal helper. If filterSkipMessage's signature ever drifts, this file
// fails to build and the test breakage is local.
var _ = func(repoKey, reason string) string {
	return filterSkipMessage(repoKey, reason)
}
