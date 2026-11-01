// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	stderrors "errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	gogithub "github.com/google/go-github/v84/github"

	"github.com/bborbe/maintainer/maintainerconfig"
)

// ErrRateLimited is returned when the GitHub API responds with a rate-limit
// or abuse-rate-limit error.
var ErrRateLimited = stderrors.New("github rate limited")

//counterfeiter:generate -o ../mocks/github_client.go --fake-name GitHubClient . GitHubClient

// GitHubClient is the upstream-source surface for the release watcher.
// All methods are scoped to a single owner; the watcher iterates per-owner.
//
// Reference: watcher/github-pr/pkg/githubclient.go (uses SearchPRs + GetPRDetails);
// watcher/github-build/pkg/githubclient.go (uses ListWorkflowRuns + GetJobInfoForRun).
type GitHubClient interface {
	// ListRepos returns the non-archived repositories owned by owner that the
	// authenticated GitHub App installation can access — public AND private.
	// Forks are INCLUDED here (Repo.Fork carries the flag); whether a fork is
	// actually eligible for auto-release is decided downstream by the
	// filter.NewForkFilter trust gate on `.maintainer.yaml: release.allowFork`,
	// not by this listing step. Dropping forks at listing time (the pre-v0.48.0
	// behaviour) meant a fork with autoRelease: true never released and never
	// logged why — see filter.NewForkFilter doc for the incident.
	// It enumerates the installation grant via
	// GET /installation/repositories (Apps.ListRepos), NOT GET /users/{u}/repos:
	// the latter silently omits private repos under an installation token (no
	// error, no filter drop), which is why private auto-release repos never fired.
	// Results are filtered to owner (an installation is scoped to one account, so
	// this is defensive). Pagination is internal; the returned slice is the full set.
	ListRepos(ctx context.Context, owner string) ([]Repo, error)

	// GetMasterSHA returns the full HEAD SHA of repo's default branch.
	GetMasterSHA(ctx context.Context, repo Repo) (string, error)

	// GetChangelogContent returns the raw bytes of CHANGELOG.md at HEAD of repo's
	// default branch. Returns (nil, nil) if the file does not exist (404).
	// Other errors propagate.
	GetChangelogContent(ctx context.Context, repo Repo) ([]byte, error)

	// GetLatestReleaseTag returns the tag name of the repo's latest GitHub
	// release, or "" when the repo has published none.
	//
	// This is the poll backstop's input: a missed tag-push webhook is
	// recovered by asking GitHub what the newest release is, rather than by
	// listing every tag (one API call instead of a paginated walk). The
	// backstop's cursor entry keeps the call from re-dispatching a tag it has
	// already handled.
	GetLatestReleaseTag(ctx context.Context, repo Repo) (string, error)

	// GetFileContent returns the raw bytes of path at ref. An empty ref means
	// the repo's default branch.
	//
	// Returns (nil, nil) when the file does not exist (404) — absence is a
	// normal answer for the optional files this watcher reads (`.maintainer.yaml`
	// is the common case: most repos have none).
	//
	// Returns ErrRateLimited on primary or abuse rate-limit responses, and a
	// wrapped error on every other failure including network errors, 5xx,
	// oversize files (>1 MiB) and base64 decode failures. A caller that cannot
	// tell "absent" from "unreachable" must not treat the error as absence —
	// the build dispatch path fails closed on exactly this distinction.
	GetFileContent(ctx context.Context, repo Repo, path, ref string) ([]byte, error)

	// GetMaintainerConfig returns the parsed `.maintainer.yaml` document at
	// HEAD of repo's default branch. The file is the trust gate for maintainer
	// bots; a repo without it is treated as "not opted in" (zero-value config,
	// nil error). This is the common case — the file is rare.
	//
	// Returns:
	//   - (parsed config, nil) on a valid YAML document (including empty input
	//     and documents with the `release:` key absent — both yield zero-value).
	//   - (zero-value maintainerconfig.MaintainerConfig, nil) on HTTP 404 (file absent).
	//   - (zero-value maintainerconfig.MaintainerConfig, ErrRateLimited) on primary or abuse
	//     rate-limit responses.
	//   - (zero-value maintainerconfig.MaintainerConfig, wrapped error) on every other failure
	//     including network errors, 5xx responses, oversize files (>1 MiB),
	//     base64 decode failures, and YAML parse failures. Malformed YAML
	//     must NOT be silently treated as `autoRelease: false`.
	//
	// The 1 MiB cap is enforced via the API-reported Size before decoding
	// (cheap upstream rejection). A post-decode re-check is not added because
	// base64 encoding can only inflate, never deflate — a Size under 1 MiB
	// cannot decode to over 1 MiB.
	GetMaintainerConfig(ctx context.Context, repo Repo) (maintainerconfig.MaintainerConfig, error)

	// RateLimitRemaining returns the requests remaining in the current primary
	// rate-limit window, as reported by the last API response's
	// X-RateLimit-Remaining header (go-github surfaces it via
	// client.RateLimit()). Zero until the first request populates it. Both
	// watchers share the same App installation token, so either watcher's
	// reading reflects the shared 12,500/hour budget — the metric built from
	// this is the alert surface that catches quota exhaustion BEFORE the
	// fleet-wide 403 stall (2026-08-23 incident).
	RateLimitRemaining() int
}

