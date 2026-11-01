// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"
	"crypto/rand"
	stderrors "errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bborbe/errors"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/yaml"
)

// BuildJobManifestPath is the repo-relative path of the build recipe the
// dispatcher fetches at the tag. The file is the target repo's own artifact —
// this package renders and applies it, it does not define it.
const BuildJobManifestPath = "hack/build-job.yaml"

// DefaultBuildNamespace is used when the rendered manifest names no namespace.
const DefaultBuildNamespace = "build"

// The canonical template dialect.
//
// These are the tokens `hack/build-job.yaml` carries and `hack/build-cluster.sh`
// renders for the manual path. The watcher renders the same file the same way:
// one template, one dialect, two entry points. The names are deliberately the
// reference renderer's, not this package's — a second spelling would mean the
// repo's template could only ever serve one of the two callers.
const (
	// TokenJobName is the Job name, DNS-safe and unique per dispatch.
	TokenJobName = "__JOB_NAME__"
	// TokenRepoURL is the git URL to clone.
	TokenRepoURL = "__REPO_URL__"
	// TokenRef is the git ref to build.
	TokenRef = "__REF__"
	// TokenTag is the image tag to push.
	TokenTag = "__TAG__"
	// TokenDockerfile is the Dockerfile filename, resolved against the dockerfile local.
	TokenDockerfile = "__DOCKERFILE__"
	// TokenImage is the target image repository, without tag.
	TokenImage = "__IMAGE__"
	// TokenPush is "true" or "false".
	TokenPush = "__PUSH__"
	// TokenPushSecret names the dockerconfigjson Secret mounted at /docker-config.
	// It is a parameter, not a constant: a nuke-registry build needs a secret
	// whose `auths` key is the nuke host, and the docker.io one fails at push
	// time — after the build has already run.
	TokenPushSecret = "__PUSH_SECRET__"
	// TokenDeadlineSeconds is the Job's activeDeadlineSeconds.
	TokenDeadlineSeconds = "__DEADLINE_SECONDS__"
	// TokenSourceDateEpoch is the built commit's timestamp in decimal seconds.
	//
	// This is not merely a build arg: the build container compares the cloned
	// commit's own epoch against this literal and exits non-zero on a mismatch,
	// so it must be the real epoch of the commit Ref resolves to.
	TokenSourceDateEpoch = "__SOURCE_DATE_EPOCH__"
	// TokenServicePath is the repo-relative service path, written to the
	// `build.benjamin-borbe.de/service` annotation. An annotation rather than a
	// label because it may contain "/" and a label value may not.
	TokenServicePath = "__SERVICE_PATH__"
	// TokenContextPath is the BuildKit `--local context=`.
	TokenContextPath = "__CONTEXT_PATH__"
	// TokenDockerfilePath is the BuildKit `--local dockerfile=`.
	TokenDockerfilePath = "__DOCKERFILE_PATH__"
	// TokenNodeSelector carries ONLY a hostname, never a whole mapping: the
	// template line is flow-style `nodeSelector: {kubernetes.io/hostname:
	// __NODE_SELECTOR__}`, and the block style is invalid YAML.
	//
	// Empty means "no selector at all", which is a line deletion rather than an
	// empty substitution — rendering "" into the flow map yields
	// `{kubernetes.io/hostname: }`, which is not a node selector.
	TokenNodeSelector = "__NODE_SELECTOR__"
)

// nodeSelectorLinePattern matches the whole line carrying the node-selector
// token, so an empty value can remove it rather than leave a broken map.
var nodeSelectorLinePattern = regexp.MustCompile(`(?m)^.*__NODE_SELECTOR__.*\n?`)

// DefaultDockerfile is the Dockerfile filename assumed when a repo's build
// config does not name one.
const DefaultDockerfile = "Dockerfile"

// ErrUnrenderedToken reports that a token survived substitution.
//
// Creating a Job in this state is worse than not creating one: the pod would
// run `git clone __REPO_URL__` and push `__IMAGE__:__TAG__`, failing minutes
// later with an error that reads like a build bug rather than a contract bug.
// The dispatcher skips on this and records reason=unrendered_token.
var ErrUnrenderedToken = stderrors.New("build job manifest has an unrendered token")

