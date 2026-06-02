# Codex Visual Review Feedback Loop Design

## Goal

Allow Codex to use real browser screenshots as feedback during implementation, without requiring Codex itself to start preview servers or Chromium inside its sandbox.

## Verified Environment Boundary

The current host can run browser verification outside Codex:

- A host Node process can bind `127.0.0.1`.
- A host Playwright process can launch Chromium from `/home/hellcat/.cache/ms-playwright`.
- Host `npm run test` for `/data/share/epubReader` passes.

Codex `workspace-write` sandbox can run non-browser self-tests:

- It can write and delete workspace files.
- It can run targeted Vitest tests.
- It can run focused regression tests for local TDD.

Codex `workspace-write` sandbox cannot run browser review directly:

- Binding `127.0.0.1` fails with `listen EPERM`.
- Launching Chromium fails with `sandbox_host_linux.cc ... Operation not permitted`.

Therefore Codex should keep the TDD red/green loop focused on non-browser tests, while the gateway worker owns browser screenshot capture outside the Codex sandbox.

## Architecture

Add repository-level `visual_review_commands`. These commands are separate from `test_commands` and are run by the worker after Codex returns `completed` and configured non-browser verification passes.

`visual_review_commands` must write screenshots under:

```text
.codex-gateway-artifacts/screenshots/
```

The worker publishes validated screenshots into the job public artifact directory, then feeds the latest screenshots back into the next Codex invocation as image inputs. Codex inspects those images and either:

- returns `completed` without changing files when the visual result satisfies the issue, or
- modifies the workspace and returns `completed`, causing the worker to run tests and visual review again.

This creates a real visual feedback loop:

```text
Codex code change
  -> Codex runs focused non-browser TDD tests inside sandbox
  -> worker runs configured test_commands outside sandbox
  -> worker runs visual_review_commands outside sandbox
  -> worker attaches latest screenshots to Codex
  -> Codex inspects screenshots and adjusts or confirms
```

## Configuration

Add:

```yaml
worker:
  visual_review_attempts: 3

repos:
  - full_name: "owner/repo"
    visual_review_commands:
      - "npm run visual-review"
```

`worker.visual_review_attempts` limits the number of screenshot feedback cycles. The default is `3`.

## Worker Flow

Implementation and fix jobs use this sequence:

1. Run Codex.
2. If Codex process fails, continue the existing auto-repair loop.
3. If Codex returns `needs_plan_revision` and visual review commands exist, run visual review once before going to `waiting_human`; this handles cases where Codex only asked for plan revision because it could not capture screenshots itself.
4. If Codex returns `completed`, run `test_commands`.
5. If tests fail, feed sanitized diagnostics back to Codex as today.
6. If tests pass and `visual_review_commands` are absent, continue to diff policy and PR creation.
7. If visual review commands exist, run them outside Codex.
8. If visual review fails or produces no safe screenshots, feed diagnostics and any produced screenshots back to Codex.
9. If visual review passes and produces safe screenshots, attach the latest screenshots to Codex and ask it to inspect them.
10. If Codex confirms screenshots without changing files, continue to diff policy and PR creation.
11. If Codex changes files after seeing screenshots, rerun tests and visual review.

## Prompting Contract

Codex prompts should explicitly say:

- Do not start preview servers or browser automation inside the Codex sandbox.
- Use focused non-browser tests for TDD inside Codex.
- The gateway will run browser visual review outside the sandbox and attach screenshots.
- When screenshots are attached, inspect them and decide whether to adjust code or confirm completion.

## Safety

Screenshot publication continues to use the existing artifact safety gate:

- source directory must not be symlinked
- files must be regular image files
- file size is capped
- suspicious names are skipped
- public comments and PR bodies are sanitized

Only validated public screenshot files are reattached to Codex as image inputs.

## Testing Strategy

Unit coverage should prove:

- config parses `visual_review_commands`
- config defaults `worker.visual_review_attempts`
- prompts tell Codex not to run browser automation inside sandbox
- worker runs visual review commands after tests pass
- worker feeds newly published screenshots into the next Codex call
- worker does not create a PR until Codex confirms the screenshot
- worker reruns tests and visual review if Codex changes files after seeing screenshots
- worker treats missing screenshots from visual review as a repair condition

Local verification should run:

```bash
go test -count=1 ./...
go build -o tmp/codex-issue-gateway ./cmd/codex-issue-gateway
```

For `/data/share/epubReader`, local config should move the current screenshot-producing Playwright command from `test_commands` to `visual_review_commands`.
