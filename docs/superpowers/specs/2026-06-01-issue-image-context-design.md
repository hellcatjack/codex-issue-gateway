# Issue Image Context Design

## Goal

Allow Codex Gateway jobs to understand images embedded in GitHub issues and comments, such as pasted screenshots, while preserving the existing workflow guarantees: collaborator-only issue context, `/codex` commands accepted only from `hellcatjack`, no interactive Codex questions, and no public leakage of internal paths or secrets.

## Scope

The gateway will process Markdown image links and simple HTML image tags found in collaborator-authored issue bodies and comments. It will only download GitHub-hosted issue image assets from an allowlist of hosts and paths. Unsupported hosts, oversized files, non-image responses, and failed downloads are skipped without blocking the job.

## Architecture

Add an `internal/issueimage` package that:

- Extracts image references from collaborator-only issue context.
- Allows only HTTPS GitHub-hosted image URLs.
- Downloads a small bounded number of images with byte limits, timeouts, content-type validation, and redirect allowlist checks.
- Saves images into the job artifacts directory.
- Produces a safe prompt section with alt text, source host, content type, size, and dimensions when available.

Extend the worker prompt preparation so the prompt includes a safe "Issue image inputs" section and the local runner receives the downloaded files as Codex `--image` attachments. This uses `codex exec --image <FILE>` instead of asking Codex to fetch remote URLs.

## Data Flow

1. Worker fetches the fresh issue context from GitHub.
2. `issuecontext` filters visible text to collaborator-authored content.
3. `issueimage` extracts and downloads allowed images from the same trusted body/comment set.
4. Worker appends the safe image summary to the internal prompt.
5. Worker passes downloaded image paths through `CodexInput.ImageFiles`.
6. Runner adds `--image` arguments for each file when invoking `codex exec`.

## Safety Rules

- Do not download arbitrary URLs.
- Require `https`.
- Allow `github.com/user-attachments/assets/...`, `user-images.githubusercontent.com`, `private-user-images.githubusercontent.com`, and `camo.githubusercontent.com` by default.
- Allow GitHub user-attachment URLs to follow one or more checked redirects to GitHub production user-asset S3 hosts, but do not allow direct S3 URLs as initial issue image inputs.
- Validate every redirect target against the same allowlist.
- Reject SVG and non-image responses.
- Enforce maximum image count and maximum bytes per image.
- Do not include local image paths or source URLs in public comments or PR bodies.
- Do not fail the whole job if image processing fails; the prompt should record only safe skipped-image summaries.

## Testing

Unit tests cover extraction, host filtering, download validation, non-image rejection, runner `--image` argument construction, and worker prompt integration. Full repository tests must pass before completion.
