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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
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

// NewBuildCommitEpochProvider adapts the GitHub client to
// build.CommitEpochProvider — the __SOURCE_DATE_EPOCH__ source.
func NewBuildCommitEpochProvider(ghClient GitHubClient) build.CommitEpochProvider {
	return &buildCommitEpochProvider{ghClient: ghClient}
}

type buildCommitEpochProvider struct {
	ghClient GitHubClient
}

func (p *buildCommitEpochProvider) CommitEpoch(
	ctx context.Context,
	repo, ref string,
) (int64, error) {
	r, ok := repoFromKey(repo)
	if !ok {
		return 0, errors.Errorf(ctx, "malformed repo key %q", repo)
	}
	return p.ghClient.GetCommitEpoch(ctx, r, ref)
}

// BuildJobServiceAnnotation is the annotation carrying a build Job's
// owner-qualified service path. An annotation rather than a label because the
// value may contain "/", which a label value may not.
const BuildJobServiceAnnotation = "build.benjamin-borbe.de/service"

// NewBuildJobLister adapts the cluster clientset to build.JobLister.
//
// The list is filtered to non-terminal Jobs on purpose: completed Jobs linger
// until ttlSecondsAfterFinished expires (3600s in the reference template), so a
// bare label list would report an hour of finished builds as "active" and
// suppress every legitimate re-run of the same tag.
func NewBuildJobLister(clientset kubernetes.Interface) build.JobLister {
	return &buildJobLister{clientset: clientset}
}

type buildJobLister struct {
	clientset kubernetes.Interface
}

func (l *buildJobLister) ActiveByTag(
	ctx context.Context,
	tag string,
) ([]build.ActiveJob, error) {
	// Built from a Set rather than formatted into a string: the tag is the
	// selector's value, and a value carrying `,` or `=` would otherwise
	// terminate the requirement and widen the match — silently defeating the
	// dedupe. NewRequirement validates the value and returns an error instead.
	selector := labels.Set{
		"app.kubernetes.io/component": "build",
		"app.kubernetes.io/version":   tag,
	}.AsSelector()

	list, err := l.clientset.BatchV1().Jobs(build.DefaultBuildNamespace).List(
		ctx,
		metav1.ListOptions{LabelSelector: selector.String()},
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "list build jobs tag=%s", tag)
	}
	active := make([]build.ActiveJob, 0, len(list.Items))
	for _, job := range list.Items {
		if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
			continue
		}
		if job.DeletionTimestamp != nil {
			continue
		}
		active = append(active, build.ActiveJob{
			Name:    job.Name,
			Service: job.Annotations[BuildJobServiceAnnotation],
		})
	}
	return active, nil
}

// repoFromKey splits an "owner/name" key into a Repo.
func repoFromKey(key string) (Repo, bool) {
	owner, name, ok := strings.Cut(key, "/")
	if !ok || owner == "" || name == "" {
		return Repo{}, false
	}
	return Repo{Owner: owner, Name: name}, true
}