// NewGitHubClient returns the production GitHubClient backed by the given HTTP client
// (typically authenticated via GitHub App installation token). The client's
// transport is wrapped to observe X-RateLimit-Remaining on every response, so
// RateLimitRemaining() reflects the shared App token's live quota.
func NewGitHubClient(httpClient *http.Client) GitHubClient {
	c := &githubClient{}
	inner := httpClient.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	httpClient.Transport = &rateCapturingTransport{
		inner: inner,
		set:   c.setRateRemaining,
		log:   logNon2xxV2,
	}
	c.client = gogithub.NewClient(httpClient)
	return c
}

type githubClient struct {
	client        *gogithub.Client
	mu            sync.Mutex
	rateRemaining int
}

func (c *githubClient) setRateRemaining(n int) {
	c.mu.Lock()
	c.rateRemaining = n
	c.mu.Unlock()
}

func (c *githubClient) RateLimitRemaining() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rateRemaining
}

// rateCapturingTransport wraps an http.RoundTripper and records the primary
// rate-limit remaining from each response's X-RateLimit-Remaining header. One
// capture point covers every API call (success, pagination, and error
// responses that carry the header), so the watcher can expose the shared
// token's remaining quota without touching each method.
//
// The same capture point logs one line per non-2xx response (status, rate-limit
// bucket, remaining, Retry-After, body snippet) via log — the only record of
// whether a 429/403 is primary-quota exhaustion or secondary rate limiting.
// go-github does not log at its HTTP boundary, so without this line that
// evidence is lost. A nil log disables it.
type rateCapturingTransport struct {
	inner http.RoundTripper
	set   func(int)
	log   func(string)
}

func (t *rateCapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	t.captureRemaining(resp)
	if t.log != nil {
		logNon2xxResponse(req, resp, t.log)
	}
	return resp, err
}

// captureRemaining records the response's remaining quota from the
// X-RateLimit-Remaining header into the gauge.
func (t *rateCapturingTransport) captureRemaining(resp *http.Response) {
	if v := resp.Header.Get("X-RateLimit-Remaining"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			t.set(n)
		}
	}
}

// isRateLimitError reports whether err is a GitHub API rate-limit signal
// (primary or secondary/abuse). Used by every API-surface method to map
// upstream rate-limit responses to ErrRateLimited so callers can abort the
// cycle uniformly.
func isRateLimitError(err error) bool {
	var rl *gogithub.RateLimitError
	var arl *gogithub.AbuseRateLimitError
	return stderrors.As(err, &rl) || stderrors.As(err, &arl)
}

