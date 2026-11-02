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
// maintainerconfig because the schema this module pins has no `dockerBuild`
// field: the key is dropped on decode, so an opted-in repo reads as "not opted
// in" and nothing is ever built. (v0.51.0 exports it; this module does not pin
// that version yet, and parsing the raw bytes also lets a repo carry the
// watcher-only fields below, which the shared schema does not model.)
type DockerBuildConfig struct {
	DockerBuild struct {
		// AutoBuild opts the repo into automatic builds on release-tag push.
		AutoBuild bool `json:"autoBuild"`
		// Destinations names the registries the image is pushed to. The first is
		// the image repository the template builds.
		Destinations []string `json:"destinations"`
		// PushSecret names the dockerconfigjson Secret mounted at /docker-config.
		PushSecret string `json:"pushSecret"`
		// DeadlineSeconds is the Job's activeDeadlineSeconds.
		DeadlineSeconds int `json:"deadlineSeconds"`
		// Dockerfile is the Dockerfile filename.
		Dockerfile string `json:"dockerfile"`
		// ServicePath is the repo-relative service path.
		ServicePath string `json:"servicePath"`
		// ContextPath is the BuildKit context local.
		ContextPath string `json:"contextPath"`
		// DockerfilePath is the BuildKit dockerfile local.
		DockerfilePath string `json:"dockerfilePath"`
	} `json:"dockerBuild"`
}

//counterfeiter:generate -o ../../mocks/build_file_fetcher.go --fake-name BuildFileFetcher . FileFetcher

// FileFetcher returns the raw bytes of a file at a ref, or nil when the file
// does not exist.
type FileFetcher interface {
	GetFileContent(ctx context.Context, repo, path, ref string) ([]byte, error)
}

// NewConfigProvider returns a ConfigProvider that reads the `dockerBuild` block
// from the repo's `.maintainer.yaml` on the default branch.
//
// Absence is not an error: a repo with no `.maintainer.yaml`, no `dockerBuild`
// block, or `autoBuild: false` is simply not opted in. That default is what
// keeps the watcher from building every tag of every allowlisted repo.
func NewConfigProvider(fetcher FileFetcher) ConfigProvider {
	return &configProvider{fetcher: fetcher}
}

type configProvider struct {
	fetcher FileFetcher
}

func (p *configProvider) Config(ctx context.Context, repo string) (Config, error) {
	// The tag is not passed through: the opt-in is a repo property rather than a
	// per-release one. Reading the default branch keeps a build's opt-in
	// decision independent of which tag triggered it.
	raw, err := p.fetcher.GetFileContent(ctx, repo, MaintainerConfigPath, "")
	if err != nil {
		return Config{}, errors.Wrapf(
			ctx, err, "read %s repo=%s", MaintainerConfigPath, repo,
		)
	}
	if len(raw) == 0 {
		return Config{}, nil
	}
	return ParseBuildConfig(ctx, raw, repo)
}

// ParseBuildConfig decodes the build configuration from raw `.maintainer.yaml`
// bytes. Malformed YAML is an error, never a silent "not opted in": a typo in
// the opt-in must be visible, not read as a deliberate no.
func ParseBuildConfig(ctx context.Context, raw []byte, repo string) (Config, error) {
	var cfg DockerBuildConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, errors.Wrapf(
			ctx, err, "parse %s dockerBuild", MaintainerConfigPath,
		)
	}
	b := cfg.DockerBuild
	return Config{
		AutoBuild:       b.AutoBuild,
		Destinations:    b.Destinations,
		PushSecret:      orDefault(b.PushSecret, DefaultPushSecret),
		DeadlineSeconds: orDefaultInt(b.DeadlineSeconds, DefaultDeadlineSeconds),
		Dockerfile:      b.Dockerfile,
		ServicePath:     orDefault(b.ServicePath, repo),
		ContextPath:     b.ContextPath,
		DockerfilePath:  b.DockerfilePath,
	}, nil
}

// orDefault returns v, or fallback when v is empty.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// orDefaultInt returns v, or fallback when v is not positive.
//
// A zero here is not "leave the manifest's value alone": the renderer
// substitutes it unconditionally, so zero would render
// `activeDeadlineSeconds: 0`, which the API server rejects. The fallback is
// what makes a `dockerBuild` block carrying only `autoBuild` and `destinations`
// produce a valid Job.
func orDefaultInt(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