// unrenderedTokenPattern matches any surviving `__TOKEN__` placeholder.
var unrenderedTokenPattern = regexp.MustCompile(`__[A-Z0-9_]+__`)

// RenderParams carries every value the canonical template needs.
//
// All thirteen tokens are supplied here rather than read out of the manifest, so
// a missing value is a compile-time-shaped omission in the caller instead of a
// literal token in a Job spec.
type RenderParams struct {
	// JobName is the unique, DNS-safe Job name for this dispatch.
	JobName string
	// RepoURL is the git URL to clone.
	RepoURL string
	// Ref is the git ref to build, e.g. "refs/tags/v1.2.3".
	Ref string
	// Tag is the image tag, e.g. "v1.2.3".
	Tag string
	// Dockerfile is the Dockerfile filename. Empty means DefaultDockerfile.
	Dockerfile string
	// Image is the target image repository, without tag.
	Image string
	// Push controls whether the build pushes its result.
	Push bool
	// PushSecret names the dockerconfigjson Secret mounted at /docker-config.
	PushSecret string
	// DeadlineSeconds is the Job's activeDeadlineSeconds. Zero leaves the
	// manifest's own value untouched.
	DeadlineSeconds int
	// SourceDateEpoch is the epoch seconds of the commit Ref resolves to.
	SourceDateEpoch int64
	// ServicePath is the repo-relative service path. Callers should pass the
	// owner-qualified form: the template defaults it to the image basename,
	// which makes two repos sharing a basename indistinguishable.
	ServicePath string
	// ContextPath is the BuildKit context local. Empty derives it from
	// ServicePath.
	ContextPath string
	// DockerfilePath is the BuildKit dockerfile local. Empty mirrors ContextPath
	// so the plain "Dockerfile" name stays valid.
	DockerfilePath string
	// NodeSelector is a hostname to pin the build to. Empty removes the
	// nodeSelector line entirely — see TokenNodeSelector.
	NodeSelector string
}

// RenderJob substitutes the canonical tokens into a raw build-job manifest and
// decodes it into a Job.
//
// The rendered text is scanned for surviving tokens before it is decoded: a
// manifest that asks for a value this dispatcher cannot supply is a skip, not a
// Job that fails later.
func RenderJob(ctx context.Context, raw []byte, p RenderParams) (batchv1.Job, error) {
	var job batchv1.Job

	if err := p.validate(ctx); err != nil {
		return job, err
	}

	// An empty node selector deletes its line rather than substituting "": the
	// template uses a flow-style map, and `{kubernetes.io/hostname: }` parses as
	// a broken selector instead of "no selector".
	if p.NodeSelector == "" {
		raw = nodeSelectorLinePattern.ReplaceAll(raw, nil)
	}

	rendered := strings.NewReplacer(p.replacements()...).Replace(string(raw))

	if leftovers := unrenderedTokenPattern.FindAllString(rendered, -1); len(leftovers) > 0 {
		return job, errors.Wrapf(
			ctx, ErrUnrenderedToken,
			"render build job manifest service=%s tag=%s unrendered=%s",
			p.ServicePath, p.Tag, strings.Join(uniqueStrings(leftovers), ","),
		)
	}

	if err := yaml.Unmarshal([]byte(rendered), &job); err != nil {
		return job, errors.Wrapf(
			ctx, err, "parse build job manifest service=%s tag=%s", p.ServicePath, p.Tag,
		)
	}

	if job.Namespace == "" {
		job.Namespace = DefaultBuildNamespace
	}
	return job, nil
}