func (c *githubClient) ListRepos(ctx context.Context, owner string) ([]Repo, error) {
	repos := make([]Repo, 0, 32)
	var total, private int
	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		result, resp, err := c.client.Apps.ListRepos(ctx, opts)
		if err != nil {
			return nil, c.wrapRateLimitErr(
				ctx, err, "list installation repos for %s page=%d", owner, opts.Page,
			)
		}
		for _, repo := range result.Repositories {
			total++
			if repo.GetPrivate() {
				private++
			}
		}
		repos = append(repos, mapGitHubRepos(result.Repositories, owner)...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	var forks int
	for _, repo := range repos {
		if repo.Fork {
			forks++
		}
	}
	// Per-poll listing count so a silent shrink (e.g. an installation-scope change
	// dropping repos) is observable in logs even before it drops a release task.
	// forks= is now included in in_scope (as of the fork trust-gate change) —
	// eligibility for those is decided downstream by filter.NewForkFilter, see
	// its skip-reason log line for the per-repo verdict.
	glog.Infof(
		"github-release-watcher listed installation repos owner=%s total=%d private=%d forks=%d in_scope=%d",
		owner,
		total,
		private,
		forks,
		len(repos),
	)
	return repos, nil
}

// mapGitHubRepos maps an API repo page into our domain Repo slice, keeping only
// repos owned by owner and dropping archived and empty-name entries. Forks are
// KEPT (with Fork: true set) — the fork-vs-not-fork decision belongs to
// filter.NewForkFilter downstream, not to this listing step; see that filter's
// doc comment for why forks used to be dropped here and what broke.
// The owner filter is defensive: a GitHub App installation is scoped to a single
// account, so every returned repo already shares owner — but filtering keeps the
// per-owner contract honest if that ever changes.
func mapGitHubRepos(repos []*gogithub.Repository, owner string) []Repo {
	var result []Repo
	for _, repo := range repos {
		if repo.GetArchived() {
			continue
		}
		if repo.GetOwner().GetLogin() != owner {
			continue
		}
		name := repo.GetName()
		if name == "" {
			continue
		}
		result = append(result, Repo{
			Owner:         repo.GetOwner().GetLogin(),
			Name:          name,
			DefaultBranch: repo.GetDefaultBranch(),
			Fork:          repo.GetFork(),
		})
	}
	return result
}

func (c *githubClient) wrapRateLimitErr(
	ctx context.Context,
	err error,
	msg string,
	args ...interface{},
) error {
	if isRateLimitError(err) {
		return ErrRateLimited
	}
	return errors.Wrapf(ctx, err, msg, args...)
}

func (c *githubClient) GetMasterSHA(ctx context.Context, repo Repo) (string, error) {
	if repo.DefaultBranch == "" {
		// Scoped-poll path (webhook release-check): the Repo is built from the
		// scope alone, so DefaultBranch is unknown — resolve it once before
		// fetching the branch SHA. Without this the scoped check drops every
		// repo and the webhook-triggered release never fires (the periodic
		// scan was the only working path).
		ghRepo, _, err := c.client.Repositories.Get(ctx, repo.Owner, repo.Name)
		if err != nil {
			return "", c.wrapRateLimitErr(
				ctx,
				err,
				"get repo %s/%s for default branch",
				repo.Owner,
				repo.Name,
			)
		}
		repo.DefaultBranch = ghRepo.GetDefaultBranch()
	}
	if repo.DefaultBranch == "" {
		return "", errors.Errorf(
			ctx,
			"repo %s/%s has empty DefaultBranch — cannot fetch HEAD SHA",
			repo.Owner,
			repo.Name,
		)
	}
	branch, _, err := c.client.Repositories.GetBranch(
		ctx,
		repo.Owner,
		repo.Name,
		repo.DefaultBranch,
		1, // follow one redirect — GitHub returns 301 for renamed default branches
	)
	if err != nil {
		return "", c.wrapRateLimitErr(
			ctx,
			err,
			"get branch %s/%s@%s",
			repo.Owner,
			repo.Name,
			repo.DefaultBranch,
		)
	}
	return branch.GetCommit().GetSHA(), nil
}

func (c *githubClient) GetLatestReleaseTag(ctx context.Context, repo Repo) (string, error) {
	release, _, err := c.client.Repositories.GetLatestRelease(ctx, repo.Owner, repo.Name)
	if err != nil {
		var ghErr *gogithub.ErrorResponse
		if stderrors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
			// No releases published (or none tagged) — nothing to back-fill.
			return "", nil
		}
		if isRateLimitError(err) {
			return "", ErrRateLimited
		}
		return "", errors.Wrapf(
			ctx, err, "get latest release %s/%s", repo.Owner, repo.Name,
		)
	}
	return release.GetTagName(), nil
}

