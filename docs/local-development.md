# Local Development

## Prerequisites

- Go toolchain for the module in `go.mod`.
- GitHub CLI when testing real issue comments or pull requests.
- A local Codex CLI when `worker.enabled` is true.
- A local Node runtime when `worker.playwright.enabled` is true.
- A GitHub App private key and webhook secret for real GitHub execution.
- Local fixture repositories for integration tests, when enabled.

Do not commit local runtime config files, database files, job workspaces, private keys, or generated screenshots. The default `.gitignore` excludes `tmp/`, SQLite files, coverage output, and build artifacts.

## Verify

Run the full Go test suite:

```bash
go test ./...
```

Run the FolioSpace fixture integration test when the local fixture is available:

```bash
CODEX_GATEWAY_RUN_INTEGRATION=1 go test ./tests/integration -v
```

Build the gateway binary:

```bash
go build -o tmp/codex-issue-gateway ./cmd/codex-issue-gateway
```

## Run Locally

Start the gateway with a config file:

```bash
go run ./cmd/codex-issue-gateway --config configs/example.yml
```

For real GitHub execution, use a local config outside version control. The config must include:

- `github.app_id`
- `github.installation_id`
- `github.private_key_file`
- `github.webhook_secret_file`
- `queue.dsn`
- at least one `repos` entry with clone, push, actor, branch, test, deny path, and Codex policy settings

When testing through a reverse proxy, point GitHub webhooks at:

```text
POST <public-base-url>/github/webhook
```

Health checks:

```bash
curl -s http://127.0.0.1:18090/healthz
curl -s http://127.0.0.1:18090/readyz
```

## Worker Notes

Set `worker.enabled: true` only when the host is ready to execute local Codex jobs. The worker:

- clones or copies the configured repository into an isolated job directory
- prepares a per-job `CODEX_HOME`
- optionally runs `agent_setup_commands`
- optionally preinstalls Playwright browsers into the configured worker cache
- runs Codex non-interactively
- runs `test_commands`
- opens a PR only when committed changes exist

For Node projects, prefer putting the intended runtime at the front of `PATH` in `test_commands`; npm and test runner shims often use `/usr/bin/env node`.

For Playwright projects, enable `worker.playwright` and keep `node_modules` restoration in `agent_setup_commands`. The worker injects `PLAYWRIGHT_BROWSERS_PATH` into Codex and verification commands, then runs `node node_modules/@playwright/test/cli.js install chromium` after dependencies are present.
