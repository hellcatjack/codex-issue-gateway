# codex-issue-gateway

Self-hosted GitHub Issue automation gateway for running non-interactive Codex development jobs across configured repositories.

## Safety Defaults

- GitHub webhook HMAC verification is required.
- Commands are accepted only from standalone `/codex ...` lines.
- Execution commands are non-interactive and never wait for user input.
- Worker expiry is based on no activity, not normal elapsed job duration.
- Jobs run in isolated directories with per-job `CODEX_HOME`.
- Repository policies define allowed actors, branches, tests, deny paths, and review-required paths.
