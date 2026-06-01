package runner

import (
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

type CodexInput struct {
	CodexBinary string
	RepoDir     string
	CodexHome   string
	Repo        config.RepoConfig
	Prompt      string
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
		"--ask-for-approval", approval,
	}
	if input.Repo.Codex.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if input.Repo.Codex.JSONEvents {
		args = append(args, "--json")
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
`)
	return safety + "\n\n" + prompt
}
