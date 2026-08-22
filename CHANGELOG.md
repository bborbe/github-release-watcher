# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.4.1

- chore: update Go to 1.27.0 and github.com/bborbe/agent to v0.82.1, github.com/bborbe/cqrs to v0.6.8, github.com/bborbe/errors to v1.5.20, github.com/bborbe/http to v1.26.24, github.com/bborbe/kafka to v1.25.9, github.com/bborbe/kv to v1.21.11, github.com/bborbe/log to v1.6.23, github.com/bborbe/maintainer to v0.49.2, github.com/bborbe/parse to v1.10.21, github.com/bborbe/run to v1.9.37, github.com/bborbe/sentry to v1.9.26, github.com/bborbe/service to v1.10.9, github.com/bborbe/time to v1.27.10

## v0.4.0

- feat: GitHub webhook receiver for instant release-check trigger — POST `/webhook/github-release`, HMAC `X-Hub-Signature-256` verification, `push` events dispatch `TriggerReleaseCheckCommand` to Kafka (release check starts seconds after a merge instead of the 10-min poll); metrics `webhook_deliveries_total`, `webhook_signature_rejections_total`, `webhook_dispatch_latency_seconds`

## v0.3.4

- chore: Bump errcheck to v1.20.0 and golangci-lint to v2.13.1 for Go 1.27 support
## v0.3.3

- chore: update dependencies to clear CVEs/CGOs: CVE-2026-56864, CVE-2026-56865, GO-2026-6179, GO-2026-6180, GO-2026-5026, GO-2026-5972, GO-2026-6089, GO-2026-6090, GO-2026-6218

## v0.3.2

- chore: bump Go 1.26.5 → 1.26.6 and update dependencies to clear CVEs/CGOs: CVE-2026-56864, CVE-2026-56865, GO-2026-6179, GO-2026-6180, GO-2026-5026, GO-2026-5972, GO-2026-6089, GO-2026-6090, GO-2026-6218

## v0.3.1

- fix: move the fork check out of repo listing (`ListRepos`/`mapGitHubRepos`) and into the `TaskCreationFilter` chain as a new `filter.NewForkFilter` trust gate on `.maintainer.yaml: release.allowFork` (requires `github.com/bborbe/maintainer` v0.48.0). Previously forks were dropped silently at listing time, upstream of every filter — a fork with `autoRelease: true` never released and emitted no log line (found on `bborbe/tts-mcp`; cost ~40min to diagnose). Forks now enter the scan set (`Repo.Fork` carries the flag), pass the gate when `allowFork: true`, and are skipped with a `fork` reason (`Metrics.IncFilterSkipped("fork")` + a glog line naming the repo) otherwise — archived repos are still dropped at listing. The per-poll listing log now reports `forks=N` in addition to `total`/`private`/`in_scope`.

## v0.3.0

- fix: list repositories via `GET /installation/repositories` (`Apps.ListRepos`) instead of `GET /users/{u}/repos` (`Repositories.ListByUser`/`ListByOrg`). The user/org endpoints silently omit **private** repos under a GitHub App installation token — no error, no filter drop — so private repos with `autoRelease: true` (e.g. `bborbe/jira-task-creator`) never got a release task and had to be released by hand. The installation endpoint enumerates exactly the installation grant (public + private); results are still filtered to `OWNER`, archived, and forks.
- feat: log a per-poll installation-listing count (`total` / `private` / `in_scope`) so a silent listing shrink is observable in logs before it drops a release task.
- chore: bump `golang.org/x/text` v0.38.0 → v0.39.0 to clear advisory GO-2026-5970 (infinite loop on invalid input).

## v0.2.0

- feat: add optional `--target-vault` / `TARGET_VAULT` flag. When set, the watcher stamps `TargetVault` on every emitted `CreateTaskCommand`, so it routes to a controller whose `VAULT_NAME` matches verbatim. Empty (default) leaves `TargetVault` unset (`omitempty` → wire byte-compatible), preserving the controller's legacy default-vault fallback. Enables deployments whose work-vault is not the controller's hardcoded legacy default (e.g. the Seibert-Data `agent` vault). Threaded through both the poll watcher and the `run-once` command.
- chore: bump Go 1.26.4 → 1.26.5 (go.mod + Dockerfile) to clear stdlib advisory GO-2026-5856; ignore unmaintained-openpgp advisory GO-2026-5932 in `VULNCHECK_IGNORE` + `.trivyignore` (indirect, unreachable, no fix — same class as GO-2022-0470).

## v0.1.1

- refactor: import the shared library from its new root module path `github.com/bborbe/maintainer` (was `github.com/bborbe/maintainer/lib`) and bump to `@v0.45.0`. The maintainer repo flattened `lib/` to its root to match the `bborbe/agent` layout. No behavior change.

## v0.1.0

- Extracted from the `bborbe/maintainer` monorepo (`watcher/github-release`) into a standalone
  publish-only repository. Shared code now comes from the versioned
  `github.com/bborbe/maintainer/lib` module instead of a local `replace`. Builds and
  publishes `docker.io/bborbe/github-release-watcher:<version>` via `make buca`.
