package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/sandbox"
)

func TestLocalRunnerRunsTestCommands(t *testing.T) {
	dir := t.TempDir()
	runner := LocalRunner{}

	result, err := runner.RunTests(context.Background(), dir, []string{"printf ok > result.txt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("result=%#v", result)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "result.txt")); err != nil || string(got) != "ok" {
		t.Fatalf("file=%q err=%v", string(got), err)
	}
}

func TestLocalRunnerReturnsPublicReportWhenCodexExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	codex := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"status: failed\n\nSummary:\n- Safe partial result.\n- Log was written under /tmp/private/job.log"}}'
exit 1
`
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (LocalRunner{CodexBinary: codex}).RunCodex(context.Background(), CodexInput{
		Repo:      config.RepoConfig{},
		Workspace: sandbox.Workspace{RepoDir: dir, CodexHome: codexHome},
		Prompt:    "test prompt",
	}, nil)

	if err == nil {
		t.Fatal("expected codex process failure")
	}
	if !strings.Contains(result.PublicReport, "Safe partial result") {
		t.Fatalf("public report missing safe agent output: %q", result.PublicReport)
	}
	if strings.Contains(result.PublicReport, "/tmp/private") {
		t.Fatalf("public report leaked local path: %q", result.PublicReport)
	}
}

func TestLocalRunnerReturnsSafeProcessFailureSummaryWhenCodexExitsBeforeAgentMessage(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	artifactsDir := t.TempDir()
	codex := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
printf '%s\n' 'safe stderr line before crash' >&2
printf '%s\n' 'log at /tmp/private/job.log and OPENAI_API_KEY=sk-proj-secret' >&2
exit 2
`
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := (LocalRunner{CodexBinary: codex}).RunCodex(context.Background(), CodexInput{
		Repo:      config.RepoConfig{},
		Workspace: sandbox.Workspace{RepoDir: dir, CodexHome: codexHome, ArtifactsDir: artifactsDir},
		Prompt:    "test prompt",
	}, nil)

	if err == nil {
		t.Fatal("expected codex process failure")
	}
	for _, want := range []string{"Codex process failed before completing the task.", "Exit code: 2", "safe stderr line before crash"} {
		if !strings.Contains(result.PublicReport, want) {
			t.Fatalf("public report missing %q: %q", want, result.PublicReport)
		}
	}
	for _, leaked := range []string{"/tmp/private", "OPENAI_API_KEY", "sk-proj-secret"} {
		if strings.Contains(result.PublicReport, leaked) {
			t.Fatalf("public report leaked %q: %q", leaked, result.PublicReport)
		}
	}
	stderr, err := os.ReadFile(filepath.Join(artifactsDir, "internal", "codex-stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stderr), "OPENAI_API_KEY=sk-proj-secret") {
		t.Fatalf("internal stderr diagnostic missing raw failure output: %q", string(stderr))
	}
}

func TestLocalRunnerReportsCodexOutputActivity(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	codex := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"status: completed"}}'
`
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	activity := 0

	_, err := (LocalRunner{CodexBinary: codex}).RunCodex(context.Background(), CodexInput{
		Repo:      config.RepoConfig{},
		Workspace: sandbox.Workspace{RepoDir: dir, CodexHome: codexHome},
		Prompt:    "test prompt",
	}, func() { activity++ })

	if err != nil {
		t.Fatal(err)
	}
	if activity == 0 {
		t.Fatal("expected codex output activity callback")
	}
}

func TestLocalRunnerReturnsSafeSummaryForFailedTestCommand(t *testing.T) {
	dir := t.TempDir()
	runner := LocalRunner{}

	result, err := runner.RunTests(context.Background(), dir, []string{"printf 'safe failure detail'; exit 1"}, nil)

	if err == nil {
		t.Fatal("expected test command failure")
	}
	if result.Passed {
		t.Fatalf("result=%#v", result)
	}
	if !strings.Contains(result.Output, "Gateway verification failed") ||
		!strings.Contains(result.Output, "- Command 1: failed with exit code 1") ||
		!strings.Contains(result.Output, "Safe failure output:\nsafe failure detail") {
		t.Fatalf("test output missing failure summary: %q", result.Output)
	}
	if strings.Contains(result.Output, "/tmp/") {
		t.Fatalf("test output leaked local path: %q", result.Output)
	}
}

func TestLocalRunnerReportsOnlyTheFailedTestCommandAsFailed(t *testing.T) {
	dir := t.TempDir()
	runner := LocalRunner{}

	result, err := runner.RunTests(context.Background(), dir, []string{
		"printf ok",
		"printf 'safe failure detail'; printf ' /tmp/private OPENAI_API_KEY=sk-proj-secret' >&2; exit 7",
		"printf should-not-run",
	}, nil)

	if err == nil {
		t.Fatal("expected test command failure")
	}
	if result.Passed {
		t.Fatalf("result=%#v", result)
	}
	for _, want := range []string{
		"Gateway verification failed",
		"- Command 1: passed",
		"- Command 2: failed with exit code 7",
		"Safe failure output:",
		"safe failure detail",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("test output missing %q: %q", want, result.Output)
		}
	}
	for _, unwanted := range []string{"Command 1: failed", "Command 3", "/tmp/private", "OPENAI_API_KEY", "sk-proj-secret"} {
		if strings.Contains(result.Output, unwanted) {
			t.Fatalf("test output contained %q: %q", unwanted, result.Output)
		}
	}
}

func TestLocalRunnerIncludesTailOfLongFailedTestOutput(t *testing.T) {
	dir := t.TempDir()
	runner := LocalRunner{}

	result, err := runner.RunTests(context.Background(), dir, []string{
		"for i in $(seq 1 20); do printf 'passing line %02d\\n' \"$i\"; done; printf 'final failure detail\\n'; exit 1",
	}, nil)

	if err == nil {
		t.Fatal("expected test command failure")
	}
	for _, want := range []string{"passing line 01", "passing line 20", "final failure detail"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("test output missing %q: %q", want, result.Output)
		}
	}
}

func TestGitDiffScannerIncludesCommittedAndUncommittedChanges(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	runGitForWorkerTest(t, dir, "init", "-b", "main")
	runGitForWorkerTest(t, dir, "config", "user.name", "Test")
	runGitForWorkerTest(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForWorkerTest(t, dir, "add", "README.md")
	runGitForWorkerTest(t, dir, "commit", "-m", "base")
	runGitForWorkerTest(t, dir, "checkout", "-b", "codex/issue-1")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := GitDiffScanner{}.ChangedFiles(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files, "README.md") || !slices.Contains(files, "new.txt") {
		t.Fatalf("files=%v", files)
	}
}

func runGitForWorkerTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, string(out))
	}
}
