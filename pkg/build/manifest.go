// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"
	"strings"

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

// Placeholders the build manifest may use. Substitution is a plain textual
// replace, so a manifest may also hardcode everything and use none of them.
const (
	// TagPlaceholder is replaced with the bare tag, e.g. "v1.2.3".
	TagPlaceholder = "${TAG}"
	// TagBarePlaceholder is replaced with the tag minus a leading "v", e.g. "1.2.3".
	TagBarePlaceholder = "${TAG_BARE}"
	// RepoPlaceholder is replaced with the repo key, e.g. "bborbe/go-skeleton".
	RepoPlaceholder = "${REPO}"
)

// RenderJob substitutes the tag/repo placeholders into a raw build-job manifest
// and decodes it into a Job.
//
// The Job's name and namespace are then forced by the dispatcher's own rules
// rather than trusted from the manifest: the name is what makes dispatch
// idempotent across the webhook and poll paths, and it must name the tag so an
// operator reading `kubectl get jobs -n build` can trace a Job back to a
// release.
func RenderJob(ctx context.Context, raw []byte, req Request) (batchv1.Job, error) {
	rendered := strings.NewReplacer(
		TagPlaceholder, req.Tag,
		TagBarePlaceholder, strings.TrimPrefix(req.Tag, "v"),
		RepoPlaceholder, req.Repo,
	).Replace(string(raw))

	var job batchv1.Job
	if err := yaml.Unmarshal([]byte(rendered), &job); err != nil {
		return job, errors.Wrapf(
			ctx, err, "parse build job manifest repo=%s tag=%s", req.Repo, req.Tag,
		)
	}

	job.Name = JobName(req.Repo, req.Tag)
	if job.Namespace == "" {
		job.Namespace = DefaultBuildNamespace
	}
	return job, nil
}

// JobName derives the deterministic Job name for a (repo, tag) pair.
//
// Determinism is the idempotency mechanism: the webhook dispatch and the poll
// backstop compute the same name for the same tag, so the second one collides
// on Create and is absorbed as a no-op instead of building twice.
func JobName(repo, tag string) string {
	base := repo
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		base = repo[idx+1:]
	}
	return dnsLabel("build-" + base + "-" + tag)
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
