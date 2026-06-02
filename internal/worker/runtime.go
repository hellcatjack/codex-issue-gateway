package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/publicreport"
	"github.com/hellcatjack/codex-issue-gateway/internal/runner"
	"github.com/hellcatjack/codex-issue-gateway/internal/sandbox"
)

type LocalRunner struct {
	CodexBinary string
}

const (
	safeProcessOutputMaxLines  = 240
	safeProcessOutputHeadLines = 80
	safeProcessOutputTailLines = 160
)

func (r LocalRunner) RunCodex(ctx context.Context, input CodexInput, onActivity func()) (CodexResult, error) {
	spec := runner.BuildCodexCommand(runner.CodexInput{
		CodexBinary: r.CodexBinary,
		RepoDir:     input.Workspace.RepoDir,
		CodexHome:   input.Workspace.CodexHome,
		Repo:        input.Repo,
		Prompt:      input.Prompt,
		ImageFiles:  input.ImageFiles,
	})
	result, err := runner.RunProcess(ctx, runner.ProcessInput{
		Name:  spec.Name,
		Args:  spec.Args,
		Dir:   spec.Dir,
		Env:   spec.Env,
		Stdin: spec.Stdin,
		OnActivity: func(runner.Activity) {
			if onActivity != nil {
				onActivity()
			}
		},
	})
	writeCodexDiagnostics(input.Workspace, result)
	publicReport := publicreport.FromCodexOutput(result.Stdout)
	if err != nil {
		failureReport := codexProcessFailureReport(result)
		if publicReport == publicreport.Fallback {
			publicReport = failureReport
		} else {
			publicReport = appendPublicSection(publicReport, failureReport)
		}
		return CodexResult{Status: "failed", PublicReport: publicReport}, err
	}
	status := "completed"
	if strings.Contains(result.Stdout, "needs_plan_revision") || strings.Contains(result.Stderr, "needs_plan_revision") {
		status = "needs_plan_revision"
	}
	return CodexResult{
		Status:                 status,
		Summary:                "completed",
		PublicReport:           publicReport,
		ReadyForImplementation: status == "completed",
	}, nil
}

func writeCodexDiagnostics(ws sandbox.Workspace, result runner.ProcessResult) {
	if strings.TrimSpace(ws.ArtifactsDir) == "" {
		return
	}
	dir := filepath.Join(ws.ArtifactsDir, "internal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	writeDiagnosticFile(filepath.Join(dir, "codex-stdout.log"), result.Stdout)
	writeDiagnosticFile(filepath.Join(dir, "codex-stderr.log"), result.Stderr)
}

func writeDiagnosticFile(path, body string) {
	if body == "" {
		return
	}
	_ = os.WriteFile(path, []byte(body), 0o600)
}

func codexProcessFailureReport(result runner.ProcessResult) string {
	lines := []string{"Codex process failed before completing the task."}
	if result.ExitCode != 0 {
		lines = append(lines, fmt.Sprintf("- Exit code: %d", result.ExitCode))
	}
	if excerpt := safeProcessOutputExcerpt(result.Stderr); excerpt != "" {
		lines = append(lines, "Safe diagnostic output:\n"+excerpt)
	} else if excerpt := safeProcessOutputExcerpt(result.Stdout); excerpt != "" {
		lines = append(lines, "Safe diagnostic output:\n"+excerpt)
	}
	return publicreport.Sanitize(strings.Join(lines, "\n"))
}

func safeProcessOutputExcerpt(output string) string {
	output = publicreport.Sanitize(output)
	if output == publicreport.Fallback {
		return ""
	}
	rawLines := strings.Split(output, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > safeProcessOutputMaxLines {
		head := append([]string(nil), lines[:safeProcessOutputHeadLines]...)
		tail := lines[len(lines)-safeProcessOutputTailLines:]
		return strings.Join(append(append(head, "..."), tail...), "\n")
	}
	return strings.Join(lines, "\n")
}

func (r LocalRunner) RunTests(ctx context.Context, repoDir string, commands []string, onActivity func()) (TestResult, error) {
	result, err := runner.RunTestCommands(ctx, repoDir, commands, func(runner.Activity) {
		if onActivity != nil {
			onActivity()
		}
	})
	return TestResult{Passed: result.Passed, Output: publicTestReport(commands, result)}, err
}

func (r LocalRunner) RunCommands(ctx context.Context, repoDir string, commands []string, onActivity func()) (CommandResult, error) {
	result, err := runner.RunTestCommands(ctx, repoDir, commands, func(runner.Activity) {
		if onActivity != nil {
			onActivity()
		}
	})
	return CommandResult{Passed: result.Passed, Output: publicCommandReport(commands, result)}, err
}

func publicCommandReport(commands []string, result runner.TestCommandsResult) string {
	if result.Passed {
		return ""
	}
	return publicProcessReport("Gateway workspace preparation failed:", commands, result)
}

func publicTestReport(commands []string, result runner.TestCommandsResult) string {
	if result.Passed {
		return ""
	}
	return publicProcessReport("Gateway verification failed:", commands, result)
}

func publicProcessReport(title string, commands []string, result runner.TestCommandsResult) string {
	lines := []string{title}
	failedIndex := firstFailedProcessIndex(result)
	for i, process := range result.Results {
		status := "passed"
		if i == failedIndex {
			status = "failed"
			if process.ExitCode != 0 {
				status = fmt.Sprintf("failed with exit code %d", process.ExitCode)
			}
		}
		line := fmt.Sprintf("- Command %d: %s", i+1, status)
		if label := safeCommandLabel(commands, i); label != "" {
			line += " (" + label + ")"
		}
		lines = append(lines, line)
		if i == failedIndex {
			if excerpt := safeProcessOutputExcerpt(strings.TrimSpace(process.Stdout + "\n" + process.Stderr)); excerpt != "" {
				lines = append(lines, "Safe failure output:\n"+excerpt)
			}
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "- Command 1: failed")
	}
	return publicreport.Sanitize(strings.Join(lines, "\n"))
}

func firstFailedProcessIndex(result runner.TestCommandsResult) int {
	for i, process := range result.Results {
		if process.ExitCode != 0 {
			return i
		}
	}
	if !result.Passed && len(result.Results) > 0 {
		return len(result.Results) - 1
	}
	return -1
}

func safeCommandLabel(commands []string, index int) string {
	if index >= len(commands) {
		return ""
	}
	command := strings.TrimSpace(commands[index])
	if command == "" || strings.Contains(command, "`") {
		return ""
	}
	if len(command) > 160 {
		command = strings.TrimSpace(command[:160]) + "..."
	}
	label := "`" + command + "`"
	if publicreport.Sanitize(label) != label {
		return ""
	}
	return label
}

type GitDiffScanner struct{}

func (GitDiffScanner) ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error) {
	return sandbox.ChangedFiles(ctx, repoDir, baseBranch)
}
