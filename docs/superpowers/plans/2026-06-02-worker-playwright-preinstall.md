# Worker Playwright Preinstall Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Playwright browser availability a worker-level responsibility so gateway jobs do not fail because browsers are missing.

**Architecture:** Add a `worker.playwright` config block, inject `PLAYWRIGHT_BROWSERS_PATH` into Codex/setup/test commands, and run a best-effort Playwright browser install after repo setup when a Playwright CLI exists in the prepared workspace. Non-Playwright repositories skip this step.

**Tech Stack:** Go, YAML config, existing worker runner abstractions, Playwright CLI.

---

### Task 1: Config Model And Defaults

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add a config test that loads `worker.playwright.enabled`, `browsers_path`, `node_binary`, and `browsers`, then asserts those values are present in `Config.Worker.Playwright`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config -run TestLoadParsesWorkerPlaywrightConfig -count=1`

Expected: compile failure because `WorkerConfig.Playwright` does not exist.

- [ ] **Step 3: Implement minimal config support**

Add `PlaywrightConfig` with `Enabled`, `BrowsersPath`, `NodeBinary`, and `Browsers`, then add it to `WorkerConfig`.

- [ ] **Step 4: Add defaults and validation**

When enabled, default browsers to `chromium`, default node binary to `node`, and default browser cache path to `<job_root>/_playwright-browsers` if unset. Reject empty browser names.

- [ ] **Step 5: Run config tests**

Run: `go test ./internal/config -count=1`

Expected: all config tests pass.

### Task 2: Runner Environment Injection

**Files:**
- Modify: `internal/runner/process.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `internal/worker/runtime.go`
- Add: `internal/worker/runtime_test.go`

- [ ] **Step 1: Write the failing runner test**

Add a test that calls `RunTestCommandsWithEnv` with `PLAYWRIGHT_BROWSERS_PATH=/cache/ms-playwright` and verifies a shell command can read that environment variable.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runner -run TestRunTestCommandsUsesExtraEnv -count=1`

Expected: compile failure because `RunTestCommandsWithEnv` does not exist.

- [ ] **Step 3: Implement runner env API**

Add `RunTestCommandsWithEnv(ctx, repoDir, commands, env, onActivity)` and keep `RunTestCommands` as a nil-env wrapper.

- [ ] **Step 4: Write the failing LocalRunner test**

Add a test for `worker.LocalRunner{Environment: map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "/cache/ms-playwright"}}` proving test commands receive the configured environment.

- [ ] **Step 5: Implement LocalRunner environment merge**

Add `Environment map[string]string` to `LocalRunner`; use it for `RunCodex`, `RunCommands`, and `RunTests` while preserving per-job `spec.Env`.

- [ ] **Step 6: Run runner and worker runtime tests**

Run: `go test ./internal/runner ./internal/worker -count=1`

Expected: runner and worker tests pass.

### Task 3: Worker Playwright Preinstall

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write the failing worker test**

Add a test where agent setup creates `node_modules/@playwright/test/cli.js`, then assert the worker runs a command containing `PLAYWRIGHT_BROWSERS_PATH`, the configured node binary, `node_modules/@playwright/test/cli.js`, `install`, and `chromium` before Codex execution.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/worker -run TestWorkerPreinstallsPlaywrightBrowsersAfterSetup -count=1`

Expected: test failure because the worker currently only runs agent setup commands.

- [ ] **Step 3: Implement preinstall**

After `repo.AgentSetupCommands`, detect `node_modules/@playwright/test/cli.js` or `node_modules/playwright/cli.js`; if present, run `PLAYWRIGHT_BROWSERS_PATH=<path> <node> <cli> install <browsers...>` through the existing command runner.

- [ ] **Step 4: Wire env into the app**

When `worker.playwright.enabled` is true, construct `LocalRunner.Environment` with `PLAYWRIGHT_BROWSERS_PATH`.

- [ ] **Step 5: Run worker tests**

Run: `go test ./internal/worker -count=1`

Expected: worker tests pass.

### Task 4: Local Config, Docs, And Verification

**Files:**
- Modify: `configs/example.yml`
- Modify: `docs/operations.md`
- Modify: `README.md`
- Modify: `tmp/local.yml` without committing it

- [ ] **Step 1: Document the new config**

Add a `worker.playwright` example showing `enabled`, `browsers_path`, `node_binary`, and `browsers`.

- [ ] **Step 2: Update local config**

Enable Playwright in `tmp/local.yml` with `/home/hellcat/.cache/ms-playwright` and `/home/hellcat/.nvm/versions/node/v22.21.1/bin/node`.

- [ ] **Step 3: Install browsers locally**

Run: `PLAYWRIGHT_BROWSERS_PATH=/home/hellcat/.cache/ms-playwright /home/hellcat/.nvm/versions/node/v22.21.1/bin/node /data/share/epubReader/node_modules/@playwright/test/cli.js install chromium`

Expected: command exits 0 and Chromium is available under the configured cache.

- [ ] **Step 4: Run full verification**

Run: `go test ./...`

Expected: all Go tests pass.

- [ ] **Step 5: Rebuild and restart gateway**

Run: `go build -o tmp/codex-issue-gateway ./cmd/codex-issue-gateway`, restart the local process with `tmp/local.yml`, then verify `/readyz` returns 200.

- [ ] **Step 6: Commit and push**

Commit tracked code and docs only. Do not commit `tmp/local.yml`.
