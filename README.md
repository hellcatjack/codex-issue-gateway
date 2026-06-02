# codex-issue-gateway

Self-hosted GitHub Issue automation gateway for running non-interactive Codex development jobs across configured repositories.

## What It Does

`codex-issue-gateway` turns trusted GitHub Issue commands into local Codex development jobs:

1. GitHub sends an issue or issue comment webhook.
2. The gateway verifies the webhook, parses `/codex ...` commands, and enqueues an authorized job.
3. The worker fetches the current issue context from GitHub, not just the webhook payload.
4. Codex runs in a per-job workspace with a per-job `CODEX_HOME`.
5. The gateway runs configured repository tests, evaluates changed files, commits changes, pushes a work branch, and opens a Pull Request.
6. Public issue and PR feedback is filtered before publication.

The system is repository-configured. `funland/foliospace-Library` and `hellcatjack/epubReader` are local test targets, not hardcoded product assumptions.

## Supported Commands

- `/codex plan`: build a Markdown implementation plan without runtime code changes.
- `/codex implement`: execute the approved work, verify it, and create a PR when files changed.
- `/codex fix`: execute a follow-up fix job with the same safety rules.

Commands are recognized only on standalone `/codex ...` lines. In the current build, only the configured command actor policy accepts `/codex` instructions from `hellcatjack`, while issue context passed to Codex is limited to collaborator-owned text.

## Safety Defaults

- GitHub webhook HMAC verification is required.
- Public GitHub comments and PR text are sanitized through a centralized fail-closed gate.
- Codex agent feedback is published only after structured extraction and line-level filtering; raw logs, local absolute paths, credentials, and internal workspace details are not posted.
- Commands are accepted only from standalone `/codex ...` lines.
- `/codex` commands are accepted only from the configured command actor policy (`hellcatjack` in the current build).
- Worker prompts are rebuilt from current GitHub issue state and include only owner/member/collaborator issue text.
- GitHub-hosted images embedded in trusted issue text are downloaded with host, content-type, size, and redirect checks, then passed to Codex as `--image` attachments; arbitrary external image URLs are skipped.
- Execution commands are non-interactive and never wait for user input.
- Worker subprocesses run with an allowlisted environment instead of inheriting host secrets.
- Playwright browser caches can be configured at worker level and injected as `PLAYWRIGHT_BROWSERS_PATH` without exposing broader host environment variables.
- Worker expiry is based on no activity, not normal elapsed job duration.
- Jobs run in isolated directories with per-job `CODEX_HOME`; when configured, only `auth.json` is copied from the Codex auth source directory.
- Repository policies define allowed actors, branches, tests, deny paths, and review-required paths.

## Worker Execution

The HTTP gateway can run without a worker and only enqueue jobs. When `worker.enabled` is true, the process starts a background worker loop that leases queued jobs, fetches current GitHub issue context, runs Codex in an isolated job workspace, runs configured tests, checks changed files, commits, pushes the work branch, and opens a pull request.

Production worker execution requires a configured GitHub App private key and installation id. Local development may fall back to the fake GitHub client for intake testing, but fake mode cannot complete real GitHub issue execution.

If Codex verifies that the requested behavior already exists and no files changed, the job completes successfully without creating an empty PR.

When `worker.playwright.enabled` is true, the worker injects `PLAYWRIGHT_BROWSERS_PATH` into Codex, setup, and test commands, then preinstalls configured browsers after project dependencies are restored.

## Documentation

- [Local development](docs/local-development.md)
- [Operations and configuration](docs/operations.md)
- [Original requirements](todos.md)
- [Design spec](docs/superpowers/specs/2026-06-01-multi-repo-codex-issue-gateway-design.md)
- [Implementation plan](docs/superpowers/plans/2026-06-01-multi-repo-codex-issue-gateway.md)
