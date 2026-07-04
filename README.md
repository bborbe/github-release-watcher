# maintainer-watcher-github-release

Polls the GitHub API for repositories with a non-empty `## Unreleased` block in `CHANGELOG.md` and publishes a `CreateTaskCommand` to Kafka per affected repo so the `agent/github-releaser` (future Phase 2 sibling spec) picks each up and cuts the release autonomously.

The watcher is the **producer** half of the pipeline. It never modifies the target repo — no commit, no tag, no push. The agent owns release execution.

## Links

Dev:
https://dev.quant.benjamin-borbe.de/admin/maintainer-watcher-github-release/setloglevel/3
https://dev.quant.benjamin-borbe.de/admin/maintainer-watcher-github-release/metrics
https://dev.quant.benjamin-borbe.de/admin/maintainer-watcher-github-release/trigger

Prod:
https://prod.quant.benjamin-borbe.de/admin/maintainer-watcher-github-release/setloglevel/3
https://prod.quant.benjamin-borbe.de/admin/maintainer-watcher-github-release/metrics
https://prod.quant.benjamin-borbe.de/admin/maintainer-watcher-github-release/trigger

## How It Works

On each poll cycle:

1. **List repos** under `OWNER` (non-archived, non-fork).
2. For each repo in scope (filtered against `REPO_ALLOWLIST`):
   - Fetch master HEAD SHA.
   - Fetch `CHANGELOG.md`; parse `## Unreleased` bullet count + first-section flag + latest version header.
   - Fetch `.maintainer.yaml` to read `release.autoRelease`; the watcher proceeds only when this is `true` (trust gate — repos without the file are skipped).
3. **Apply filter chain** (skip if ANY votes skip):
   - `RepoAllowlistFilter` — host-qualified scope filter
   - `EmptyUnreleasedFilter` — skip repos whose `## Unreleased` has zero bullets
   - `AutoReleaseFilter` — gate; passes only when `.maintainer.yaml: release.autoRelease: true`, skips every other shape (file absent, key absent, false)
   - `SHAUnchangedFilter` — skip if cursor already records this master HEAD (cursor-aware, composed per-cycle)
4. **Publish** `CreateTaskCommand` per non-skipped repo.
5. **Save cursor** at `/data/cursor.json` (per-repo `LastSeenMasterSHA`, atomic temp+rename).

Cycle abort (no cursor save) on GitHub 5xx, rate limit, or `ListRepos` failure — next cycle resumes from the same cursor.

## Task Contract

Per [[Agent Task File Contract]] — every emitted `CreateTaskCommand` carries this frontmatter shape:

```yaml
task_type: github-release
assignee: github-releaser-agent
phase: planning
status: in_progress
stage: dev|prod
task_identifier: <UUID5(owner, repo, head_sha)>
title: Release <owner>-<repo> <sha[:7]>
repo: owner/name
clone_url: git@github.com:owner/name.git
ref: <full HEAD SHA>
current_version: vX.Y.Z   # or v0.0.0 if no prior release
```

The `title` field uses a **dash** between owner+repo (not slash) — the controller's CreateCommand validator rejects `/` in titles.

Body is an operator-readable header only (title + version + HEAD + changelog URL + repo link). The downstream agent clones the repo at `ref` and reads CHANGELOG itself — never parses the body.

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `OWNER` | yes | — | GitHub owner / org to scan (e.g. `bborbe`) |
| `KAFKA_BROKERS` | yes | — | Comma-separated Kafka broker list |
| `STAGE` | yes | — | Deployment stage (`dev` or `prod`) |
| `APP_ID` | no | — | GitHub App ID (set all three of `APP_ID` + `INSTALLATION_ID` + `PEM_KEY` for App auth) |
| `INSTALLATION_ID` | no | — | GitHub App Installation ID |
| `PEM_KEY` | no | — | GitHub App private key (PEM content from k8s Secret envFrom — see `teamvault-conventions.md`) |
| `GH_TOKEN` | no | — | Legacy PAT fallback when App credentials are absent |
| `LISTEN` | no | `:9090` | HTTP listen address (`/healthz`, `/readiness`, `/metrics`) |
| `POLL_INTERVAL` | no | `10m` | Poll interval (Go duration string) |
| `REPO_ALLOWLIST` | no | — | Comma-separated host-qualified repo allowlist (`host/owner/repo`); empty = allow-all within `OWNER` |
| `CURSOR_PATH` | no | `/data/cursor.json` | Cursor persistence path (PVC mount) |
| `SENTRY_DSN` | no | — | Sentry DSN for error tracking |
| `SENTRY_PROXY` | no | — | HTTP proxy URL for Sentry transport |

