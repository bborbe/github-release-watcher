// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"

	"github.com/bborbe/errors"
	"sigs.k8s.io/yaml"
)

// MaintainerConfigPath is the repo-relative path of the opt-in file.
const MaintainerConfigPath = ".maintainer.yaml"

// DockerBuildConfig is the `dockerBuild` block of `.maintainer.yaml`.
//
// It is parsed here rather than through `github.com/bborbe/maintainer`'s
// maintainerconfig because that schema (v0.50.12, the version pinned by this
// module) has no `dockerBuild` field: the key is dropped on decode, so an
// opted-in repo reads as "not opted in" and nothing is ever built. Once the
// shared schema carries the field this type can be deleted and the gate pointed
// at maintainerconfig instead.
type DockerBuildConfig struct {
	DockerBuild struct {
		// AutoBuild opts the repo into automatic builds on release-tag push.
		AutoBuild bool `json:"autoBuild"`
	} `json:"dockerBuild"`
}

//counterfeiter:generate -o ../../mocks/build_file_fetcher.go --fake-name BuildFileFetcher . FileFetcher

// FileFetcher returns the raw bytes of a file at a ref, or nil when the file
// does not exist.
type FileFetcher interface {
	GetFileContent(ctx context.Context, repo, path, ref string) ([]byte, error)
}

// NewAutoBuildGate returns an AutoBuildGate that reads `dockerBuild.autoBuild`
// from the repo's `.maintainer.yaml` at the tag being built.
//
// Absence is not an error: a repo with no `.maintainer.yaml`, no `dockerBuild`
// block, or `autoBuild: false` is simply not opted in. That default is what
// keeps the watcher from building every tag of every allowlisted repo.
func NewAutoBuildGate(fetcher FileFetcher) AutoBuildGate {
	return &autoBuildGate{fetcher: fetcher}
}

type autoBuildGate struct {
	fetcher FileFetcher
}

func (g *autoBuildGate) Enabled(ctx context.Context, repo string) (bool, error) {
	// The tag is not passed through: `Enabled` is called before the tag is
	// rendered into anything, and the opt-in is a repo property rather than a
	// per-release one. Reading the default branch keeps a build's opt-in
	// decision independent of which tag triggered it.
	raw, err := g.fetcher.GetFileContent(ctx, repo, MaintainerConfigPath, "")
	if err != nil {
		return false, errors.Wrapf(ctx, err, "read %s repo=%s", MaintainerConfigPath, repo)
	}
	if len(raw) == 0 {
		return false, nil
	}
	return ParseAutoBuild(ctx, raw)
}

// ParseAutoBuild decodes the opt-in flag from raw `.maintainer.yaml` bytes.
// Malformed YAML is an error, never a silent "not opted in": a typo in the
// opt-in must be visible, not read as a deliberate no.
func ParseAutoBuild(ctx context.Context, raw []byte) (bool, error) {
	var cfg DockerBuildConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return false, errors.Wrapf(ctx, err, "parse %s dockerBuild", MaintainerConfigPath)
	}
	return cfg.DockerBuild.AutoBuild, nil
}
