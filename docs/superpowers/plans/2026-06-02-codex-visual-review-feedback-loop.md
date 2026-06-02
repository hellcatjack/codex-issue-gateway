# Codex Visual Review Feedback Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Add a worker-owned visual review loop that captures Playwright screenshots outside Codex and feeds them back to Codex as image inputs before final PR creation.

**Architecture:** Extend config with `worker.visual_review_attempts` and repo `visual_review_commands`. In implementation/fix jobs, run normal Codex and gateway tests first, then run visual review commands, publish validated screenshots, attach the latest screenshots to a follow-up Codex invocation, and only finish when Codex confirms those screenshots without further file changes.

**Tech Stack:** Go, YAML config, existing worker runner, existing screenshot artifact publisher, Codex `--image` inputs, Playwright commands supplied by repository config.

---

### Task 1: Config Support

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [x] **Step 1: Write failing config tests**

Add tests that assert:

```go
if cfg.Worker.VisualReviewAttempts != 3 {
    t.Fatalf("visual review attempts = %d", cfg.Worker.VisualReviewAttempts)
}
if !slices.Equal(repo.VisualReviewCommands, []string{"npm run visual-review"}) {
    t.Fatalf("visual review commands = %#v", repo.VisualReviewCommands)
}
```

- [x] **Step 2: Verify the tests fail**

Run:

```bash
go test ./internal/config -run 'TestLoadBuildsRepoIndexAndDefaults|TestLoadParsesRepoVisualReviewCommands' -count=1
```

Expected: compile failure because `VisualReviewAttempts` and `VisualReviewCommands` do not exist.

- [x] **Step 3: Implement config fields**

Add:

```go
VisualReviewAttempts int `yaml:"visual_review_attempts"`
VisualReviewCommands []string `yaml:"visual_review_commands"`
```

Default `VisualReviewAttempts` to `3` when unset.

- [x] **Step 4: Verify config tests pass**

Run:

```bash
go test ./internal/config -count=1
```

Expected: all config tests pass.

### Task 2: Prompt Contract

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [x] **Step 1: Write failing prompt test**

Extend `TestWorkerPromptIncludesExecutionConstraints` to assert the prompt contains:

```text
Do not start preview servers or browser automation inside the Codex sandbox.
Gateway will run these visual review commands outside Codex when needed:
```

- [x] **Step 2: Verify the test fails**

Run:

```bash
go test ./internal/worker -run TestWorkerPromptIncludesExecutionConstraints -count=1
```

Expected: prompt assertion fails.

- [x] **Step 3: Implement prompt text**

Update `executionConstraints` to include the sandbox browser boundary and configured visual review commands.

- [x] **Step 4: Verify prompt test passes**

Run:

```bash
go test ./internal/worker -run TestWorkerPromptIncludesExecutionConstraints -count=1
```

Expected: test passes.

### Task 3: Visual Review Loop

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [x] **Step 1: Write failing worker tests**

Add tests for:

```go
TestWorkerFeedsVisualReviewScreenshotsBackToCodexBeforePR
TestWorkerRepairsWhenVisualReviewPassesWithoutScreenshots
```

The first test should configure `VisualReviewCommands`, make the first Codex call complete, make tests pass, have visual review write `.codex-gateway-artifacts/screenshots/latest.png`, assert the second Codex call receives that image file, then assert PR creation happens after the second Codex call.

The second test should configure `VisualReviewCommands`, make visual review pass without files, and assert Codex is invoked again with a prompt mentioning missing screenshots instead of creating a PR immediately.

- [x] **Step 2: Verify worker tests fail**

Run:

```bash
go test ./internal/worker -run 'TestWorkerFeedsVisualReviewScreenshotsBackToCodexBeforePR|TestWorkerRepairsWhenVisualReviewPassesWithoutScreenshots' -count=1
```

Expected: behavior fails because worker does not run visual review commands.

- [x] **Step 3: Implement visual review helper**

Add helper functions that:

- record existing public screenshot files
- run `repo.VisualReviewCommands`
- publish safe screenshot artifacts
- collect newly published screenshot files
- return pass/fail, public report, and image file paths

- [x] **Step 4: Integrate helper into implementation loop**

After gateway `test_commands` pass, run visual review when configured. Attach latest images to the next Codex call. Do not create a PR until Codex confirms the visual review without changing files.

- [x] **Step 5: Verify worker tests pass**

Run:

```bash
go test ./internal/worker -count=1
```

Expected: all worker tests pass.

### Task 4: Docs And Local Config

**Files:**
- Modify: `README.md`
- Modify: `docs/operations.md`
- Modify: `docs/local-development.md`
- Modify: `configs/example.yml`
- Modify: `tmp/local.yml` without committing it

- [x] **Step 1: Document visual review**

Document that Codex runs non-browser TDD in sandbox and worker runs browser visual review outside sandbox.

- [x] **Step 2: Update example config**

Add:

```yaml
worker:
  visual_review_attempts: 3
repos:
  - visual_review_commands:
      - "npm run visual-review"
```

- [x] **Step 3: Update local epubReader config**

Move the current screenshot-producing Playwright command from `test_commands` to `visual_review_commands`.

### Task 5: Verification And Deployment

**Files:**
- Build output: `tmp/codex-issue-gateway` ignored

- [x] **Step 1: Run full Go tests**

Run:

```bash
go test -count=1 ./...
```

Expected: all tests pass.

- [x] **Step 2: Build**

Run:

```bash
go build -o tmp/codex-issue-gateway ./cmd/codex-issue-gateway
```

Expected: build succeeds.

- [x] **Step 3: Restart local gateway**

Restart the running process with `tmp/local.yml` and verify:

```bash
curl -fsS http://127.0.0.1:18091/readyz
```

Expected: `{"github":"ok","ok":true,"queue":"ok"}`.

- [x] **Step 4: Commit and push**

Commit tracked code/docs/config example only. Do not commit `tmp/local.yml`.
