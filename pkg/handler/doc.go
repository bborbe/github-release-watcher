// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package handler contains the HTTP handlers for the github-release watcher.
// /trigger publishes a TriggerReleaseCheckCommand to Kafka and returns 202;
// the heavy lifting (Poll cycle) happens in the in-pod command consumer.
package handler