// fileContent fetches one file's decoded bytes at ref, mapping 404 to
// (nil, nil) and rate limits to ErrRateLimited.
//
// Every file read goes through here so the size cap, the 404 rule and the
// rate-limit mapping cannot drift between callers: the build dispatch path
// fails closed on the difference between "absent" and "could not read", and
// that difference is decided exactly once, here.
func (c *githubClient) fileContent(
	ctx context.Context,
	repo Repo,
	path, ref string,
) ([]byte, error) {
	opts := &gogithub.RepositoryContentGetOptions{Ref: ref}
	fileContent, _, _, err := c.client.Repositories.GetContents(
		ctx,
		repo.Owner,
		repo.Name,
		path,
		opts,
	)
	if err != nil {
		var ghErr *gogithub.ErrorResponse
		if stderrors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		if isRateLimitError(err) {
			return nil, ErrRateLimited
		}
		return nil, errors.Wrapf(
			ctx,
			err,
			"get %s %s/%s@%s",
			path,
			repo.Owner,
			repo.Name,
			ref,
		)
	}
	if fileContent == nil {
		return nil, nil
	}
	if fileContent.GetSize() > 1024*1024 {
		return nil, errors.Errorf(
			ctx,
			"%s %s/%s too large: %d bytes (max 1 MiB)",
			path,
			repo.Owner,
			repo.Name,
			fileContent.GetSize(),
		)
	}
	decoded, err := fileContent.GetContent()
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "decode %s %s/%s", path, repo.Owner, repo.Name)
	}
	// Enforce the limit on actual decoded content — API-reported Size is upstream metadata.
	if len(decoded) > 1024*1024 {
		return nil, errors.Errorf(
			ctx,
			"%s %s/%s decoded content too large: %d bytes (max 1 MiB)",
			path,
			repo.Owner,
			repo.Name,
			len(decoded),
		)
	}
	return []byte(decoded), nil
}

func (c *githubClient) GetFileContent(
	ctx context.Context,
	repo Repo,
	path, ref string,
) ([]byte, error) {
	if ref == "" {
		ref = repo.DefaultBranch
	}
	return c.fileContent(ctx, repo, path, ref)
}

func (c *githubClient) GetChangelogContent(ctx context.Context, repo Repo) ([]byte, error) {
	return c.fileContent(ctx, repo, "CHANGELOG.md", repo.DefaultBranch)
}

func (c *githubClient) GetMaintainerConfig(
	ctx context.Context,
	repo Repo,
) (maintainerconfig.MaintainerConfig, error) {
	decoded, err := c.fileContent(ctx, repo, ".maintainer.yaml", repo.DefaultBranch)
	if err != nil {
		return maintainerconfig.MaintainerConfig{}, err
	}
	if decoded == nil {
		return maintainerconfig.MaintainerConfig{}, nil
	}
	cfg, err := maintainerconfig.Parse(ctx, decoded)
	if err != nil {
		return maintainerconfig.MaintainerConfig{}, errors.Wrapf(
			ctx,
			err,
			"parse .maintainer.yaml %s/%s",
			repo.Owner,
			repo.Name,
		)
	}
	return cfg, nil
}
