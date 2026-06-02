# Implement Auto Repair Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make implementation jobs self-repair after Codex or verification failures instead of publicly reporting implementation failure on the first failed attempt.

**Architecture:** Keep a single queued job and a single workspace. Run the initial implement attempt, then run configured verification; if Codex execution or verification fails, build a sanitized repair prompt from the prior public report and failure report, run Codex again in the same workspace, and repeat until verification passes or the bounded repair budget is exhausted. Exhaustion should move the issue to plan revision instead of posting an implementation failure.

**Tech Stack:** Go, existing worker/runner abstractions, SQLite queue state, GitHub issue comments.

---

### Task 1: Add repair-loop behavior tests

**Files:**
- Modify: `internal/worker/worker_test.go`

- [x] Add a test where the first verification run fails, the second Codex run repairs the workspace, the second verification run passes, and the worker creates a PR without posting a failure comment.
- [x] Add a test where verification keeps failing until the repair budget is exhausted, and the worker posts a plan-revision style comment rather than `Codex 执行失败`.
- [x] Run the focused worker tests and confirm the new tests fail before implementation.

### Task 2: Implement bounded repair attempts

**Files:**
- Modify: `internal/worker/worker.go`

- [x] Split the implementation path into an initial attempt plus repair attempts.
- [x] On Codex process failure or verification failure, append safe screenshot artifacts and changed-file previews to the internal repair context.
- [x] Build a repair prompt that instructs Codex to fix the current workspace from sanitized diagnostics, not ask questions, and produce one final response.
- [x] Keep setup failures, unavailable issue context, deny-path failures, PR push failures, and GitHub API failures outside the repair loop.
- [x] Stop after a bounded number of repair attempts and report `needs_plan_revision` instead of `Codex 执行失败`.

### Task 3: Verify behavior and restart local service

**Files:**
- Verify: `internal/worker/worker.go`
- Verify: `internal/worker/worker_test.go`

- [x] Run `go test ./internal/worker -count=1`.
- [x] Run `go test ./...`.
- [x] Run `git diff --check`.
- [x] Rebuild `tmp/codex-issue-gateway`, restart the local `tmp/local.yml` service, and verify `/readyz`.
