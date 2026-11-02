// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"
	stderrors "errors"

	"github.com/bborbe/errors"
)

// Destination is one named destination class from `.maintainer.yaml`'s
// `dockerBuild.destinations`.
//
// A class, deliberately not a registry URL: the shared schema
// (`github.com/bborbe/maintainer/maintainerconfig`) defines the field as named
// classes so moving a registry is one code change here rather than an edit to
// every opted-in repo. Reading the value as a URL instead would build
// `dockerhub/bborbe/x` — a repo path with no registry host, which BuildKit
// pushes to the default registry and which the pre-flight probe then asks a
// registry that never has it.
type Destination string

const (
	// DestinationDockerHub is DockerHub (docker.io).
	DestinationDockerHub Destination = "dockerhub"
	// DestinationGHCR is the GitHub Container Registry.
	DestinationGHCR Destination = "ghcr"
	// DestinationInternal is the in-house nuke registry.
	DestinationInternal Destination = "internal"
)

// registryHosts maps each class to its registry host.
//
// internal resolves to the PROD nuke registry for both stages: the dev and prod
// nuke clusters pull from the same host, so a dev build lands where the dev
// deployment reads it. Switching dev to a separate registry would need a mirror
// step first (see nuke's github-releaser Makefile for the same reasoning).
var registryHosts = map[Destination]string{
	DestinationDockerHub: "docker.io",
	DestinationGHCR:      "ghcr.io",
	DestinationInternal:  "docker.prod.nuke.benjamin-borbe.de:443",
}

// ErrUnknownDestination reports a destination class this build arm cannot
// resolve.
//
// An error rather than a skip: an unknown class is a typo or a class added to
// the shared schema that this binary predates, and both are configuration
// faults an operator must see. Treating it as "not opted in" would silently
// stop building a repo that believes it opted in.
var ErrUnknownDestination = stderrors.New("unknown dockerBuild destination class")

// ImageRepository resolves a destination class and a repo key to the image
// repository the build pushes to, without a tag.
func ImageRepository(ctx context.Context, dest Destination, repo string) (string, error) {
	host, ok := registryHosts[dest]
	if !ok {
		return "", errors.Wrapf(
			ctx, ErrUnknownDestination,
			"resolve build destination=%s repo=%s (known: dockerhub, ghcr, internal)",
			dest, repo,
		)
	}
	return host + "/" + repo, nil
}

// ValidateDestination reports whether a destination class is resolvable.
func ValidateDestination(dest Destination) bool {
	_, ok := registryHosts[dest]
	return ok
}
