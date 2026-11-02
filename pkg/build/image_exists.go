// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// dockerHubHost is the API host that serves docker.io. It is NOT "docker.io":
// the v2 registry answers on registry-1.docker.io, and docker.io itself 404s.
const dockerHubHost = "registry-1.docker.io"

// dockerHubAuthURL issues the anonymous pull token docker.io requires before
// it will answer a manifest request.
const dockerHubAuthURL = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:"

// manifestAccept lists the manifest media types a registry may return. Without
// them some registries answer 404 for an image that exists but is only
// published as an OCI index.
var manifestAccept = strings.Join([]string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
}, ", ")

// ImageRef is a parsed container image reference.
type ImageRef struct {
	// Registry is the registry host, e.g. "registry-1.docker.io".
	Registry string
	// Repository is the path within the registry, e.g. "bborbe/go-skeleton".
	Repository string
	// Tag is the tag, defaulting to "latest" when the reference carried none.
	Tag string
}

// String renders the reference back to "registry/repository:tag".
func (r ImageRef) String() string {
	return r.Registry + "/" + r.Repository + ":" + r.Tag
}

// ParseImageRef splits an image reference into registry, repository and tag.
//
// It follows the distribution reference rules the kubelet uses: the first path
// component is a registry host only when it contains a "." or ":" or is
// "localhost"; a single-component name means an official docker.io image and
// gets the "library/" prefix.
func ParseImageRef(ctx context.Context, image string) (ImageRef, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return ImageRef{}, errors.Errorf(ctx, "empty image reference")
	}
	// A digest reference ("@sha256:…") names immutable content, never a tag we
	// would build, so it is not a reference this checker can answer for.
	if strings.Contains(image, "@") {
		return ImageRef{}, errors.Errorf(
			ctx,
			"image reference %q carries a digest, not a tag",
			image,
		)
	}

	host := "docker.io"
	rest := image
	if first, after, found := strings.Cut(image, "/"); found {
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			host = first
			rest = after
		}
	}

	tag := "latest"
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		tag = rest[idx+1:]
		rest = rest[:idx]
	}
	if rest == "" || tag == "" {
		return ImageRef{}, errors.Errorf(ctx, "malformed image reference %q", image)
	}
	if host == "docker.io" {
		host = dockerHubHost
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
	}
	return ImageRef{Registry: host, Repository: rest, Tag: tag}, nil
}

// NewRegistryImageChecker returns an ImageChecker that asks the registry's v2
// manifest API whether the reference is already published.
//
// Every failure other than a clean 404 is returned as an error, which is what
// makes the dispatcher fail closed. A 401/403 is an error, not a "not found":
// an unauthenticated private image must not read as "absent, build it".
func NewRegistryImageChecker(client *http.Client) ImageChecker {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &registryImageChecker{client: client}
}

type registryImageChecker struct {
	client *http.Client
}

func (c *registryImageChecker) Exists(ctx context.Context, imageRef string) (bool, error) {
	ref, err := ParseImageRef(ctx, imageRef)
	if err != nil {
		return false, errors.Wrapf(ctx, err, "parse image reference %q", imageRef)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodHead,
		"https://"+ref.Registry+"/v2/"+ref.Repository+"/manifests/"+ref.Tag,
		nil,
	)
	if err != nil {
		return false, errors.Wrapf(ctx, err, "build manifest request %s", ref)
	}
	req.Header.Set("Accept", manifestAccept)

	if ref.Registry == dockerHubHost {
		token, err := c.dockerHubToken(ctx, ref.Repository)
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, errors.Wrapf(ctx, err, "manifest request %s", ref)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			glog.V(4).Infof("close manifest response body: %v", cerr)
		}
	}()
	// Drain so the connection is reusable; a manifest body is small.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, errors.Errorf(
			ctx,
			"manifest request %s returned %d",
			ref,
			resp.StatusCode,
		)
	}
}

// dockerHubToken fetches an anonymous pull token for a docker.io repository.
func (c *registryImageChecker) dockerHubToken(
	ctx context.Context,
	repository string,
) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		dockerHubAuthURL+url.QueryEscape(repository)+":pull",
		nil,
	)
	if err != nil {
		return "", errors.Wrapf(ctx, err, "build docker hub token request")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", errors.Wrapf(ctx, err, "docker hub token request")
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			glog.V(4).Infof("close token response body: %v", cerr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf(ctx, "docker hub token request returned %d", resp.StatusCode)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", errors.Wrapf(ctx, err, "decode docker hub token response")
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", errors.Errorf(ctx, "docker hub token response carried no token")
}
