package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

func TestCreateJobDirsUsesIsolatedLayout(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalWD)
	root := "jobs"
	ws, err := CreateWorkspace(root, "job_123")
	if err != nil {
		t.Fatal(err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{ws.RepoDir, ws.CodexHome, ws.ArtifactsDir, ws.TempDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("dir %s info=%v err=%v", dir, info, err)
		}
		if !filepath.IsAbs(dir) {
			t.Fatalf("dir %s is not absolute", dir)
		}
	}
	if filepath.Dir(ws.RepoDir) != filepath.Join(absRoot, "job_123") {
		t.Fatalf("repo dir outside job root: %s", ws.RepoDir)
	}
}

func TestSafeWorkBranch(t *testing.T) {
	branch, err := WorkBranch(config.RepoConfig{FullName: "owner/repo"}, 7, "job_abc", "")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "codex/issue-7-job-abc" {
		t.Fatalf("branch=%q", branch)
	}
	if _, err := WorkBranch(config.RepoConfig{}, 7, "job", "../main"); err == nil {
		t.Fatalf("expected unsafe branch error")
	}
}

func TestCommitAllCreatesCommitForWorkspaceChanges(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	runGitForTest(t, source, "init", "-b", "main")
	runGitForTest(t, source, "config", "user.name", "Test")
	runGitForTest(t, source, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, source, "add", "README.md")
	runGitForTest(t, source, "commit", "-m", "initial")

	ws, err := CreateWorkspace(t.TempDir(), "job_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareRepository(ctx, config.RepoConfig{CloneURL: source}, ws, "main", "codex/issue-1-job-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.RepoDir, "README.md"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	committed, err := CommitAll(ctx, ws.RepoDir, config.CommitAuthor{Name: "Codex", Email: "codex@example.com"}, "Codex changes")
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("expected commit")
	}
}

func TestPrepareCodexHomeCopiesOnlyAuthFile(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{"token":"redacted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "history.jsonl"), []byte("do not copy"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PrepareCodexHome(source, target); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(target, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"token":"redacted"}` {
		t.Fatalf("auth content = %q", got)
	}
	info, err := os.Stat(filepath.Join(target, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("auth mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(target, "history.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("history file copied or unexpected error: %v", err)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, string(out))
	}
}
