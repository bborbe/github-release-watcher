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

// Credentials are the registry credentials the build itself will push with.
//
// The pre-flight asks the same registry the Job will push to, so it must ask as
// the same principal. An anonymous probe tests a different request than the
// build makes: Docker Hub answers an anonymous token request from a shared
// cluster egress with 500 while the authenticated one succeeds, which reads as
// "registry unreachable" and skips a build that would have worked.
type Credentials struct {
	Username string
	Password string
}

// IsZero reports whether no credentials are configured. A zero value means
// "probe anonymously", which is the behaviour every caller had before
// credentials were threaded through.
func (c Credentials) IsZero() bool {
	return c.Username == "" && c.Password == ""
}

// String deliberately does not render the secret, so a Credentials value can
// never leak through a %v or %s in an error or log line.
func (c Credentials) String() string {
	if c.IsZero() {
		return "credentials(anonymous)"
	}
	return "credentials(user=" + c.Username + ", password=<redacted>)"
}

// GoString matches String so %#v cannot render the password either.
func (c Credentials) GoString() string {
	return c.String()
}

type registryImageChecker struct {
	client *http.Client
}

func (c *registryImageChecker) Exists(
	ctx context.Context,
	imageRef string,
	creds Credentials,
) (bool, error) {
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
		token, err := c.dockerHubToken(ctx, ref.Repository, creds)
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else if !creds.IsZero() {
		// Other registries authenticate the manifest request directly; a
		// private in-house registry answers 401 to an anonymous probe even
		// when the image is there, which the fail-closed rule would read as
		// "cannot tell" and skip a build that would have worked.
		req.SetBasicAuth(creds.Username, creds.Password)
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

// dockerHubToken fetches a pull token for a docker.io repository.
//
// Credentials, when present, are sent as basic auth on the token request — that
// is what makes the probe ask as the same principal the build pushes as. The
// anonymous form is kept because a repo with no configured secret must still be
// able to probe a public image.
func (c *registryImageChecker) dockerHubToken(
	ctx context.Context,
	repository string,
	creds Credentials,
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
	if !creds.IsZero() {
		req.SetBasicAuth(creds.Username, creds.Password)
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
