package runner

import (
	"fmt"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

type CodexInput struct {
	CodexBinary string
	RepoDir     string
	CodexHome   string
	Repo        config.RepoConfig
	Prompt      string
	ImageFiles  []string
}

func BuildCodexCommand(input CodexInput) CommandSpec {
	binary := input.CodexBinary
	if binary == "" {
		binary = "codex"
	}
	sandbox := input.Repo.Codex.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	approval := input.Repo.Codex.AskForApproval
	if approval == "" {
		approval = "never"
	}
	args := []string{
		"exec",
		"--cd", input.RepoDir,
		"--sandbox", sandbox,
		"-c", fmt.Sprintf("approval_policy=%q", approval),
	}
	if input.Repo.Codex.JSONEvents {
		args = append(args, "--json")
	}
	for _, imageFile := range input.ImageFiles {
		if strings.TrimSpace(imageFile) == "" {
			continue
		}
		args = append(args, "--image", imageFile)
	}
	args = append(args, "-")
	return CommandSpec{
		Name:  binary,
		Args:  args,
		Dir:   input.RepoDir,
		Env:   map[string]string{"CODEX_HOME": input.CodexHome},
		Stdin: nonInteractivePrompt(input.Prompt),
	}
}

func nonInteractivePrompt(prompt string) string {
	safety := strings.TrimSpace(`
Execution contract:
- Do not ask the user questions.
- Do not wait for clarification.
- Use the approved plan assumptions when safe.
- If safe implementation is impossible, return status needs_plan_revision and list all missing information in one response.
- Do not read files outside the repository.
- Do not modify denylisted files.

Public reporting contract:
- End the final response with concise public feedback suitable for a GitHub issue or pull request.
- Include only repo-relative files, high-level decisions, changed behavior, and verification commands/results.
- Do not include local absolute paths, credentials, environment variables, private URLs, raw logs, stack traces, or internal workspace details.
- If issue images are attached, inspect them as read-only visual references.
- Do not include cached issue image paths or source image URLs in public feedback.
- If UI or browser-visible behavior changed and real screenshots were captured, save only safe non-sensitive screenshots under .codex-gateway-artifacts/screenshots/; do not claim screenshots exist unless they were actually captured.
- Prefer this shape:
  status: completed
  Summary:
  - ...
  Changes:
  - ...
  Verification:
  - ...
`)
	return safety + "\n\n" + prompt
}
