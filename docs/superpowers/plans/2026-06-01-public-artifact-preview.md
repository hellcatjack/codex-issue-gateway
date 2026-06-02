# Public Artifact Preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show useful, safe previews for files created or changed by Codex jobs, especially plan files such as `docs/superpowers/plans/*.md`.

**Architecture:** Add a small `internal/publicartifact` package that turns repo-relative changed files into bounded Markdown previews. Worker code will append those previews to plan comments, PR bodies, and implementation comments after the existing agent feedback, and the final public text still passes through the existing `publicreport` sanitizer.

**Tech Stack:** Go, Markdown text extraction, existing diff scanner, existing public report sanitizer.

---

### Task 1: Public Artifact Preview Package

**Files:**
- Create: `internal/publicartifact/publicartifact.go`
- Create: `internal/publicartifact/publicartifact_test.go`

- [ ] **Step 1: Write failing tests for Markdown preview extraction**

Create tests that write a plan-like Markdown file under a temporary repo and assert that the preview includes the relative path, Goal, Architecture, task headings, Files, and Verification commands.

- [ ] **Step 2: Write failing tests for safety filtering**

Create tests that include unsafe lines with local absolute paths, environment variable assignments, and secret-like tokens. Assert that unsafe lines are omitted and the fallback message appears only when no safe preview remains.

- [ ] **Step 3: Implement bounded previews**

Implement `Build(repoDir string, files []string, denyPaths []string) string`. It should allow text files only, reject unsafe paths, read at most a bounded amount of content, extract useful Markdown lines, and return Markdown suitable for an issue comment.

### Task 2: Worker Integration

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

- [ ] **Step 1: Add a failing plan-comment integration test**

Make the fake Codex runner create `docs/superpowers/plans/example.md` in the job workspace. Assert the plan comment contains `Artifact preview`, the file path, task headings, and no unsafe local path.

- [ ] **Step 2: Add a failing implementation-comment integration test**

Make the fake runner create a Markdown doc, run the implement path, and assert the PR body contains the public artifact preview alongside agent feedback and gateway verification.

- [ ] **Step 3: Append previews after changed-file scanning**

In `runPlan`, scan changed files after Codex finishes and append the preview to `result.PublicReport`. In `runImplementation`, append the preview after diff policy passes and before creating the PR.

### Task 3: Verification

**Files:**
- Verify: `internal/publicartifact`
- Verify: `internal/worker`
- Verify: repository root

- [ ] **Step 1: Run focused red/green tests**

Run `GOTOOLCHAIN=local go test ./internal/publicartifact ./internal/worker -count=1`.

- [ ] **Step 2: Run full verification**

Run `GOTOOLCHAIN=local go test -count=1 ./...` and `git diff --check`.

- [ ] **Step 3: Restart local gateway**

Rebuild `tmp/codex-issue-gateway`, restart the local gateway process using `tmp/local.yml`, and verify `https://ushome.amycat.com:18090/readyz`.

- [ ] **Step 4: Trigger a small epubReader closed-loop test**

Create a small issue that asks Codex to add a Markdown-only doc. Verify the plan comment and PR body show a safe artifact preview with no local absolute paths or token-like content.
