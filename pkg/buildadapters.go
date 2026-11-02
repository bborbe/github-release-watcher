// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-release-watcher/pkg/build"
	libk8s "github.com/bborbe/k8s"
	"github.com/golang/glog"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// dockerConfigJSONKey is the data key of a kubernetes.io/dockerconfigjson
// Secret, the same key the rendered Job mounts as its push credentials.
const dockerConfigJSONKey = ".dockerconfigjson"

// NewBuildCredentialReader adapts the cluster clientset to
// build.CredentialReader — it reads the dockerconfigjson Secret the build will
// push with, so the pre-flight can ask the registry as the same principal.
//
// The alternative — probing anonymously — is not the same question. Measured on
// nuke dev 2026-11-02: an anonymous Docker Hub token request from the cluster's
// egress returned 500 while the same URL answered 200 elsewhere, so the probe
// reported "registry unreachable" for a build that would have pushed fine.
func NewBuildCredentialReader(clientset kubernetes.Interface) build.CredentialReader {
	return &buildCredentialReader{clientset: clientset}
}

type buildCredentialReader struct {
	clientset kubernetes.Interface
}

// Credentials implements build.CredentialReader. A missing Secret or a missing
// auth entry is "no credentials configured", never an error: the probe then
// falls back to anonymous, which is what every caller did before this existed.
func (r *buildCredentialReader) Credentials(
	ctx context.Context,
	namespace, secretName, imageRef string,
) (build.Credentials, error) {
	ref, err := build.ParseImageRef(ctx, imageRef)
	if err != nil {
		return build.Credentials{}, errors.Wrapf(ctx, err, "parse image reference")
	}
	secret, err := r.clientset.CoreV1().Secrets(namespace).Get(
		ctx, secretName, metav1.GetOptions{},
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// One of the two "no credentials were found" states. It must say so:
			// silence here is indistinguishable from the bug this diagnostic
			// exists to explain.
			glog.V(2).Infof(
				"github-release-watcher registry credentials none matched secret=%s/%s registry=%s reason=secret_not_found",
				namespace, secretName, ref.Registry,
			)
			return build.Credentials{}, nil
		}
		return build.Credentials{}, errors.Wrapf(
			ctx, err, "get push secret %s/%s", namespace, secretName,
		)
	}
	raw, ok := secret.Data[dockerConfigJSONKey]
	if !ok {
		glog.V(2).Infof(
			"github-release-watcher registry credentials none matched secret=%s/%s registry=%s reason=no_dockerconfigjson_key",
			namespace, secretName, ref.Registry,
		)
		return build.Credentials{}, nil
	}
	var cfg struct {
		Auths map[string]struct {
			Auth     string `json:"auth"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// Never echo the payload: it is the credential itself.
		return build.Credentials{}, errors.Wrapf(
			ctx, err, "decode push secret %s/%s", namespace, secretName,
		)
	}
	// entryFound separates "the Secret has no entry for this registry" (add
	// one) from "it has one but it carries nothing usable" (populate it) —
	// different fixes, and `tried=` alone cannot tell them apart because it
	// lists candidates whether or not they are present.
	entryFound := false
	for _, key := range registryAuthKeys(ref.Registry) {
		entry, ok := cfg.Auths[key]
		if !ok {
			continue
		}
		entryFound = true
		username, password := entry.Username, entry.Password
		if entry.Auth != "" {
			if u, p, ok := decodeDockerAuth(entry.Auth); ok {
				username, password = u, p
			}
		}
		if username == "" && password == "" {
			continue
		}
		// Says only WHICH host matched — never a value, a length or a prefix.
		// This line exists because "the probe still fails" cannot be split into
		// "no credentials were found" and "credentials were sent and the
		// registry still refused" without it, and those two have different
		// fixes: a new Secret versus a network problem.
		glog.V(2).Infof(
			"github-release-watcher registry credentials resolved secret=%s/%s registry=%s key=%s",
			namespace, secretName, ref.Registry, key,
		)
		return build.Credentials{Username: username, Password: password}, nil
	}
	glog.V(2).Infof(
		"github-release-watcher registry credentials none matched secret=%s/%s registry=%s entry_found=%t tried=%v",
		namespace, secretName, ref.Registry, entryFound, registryAuthKeys(ref.Registry),
	)
	return build.Credentials{}, nil
}

// registryAuthKeys lists the dockerconfigjson keys that name a registry host.
//
// docker.io is the awkward one: the canonical key in a `docker login` file is
// the legacy `https://index.docker.io/v1/`, but hand-written and CI-generated
// files use the short names too, so all of them are accepted.
func registryAuthKeys(host string) []string {
	if host == "registry-1.docker.io" {
		return []string{
			"https://index.docker.io/v1/",
			"index.docker.io",
			"docker.io",
			"registry-1.docker.io",
		}
	}
	return []string{host, "https://" + host}
}

// decodeDockerAuth splits a dockerconfigjson `auth` value ("user:password",
// base64-encoded).
func decodeDockerAuth(value string) (string, string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return "", "", false
	}
	return username, password, true
}

// repoFromKey splits an "owner/name" key into a Repo.
func repoFromKey(key string) (Repo, bool) {
	owner, name, ok := strings.Cut(key, "/")
	if !ok || owner == "" || name == "" {
		return Repo{}, false
	}
	return Repo{Owner: owner, Name: name}, true
}
