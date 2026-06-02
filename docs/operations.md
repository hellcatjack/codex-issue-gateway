# Operations and Configuration

This document describes how to run `codex-issue-gateway` as a real GitHub Issue automation system.

## Architecture

```text
GitHub Issue or Issue Comment
  -> GitHub webhook
  -> gateway HTTP server
  -> SQLite queue
  -> local worker
  -> Codex exec
  -> configured tests
  -> branch push and Pull Request
  -> sanitized GitHub issue feedback
```

The gateway and worker are in one binary. The HTTP server can accept and queue work without a worker, but real development execution requires `worker.enabled: true`.

## GitHub App

Create a GitHub App with these repository permissions:

- Metadata: read
- Issues: write
- Contents: write
- Pull requests: write

Subscribe to these events:

- Issues
- Issue comment
- Pull request

Install the App on each repository that should be allowed to use the gateway. The gateway uses the App ID, installation ID, private key file, and webhook secret from configuration.

Keep the private key and webhook secret outside the repository. The gateway reads them from local files at startup.

## Repository Configuration

Each `repos` entry is independent. This allows the same gateway to serve multiple repositories with different base branches, actor rules, test commands, and safety policy.

Important fields:

- `full_name`: GitHub repository, for example `owner/repo`.
- `clone_url`: source repository used for job workspaces.
- `local_fixture_path`: optional local source path for testing or fixture-driven development.
- `fork_push_remote`: remote target used to push Codex work branches.
- `base_branches`: allowed base branches for `/codex` commands.
- `protected_branches`: branches that should never be pushed directly.
- `allowed_actors`: explicit actor policy.
- `agent_setup_commands`: optional commands run before Codex starts.
- `test_commands`: commands the gateway runs after Codex completes.
- `deny_paths`: paths that cannot be changed by an automated job.
- `review_required_paths`: paths that can be flagged for extra review.
- `codex.auth_source_dir`: optional source for copying only `auth.json` into per-job `CODEX_HOME`.

`agent_setup_commands` should be used for local dependency caches and SDK setup. They run before Codex and help keep Codex from trying to install dependencies interactively or through blocked external networks.

## Issue Workflow

Only standalone commands are recognized:

```text
/codex plan
/codex implement
/codex fix
```

For every job, the worker fetches the current issue from GitHub before building the Codex prompt. It does not depend on webhook body text for execution context.

Issue text passed to Codex is restricted to trusted collaborator content. Command acceptance is narrower: in the current build, `/codex` commands are accepted only from the configured command actor policy.

Images embedded in trusted issue text are downloaded only from allowed GitHub attachment hosts, checked for redirects, content type, and size, then passed to Codex as image inputs. External image URLs are skipped.

## Worker Lifecycle

For each job, the worker:

1. Creates an isolated job workspace.
2. Prepares a per-job `CODEX_HOME`.
3. Clones or copies the configured repository.
4. Runs optional `agent_setup_commands`.
5. Fetches fresh issue context from GitHub.
6. Runs Codex with non-interactive execution rules.
7. Publishes safe screenshot artifacts when Codex creates them.
8. Runs configured gateway tests.
9. Publishes safe screenshot artifacts created by gateway test commands.
10. Evaluates changed files against deny and review policy.
11. Commits and pushes changes when files changed.
12. Creates a Pull Request.
13. Completes without a PR when no files changed.

No-change completion is intentional. It prevents empty PRs when the current base branch already satisfies the issue.

Implementation jobs self-repair inside the same workspace. When Codex exits early or a configured verification command fails, the gateway does not immediately publish an implementation failure. It builds a sanitized repair prompt from the prior attempt, keeps a broad safe excerpt of verification output, tells Codex to inspect local failure artifacts such as `test-results/`, `playwright-report/`, and `.codex-gateway-artifacts/screenshots/`, attaches any safe screenshot artifacts already published for the job, reruns Codex, and verifies again. This repeats up to `worker.implementation_repair_attempts` times, which defaults to `8`. If the repair budget is exhausted, the job moves to `waiting_human` with a plan-revision request instead of a failed implementation result.

## Public Feedback Safety

Public comments and PR bodies are filtered before publication. The filter removes lines that look like:

- credentials or token assignments
- GitHub tokens or OpenAI-style API keys
- private key material
- local absolute paths
- credential-bearing URLs

Raw Codex stdout and stderr are stored only in internal job artifacts. They are not served as public artifacts.

When a Codex process exits before producing an agent message, the gateway posts a safe process failure summary rather than raw logs.

## Timeout Model

The worker uses activity-based timeout handling. Long jobs are allowed when Codex or test commands continue producing activity. A separate absolute timeout remains as a backstop.

Phase-specific no-activity timeouts can be configured for:

- `planning`
- `implementing`
- `testing`
- `creating_pr`

This avoids killing legitimate long-running work solely because total elapsed time is high.

## Public Artifacts

Codex or gateway `test_commands` may stage browser-visible screenshots under:

```text
.codex-gateway-artifacts/screenshots/
```

The gateway validates file type, file size, symlinks, and suspicious names before copying safe images into the job artifact public directory. Public issue comments and PR bodies include image links only after this validation. This lets browser screenshots run outside the Codex sandbox as part of configured verification commands.

The staging directory is removed from the repository workspace after publication.

## Operational Checks

Run before deployment:

```bash
go test ./...
go build -o tmp/codex-issue-gateway ./cmd/codex-issue-gateway
```

Health endpoints:

```text
GET /healthz
GET /readyz
```

Webhook endpoint:

```text
POST /github/webhook
```

Use `readyz` to confirm the queue and GitHub client are available before routing GitHub webhooks to the service.
