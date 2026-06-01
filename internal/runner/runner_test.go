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
	for _, want := range []string{"exec", "--sandbox workspace-write", "--ask-for-approval never", "--ephemeral", "--json", "-"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if cmd.Env["CODEX_HOME"] != "/tmp/job/codex-home" {
		t.Fatalf("env = %#v", cmd.Env)
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