// validate rejects a params set that would render a Job nobody can trace back to
// a release. The epoch is checked explicitly because a zero value passes every
// textual substitution and then fails inside the build container.
func (p RenderParams) validate(ctx context.Context) error {
	if p.JobName == "" {
		return errors.Errorf(ctx, "render build job manifest: missing job name")
	}
	if p.RepoURL == "" {
		return errors.Errorf(ctx, "render build job manifest: missing repo url")
	}
	if p.Ref == "" {
		return errors.Errorf(ctx, "render build job manifest: missing ref")
	}
	if p.Tag == "" {
		return errors.Errorf(ctx, "render build job manifest: missing tag")
	}
	if p.Image == "" {
		return errors.Errorf(ctx, "render build job manifest: missing image")
	}
	if p.SourceDateEpoch <= 0 {
		return errors.Errorf(
			ctx,
			"render build job manifest: missing source date epoch for tag=%s "+
				"(the build container rejects a commit whose epoch differs)",
			p.Tag,
		)
	}
	return nil
}

// replacements returns the substitution pairs in the order the template
// documents them.
func (p RenderParams) replacements() []string {
	dockerfile := p.Dockerfile
	if dockerfile == "" {
		dockerfile = DefaultDockerfile
	}
	contextPath := p.ContextPath
	if contextPath == "" {
		contextPath = contextRoot(p.ServicePath)
	}
	dockerfilePath := p.DockerfilePath
	if dockerfilePath == "" {
		dockerfilePath = contextPath
	}

	return []string{
		TokenJobName, p.JobName,
		TokenRepoURL, p.RepoURL,
		TokenRef, p.Ref,
		TokenTag, p.Tag,
		TokenDockerfile, dockerfile,
		TokenImage, p.Image,
		TokenPush, strconv.FormatBool(p.Push),
		TokenPushSecret, p.PushSecret,
		TokenDeadlineSeconds, strconv.Itoa(p.DeadlineSeconds),
		TokenSourceDateEpoch, strconv.FormatInt(p.SourceDateEpoch, 10),
		TokenServicePath, p.ServicePath,
		TokenContextPath, contextPath,
		TokenDockerfilePath, dockerfilePath,
		TokenNodeSelector, p.NodeSelector,
	}
}

// contextRoot maps a service path onto the BuildKit context local. A
// single-service repo builds from /src; a monorepo service from /src/<subdir>.
func contextRoot(servicePath string) string {
	// "owner/name" is a single-service repo: the whole checkout is the context.
	// Anything deeper is a service inside a monorepo, and the context is its
	// path below the repo root.
	parts := strings.Split(strings.Trim(servicePath, "/"), "/")
	if len(parts) <= 2 {
		return "/src"
	}
	return "/src/" + strings.Join(parts[2:], "/")
}

// ImageBase returns the last path segment of s, e.g. "go-skeleton" for
// "bborbe/go-skeleton".
func ImageBase(s string) string {
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// uniqueStrings removes duplicates while preserving first-seen order, so an
// error naming three distinct leftover tokens does not repeat one four times.
func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// NewJobName derives a unique, DNS-safe Job name for one dispatch.
//
// Unique per run, deliberately: the canonical template requires it, because a
// tag-derived name would collide on a second build of the same tag and a
// finished Job blocks a same-name re-create for its whole TTL. Idempotency is
// the pre-create lookup in the dispatcher, not the name — the name only has to
// make a legitimate re-run possible.
func NewJobName(repo, tag string) string {
	return dnsLabel(
		"build-" + ImageBase(repo) + "-" + tag + "-" +
			nowUTC().Format("20060102-150405") + "-" + randomSuffix(),
	)
}

// nowUTC is a package variable so tests can pin the timestamp component.
var nowUTC = func() time.Time { return time.Now().UTC() }

// randomSuffix returns four lowercase alphanumeric characters. A collision only
// matters within the same second, where Create fails with AlreadyExists and the
// dispatcher absorbs it.
func randomSuffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "0000"
	}
	out := make([]byte, 4)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

// dnsLabel lowercases and reduces s to a DNS-1123 label (max 63 chars), the
// shape Kubernetes requires for object names. Tag characters that are legal in
// git but not in a label ("+", "_") collapse to "-".
func dnsLabel(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}
