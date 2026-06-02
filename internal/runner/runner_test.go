package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

func TestCodexCommandIsNonInteractive(t *testing.T) {
	cmd := BuildCodexCommand(CodexInput{
		CodexBinary: "codex",
		RepoDir:     "/tmp/job/repo",
		CodexHome:   "/tmp/job/codex-home",
		Repo:        config.RepoConfig{Codex: config.CodexConfig{Sandbox: "workspace-write", AskForApproval: "never", Ephemeral: true, JSONEvents: true}},
		Prompt:      "do work",
	})
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"exec", "--sandbox workspace-write", "-c approval_policy=\"never\"", "--json", "-"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "--ephemeral") {
		t.Fatalf("args %q contain Codex CLI flag that is not supported by all installed versions", joined)
	}
	if strings.Contains(joined, "--ask-for-approval") {
		t.Fatalf("args %q contain removed Codex CLI flag", joined)
	}
	if cmd.Env["CODEX_HOME"] != "/tmp/job/codex-home" {
		t.Fatalf("env = %#v", cmd.Env)
	}
	for _, want := range []string{"Public reporting contract:", "repo-relative files", "Do not include local absolute paths", "If UI or browser-visible behavior changed and real screenshots were captured"} {
		if !strings.Contains(cmd.Stdin, want) {
			t.Fatalf("stdin missing %q: %s", want, cmd.Stdin)
		}
	}
}

func TestCodexCommandAttachesIssueImages(t *testing.T) {
	cmd := BuildCodexCommand(CodexInput{
		CodexBinary: "codex",
		RepoDir:     "/tmp/job/repo",
		CodexHome:   "/tmp/job/codex-home",
		Repo:        config.RepoConfig{Codex: config.CodexConfig{Sandbox: "workspace-write", AskForApproval: "never"}},
		Prompt:      "inspect screenshots",
		ImageFiles:  []string{"/tmp/job/artifacts/issue-images/image-1.png", "/tmp/job/artifacts/issue-images/image-2.jpg"},
	})

	got := strings.Join(cmd.Args, "\n")
	for _, want := range []string{
		"--image\n/tmp/job/artifacts/issue-images/image-1.png",
		"--image\n/tmp/job/artifacts/issue-images/image-2.jpg",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args missing %q:\n%s", want, got)
		}
	}
	if cmd.Args[len(cmd.Args)-1] != "-" {
		t.Fatalf("last arg = %q, want stdin marker", cmd.Args[len(cmd.Args)-1])
	}
	if !strings.Contains(cmd.Stdin, "Do not include cached issue image paths or source image URLs") {
		t.Fatalf("stdin missing image reporting contract: %s", cmd.Stdin)
	}
}

func TestWatchdogExpiresOnlyAfterNoActivity(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	wd := Watchdog{NoActivityTimeout: 45 * time.Minute}
	if wd.Expired(now, now.Add(-44*time.Minute)) {
		t.Fatalf("watchdog expired too early")
	}
	if !wd.Expired(now, now.Add(-46*time.Minute)) {
		t.Fatalf("watchdog did not expire stale activity")
	}
}

func TestProcessRunnerReportsActivityFromOutput(t *testing.T) {
	ctx := context.Background()
	var activity int
	result, err := RunProcess(ctx, ProcessInput{
		Name:       "sh",
		Args:       []string{"-c", "printf hello"},
		OnActivity: func(Activity) { activity++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || activity == 0 {
		t.Fatalf("result=%#v activity=%d", result, activity)
	}
}

func TestProcessRunnerDoesNotInheritHostSecrets(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "supersecretvalue")
	result, err := RunProcess(context.Background(), ProcessInput{
		Name: "sh",
		Args: []string{"-c", "env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Stdout, "SECRET_TOKEN") || strings.Contains(result.Stdout, "supersecretvalue") {
		t.Fatalf("process inherited host secret env: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "PATH=") {
		t.Fatalf("process env missing PATH: %s", result.Stdout)
	}
}