### `REPO_ALLOWLIST` syntax

Entries are comma-separated. A leading `!` marks an exclusion. A target is allowed iff `(includes is empty OR any include matches) AND (no exclude matches)`; excludes always override includes.

| Entry shape | Example | Meaning |
|---|---|---|
| Literal include | `github.com/bborbe/maintainer` | Allow exactly this repo |
| Wildcard include | `github.com/bborbe/*` | Allow every repo under this owner |
| Literal exclude | `!github.com/bborbe/go-skeleton` | Reject exactly this repo (overrides any matching include) |
| Wildcard exclude | `!github.com/bborbe/*` | Reject every repo under this owner |

An allowlist consisting of only exclude entries is treated as allow-all-except: every target passes the include gate, and only the exclude gate filters. Example: `REPO_ALLOWLIST=!github.com/bborbe/go-skeleton` rejects go-skeleton and allows every other repo (including all other bborbe repos). To allow every bborbe repo except go-skeleton, write `github.com/bborbe/*,!github.com/bborbe/go-skeleton`.

## HTTP Endpoints

| Path | Method | Purpose |
|---|---|---|
| `/healthz` | GET | Liveness probe (always returns 200 OK) |
| `/readiness` | GET | Readiness probe (always returns 200 OK) |
| `/metrics` | GET | Prometheus metrics |

No `/check` or `/trigger` endpoint — release work is one-task-per-repo-per-master-SHA; operator-triggered single-repo runs go through `cmd/run-once` instead (see below).

## Metrics

| Metric | Cardinality | Purpose |
|---|---|---|
| `github_release_watcher_poll_cycle_total{result}` | `result=success\|github_error\|rate_limited` | Poll health |
| `github_release_watcher_published_total{status}` | `status=create\|skipped\|error` | Per-cycle task emission |
| `github_release_watcher_repos_scanned_total` | none | Sanity check on scope filter |
| `github_release_watcher_filter_skipped_total{reason}` | `reason=empty_unreleased\|auto_release\|sha_unchanged\|scope` | Filter chain visibility |

## Development

```bash
cd watcher/github-release
make test          # run unit tests
make generate      # regenerate counterfeiter mocks
make precommit     # format + lint + test + security checks
```

## Rung-1 Smoke Test (`cmd/run-once`)

Single Poll cycle against real dev Kafka, then exits. Use to verify the watcher↔controller↔vault chain without deploying.

```bash
cd watcher/github-release/cmd/run-once
make run-once REPO_ALLOWLIST=github.com/bborbe/<repo>
```

Authenticates via `gh auth token` (PAT mode); cursor at `/tmp/cursor.json`; defaults to the same dev Kafka brokers as the deployed StatefulSet. See `docs/verifying-specs.md` for the full rung-1/2/3 evidence procedure.

## Cursor Mechanism

The cursor at `/data/cursor.json` is a per-repo map:

```json
{
  "repos": {
    "bborbe/disk-status": {"last_seen_master_sha": "6893c206..."},
    "bborbe/lib-foo":     {"last_seen_master_sha": "deadbeef..."}
  }
}
```

`SHAUnchangedFilter` consults this on each poll — only emits a task when a repo's HEAD has advanced since the last successful publish. Re-publish at the same SHA is suppressed at the filter layer; deterministic UUID5 (`owner`, `repo`, `head_sha`) at the controller layer provides a second defence (controller dedup makes re-emit a no-op).

Atomic write: temp file + rename. Corrupt cursor refuses startup — see `pkg/cursor.go`.

## Relationship to github-releaser-agent

The watcher publishes `CreateTaskCommand` events on `agent-task-v1-request`. The controller materialises each into a vault task file at `<vault>/24 Tasks/Release <owner>-<repo> <sha[:7]>.md` with `assignee: github-releaser-agent`. Tasks at `phase: planning, status: in_progress` get picked up by the agent's Pattern B Job, which classifies the bump (patch/minor/major), rewrites the CHANGELOG, commits, tags, pushes — handling branch protection via PR + auto-merge fallback.

Until `agent/github-releaser` ships (future sibling spec), the slash-command pair `/github-release-repo` (Phase 1 prototype) consumes the same task contract and performs the release manually.

See [[GitHub Release Agent]] (vault goal) for the multi-phase plan and [[Watcher Writing Guide]] for the producer-side contract this watcher satisfies.

## License

BSD 2-Clause License. See [LICENSE](../../LICENSE).
