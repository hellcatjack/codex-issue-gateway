package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

func TestCreateJobDirsUsesIsolatedLayout(t *testing.T) {
	root := t.TempDir()
	ws, err := CreateWorkspace(root, "job_123")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{ws.RepoDir, ws.CodexHome, ws.ArtifactsDir, ws.TempDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("dir %s info=%v err=%v", dir, info, err)
		}
	}
	if filepath.Dir(ws.RepoDir) != filepath.Join(root, "job_123") {
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
