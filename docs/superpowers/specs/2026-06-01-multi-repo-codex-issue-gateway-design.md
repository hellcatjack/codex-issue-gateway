# Multi-Repo Codex Issue Gateway Design

## Purpose

`codex-issue-gateway` is a self-hosted automation system that turns approved GitHub Issue commands into isolated local Codex development jobs. It must support multiple GitHub repositories through configuration, not repository-specific code paths.

The MVP scope advances through Phase 8 of `todos.md`: webhook intake, command parsing, authorization, SQLite queueing, GitHub App integration, worker isolation, Codex execution, test/diff policy, branch push, Pull Request creation, and Issue status comments.

## Language And Runtime Choice

The core service will be implemented in Go.

Go is the best fit because this system is a long-running security-sensitive daemon with HTTP webhook handling, SQLite persistence, worker scheduling, child process execution, cancellation, timeout control, and single-binary deployment needs. TypeScript has stronger GitHub client ergonomics but adds runtime and dependency surface for local process orchestration. Python would be fast for prototyping but weaker for typed long-term daemon maintenance. Rust is strong but adds implementation cost that is not justified for this MVP.

The project may later add a TypeScript web console, but the Phase 1-8 gateway and worker are Go.

## Product Scope

The gateway is not dedicated to `funland/foliospace-Library`.

It supports any configured repository installed under the GitHub App. `/app/devs/foliospace-Library` is only the first local integration fixture and will be used to verify the system against the public project `https://github.com/funland/foliospace-Library`.

Each repository gets its own policy:

- GitHub full name.
- Clone URL.
- Optional local fixture path for integration tests.
- Fork or controlled push remote.
- Allowed base branches and protected branch patterns.
- Allowed actors grouped by role.
- Required labels for implementation commands.
- Test command allowlist.
- Denylist and review-required file path rules.
- Codex timeout and sandbox options.
- Per-repo concurrency limits.

## Architecture

The MVP ships as one Go binary with clean internal package boundaries. Gateway and worker can run in the same process for MVP deployment, but they communicate through the queue boundary and must not share direct execution shortcuts.

Packages:

- `cmd/codex-issue-gateway`: process entry point, config loading, HTTP server, worker startup.
- `internal/config`: YAML configuration structs, defaults, and startup validation.
- `internal/webhook`: body size limits, GitHub HMAC verification, event normalization.
- `internal/commands`: `/codex ...` command and flag parsing.
- `internal/authz`: repo, actor, label, command, branch, and rate-limit decisions.
- `internal/queue`: SQLite schema, delivery idempotency, job CRUD, state transitions, artifacts.
- `internal/github`: GitHub App token generation and API operations.
- `internal/worker`: job polling, locking, per-repo concurrency, cancellation, state progression.
- `internal/sandbox`: job directories, clones, branches, environment assembly.
- `internal/runner`: Codex process execution and configured test command execution.
- `internal/diffpolicy`: changed file detection, denylist, review-required paths, size limits, secret checks.
- `internal/audit`: structured logs with redaction and request/job correlation fields.

## Data Flow

1. GitHub sends an Issue or Issue Comment webhook.
2. Gateway enforces max body size, validates `X-Hub-Signature-256`, and requires `X-GitHub-Event` plus `X-GitHub-Delivery`.
3. Gateway normalizes only supported issue events and ignores unsupported events with `202`.
4. Gateway stores the delivery idempotency record before job creation.
5. Gateway extracts independent-line `/codex` commands from Issue or Comment text.
6. Gateway loads the matching repo policy by `repository.full_name`.
7. Authorization validates actor role, command, labels, base branch, active job limits, and rate limits.
8. Rejected requests create no executable job and can be commented back to GitHub when the request identity is trusted.
9. Accepted executable commands create a queued job and an audit event.
10. Worker leases a queued job, creates an isolated job directory, prepares a repo checkout, and creates a safe work branch.
11. Worker runs Codex with fixed safety instructions and untrusted Issue context on stdin.
12. Worker runs configured test commands exactly from repo policy.
13. Worker evaluates diff policy.
14. Worker commits, pushes to the configured remote, creates or updates a PR, and comments on the Issue.

## Command Protocol

Commands are recognized only when they appear as a standalone line:

```text
/codex <command> [flags]
```

Supported commands:

- `/codex plan`
- `/codex implement`
- `/codex fix`
- `/codex review`
- `/codex retry`
- `/codex cancel`
- `/codex status`

MVP flags:

- `--branch <safe-name>`
- `--base <allowed-base-branch>`
- `--dry-run`
- `--max-minutes <1-120>`

The parser rejects unknown commands, unknown flags, unsafe branch names, bases outside repo policy, and max-minute values over the repo cap.

## Authorization Model

Actor roles are repository scoped:

- `viewer`: can observe only.
- `requester`: can request planning and status; implementation requires repo policy gates.
- `operator`: can plan, review, cancel, and query status.
- `maintainer`: can implement, fix, retry, cancel, review, and query status.
- `admin`: can use all commands and is reserved for future policy management.

Implementation-class commands require maintainer role unless the repo policy explicitly allows requesters with required labels such as `codex:ready`.

Unknown repositories, unknown actors, closed or locked issues, missing labels, unsafe branches, and duplicate active jobs are denied before any local execution.

