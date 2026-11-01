// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg/build"
	libk8s "github.com/bborbe/k8s"
	batchv1 "k8s.io/api/batch/v1"
)

// NewBuildManifestLoader adapts the GitHub client to build.ManifestLoader: it
// reads the repo's `hack/build-job.yaml` at the tag being built.
//
// Reading at the tag (not the default branch) is deliberate — the build recipe
// that ships with the release is the one that builds it, so a tag stays
// reproducible after later commits change the recipe.
func NewBuildManifestLoader(ghClient GitHubClient) build.ManifestLoader {
	return &buildManifestLoader{ghClient: ghClient}
}

type buildManifestLoader struct {
	ghClient GitHubClient
}

func (l *buildManifestLoader) LoadBuildJobManifest(
	ctx context.Context,
	repo, tag string,
) ([]byte, error) {
	r, ok := repoFromKey(repo)
	if !ok {
		return nil, errors.Errorf(ctx, "malformed repo key %q", repo)
	}
	return l.ghClient.GetFileContent(ctx, r, build.BuildJobManifestPath, tag)
}

// NewBuildFileFetcher adapts the GitHub client to build.FileFetcher — the
// narrow surface the auto-build opt-in gate reads.
func NewBuildFileFetcher(ghClient GitHubClient) build.FileFetcher {
	return &buildFileFetcher{ghClient: ghClient}
}

type buildFileFetcher struct {
	ghClient GitHubClient
}

func (f *buildFileFetcher) GetFileContent(
	ctx context.Context,
	repo, path, ref string,
) ([]byte, error) {
	r, ok := repoFromKey(repo)
	if !ok {
		return nil, errors.Errorf(ctx, "malformed repo key %q", repo)
	}
	return f.ghClient.GetFileContent(ctx, r, path, ref)
}

// NewBuildJobCreator adapts the cluster's Job deployer to build.JobCreator,
// translating the deployer's already-exists sentinel into the one the
// dispatcher uses for its idempotent no-op path.
func NewBuildJobCreator(deployer libk8s.JobDeployer) build.JobCreator {
	return &buildJobCreator{deployer: deployer}
}

type buildJobCreator struct {
	deployer libk8s.JobDeployer
}

func (c *buildJobCreator) Create(ctx context.Context, job batchv1.Job) error {
	if err := c.deployer.Deploy(ctx, job); err != nil {
		if stderrors.Is(err, libk8s.JobAlreadyExistsError) {
			return build.ErrJobAlreadyExists
		}
		return errors.Wrapf(ctx, err, "deploy build job %s/%s", job.Namespace, job.Name)
	}
	return nil
}

// repoFromKey splits an "owner/name" key into a Repo.
func repoFromKey(key string) (Repo, bool) {
	owner, name, ok := strings.Cut(key, "/")
	if !ok || owner == "" || name == "" {
		return Repo{}, false
	}
	return Repo{Owner: owner, Name: name}, true
}