## Queue And State Machine

SQLite is the MVP queue.

Tables:

- `webhook_deliveries`: delivery id, event type, repo, issue, actor, body hash, status, received timestamp.
- `jobs`: job id, delivery id, repo, issue, comment, actor, command, flags JSON, state, base branch, work branch, PR number, timestamps, last error.
- `job_events`: append-only state transition audit records.
- `job_artifacts`: stored logs, summaries, diffs, and checksums.

State transitions follow `todos.md`: `received`, `validating`, `rejected`, `queued`, `starting`, `planning`, `implementing`, `testing`, `reviewing`, `creating_pr`, `waiting_human`, `done`, `failed`, `cancelled`, and `expired`.

The queue enforces:

- Duplicate delivery ids do not create duplicate jobs.
- One active job per repo/issue by default.
- Configurable global and per-repo running limits.
- Timeouts transition running jobs to `expired`.

## Worker Isolation

Each job uses:

```text
/tmp/codex-issue-gateway/jobs/<job-id>/
  repo/
  codex-home/
  artifacts/
  tmp/
```

The worker must not run Codex or tests in the gateway project directory or in a developer's long-lived project checkout.

Repository preparation uses either:

- A configured `local_fixture_path` for local integration tests, cloned or worktree-copied into the job directory.
- The configured `clone_url` for normal operation.

Codex execution uses:

```text
CODEX_HOME=<job>/codex-home codex exec \
  --cd <job>/repo \
  --sandbox workspace-write \
  --ask-for-approval never \
  --ephemeral \
  --json \
  -
```

The gateway rejects configuration that requests `danger-full-access` or approval bypass options.

## Diff And PR Policy

Before PR creation, the worker runs:

- `git diff --name-only <base>...HEAD`
- `git diff --check`
- denylist matching.
- review-required matching.
- file count and line count limits.
- lightweight secret pattern scanning.

Default denylist includes:

- `.env`
- `.env.*`
- `**/*secret*`
- `**/*token*`
- `id_rsa`
- `id_ed25519`
- `docker-compose.yml`

If the denylist matches, the job fails and no PR is created. If review-required paths match, the PR may be created with a configured security review label.

## GitHub Integration

The system uses a GitHub App, not a long-lived personal access token.

MVP GitHub operations:

- Generate installation token from App private key.
- Read Issue, comments, labels, and actor permission as needed.
- Create Issue comments for accepted, rejected, failed, completed, and PR-created outcomes.
- Push a job branch through git credentials configured outside the job prompt.
- Create Pull Requests.
- Add labels such as `codex:needs-security-review` when policy requires it.

No production deployment, auto-merge, Actions write, Secrets access, or Environment access is part of MVP.

## Testing Strategy

Testing is TDD-first.

Unit coverage:

- HMAC verification success and failure.
- Missing signature rejection.
- Body size enforcement.
- Delivery id idempotency.
- Standalone command parsing.
- Flag validation.
- Repo policy lookup.
- Actor and label authorization.
- SQLite schema and state transitions.
- Active job limits.
- Worker leasing.
- Sandbox directory creation.
- Codex command assembly.
- Test command failure prevents PR creation.
- Denylist blocks `docker-compose.yml` and `.env`.
- Logs do not include webhook secrets.

Integration coverage:

- Local fixture repo `/app/devs/foliospace-Library`.
- Fake GitHub client for comments and PRs.
- Fake Codex runner for deterministic job outcomes.
- Optional real Codex/GitHub smoke tests guarded by explicit environment variables.

## Initial Repository Configuration

The sample configuration includes `funland/foliospace-Library` to support local validation, but the data model and code paths are multi-repo from the first implementation.

Example:

```yaml
repos:
  - full_name: "funland/foliospace-Library"
    clone_url: "git@github.com:funland/foliospace-Library.git"
    local_fixture_path: "/app/devs/foliospace-Library"
    fork_push_remote: "git@github.com:hellcatjack/foliospace-Library.git"
    base_branches: ["main"]
    protected_branches: ["main", "master", "release/*"]
    required_labels_for_implement: ["codex:ready"]
    allowed_actors:
      admins: ["hellcatjack"]
      maintainers: ["hellcatjack"]
      operators: []
      requesters: []
    commit_author:
      name: "hellcatjack"
      email: "hellcatjack@gmail.com"
    test_commands:
      - "go test ./..."
      - "npm --prefix web run build"
      - "git diff --check"
    deny_paths:
      - ".env"
      - ".env.*"
      - "**/*secret*"
      - "**/*token*"
      - "docker-compose.yml"
```

## MVP Boundaries

In scope:

- Multi-repo configuration model.
- GitHub Issue and Issue Comment command flow.
- Phase 1-8 execution through PR creation.
- SQLite queue.
- GitHub App authentication.
- Local worker isolation.
- Codex CLI execution.
- Test and diff policy enforcement.
- Issue status comments.

Out of scope:

- Production deployment automation.
- Auto-merge.
- Web management UI.
- PostgreSQL.
- Container sandboxing as the default implementation.
- PR comment driven fixes.
- OPA/Rego policy engine.
- OpenTelemetry.

These out-of-scope items are kept as later enhancements after the MVP proves safe and useful.
