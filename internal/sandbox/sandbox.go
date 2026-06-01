package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

type Workspace struct {
	Root         string
	RepoDir      string
	CodexHome    string
	ArtifactsDir string
	TempDir      string
}

func CreateWorkspace(jobRoot, jobID string) (Workspace, error) {
	root := filepath.Join(jobRoot, jobID)
	ws := Workspace{
		Root:         root,
		RepoDir:      filepath.Join(root, "repo"),
		CodexHome:    filepath.Join(root, "codex-home"),
		ArtifactsDir: filepath.Join(root, "artifacts"),
		TempDir:      filepath.Join(root, "tmp"),
	}
	for _, dir := range []string{ws.RepoDir, ws.CodexHome, ws.ArtifactsDir, ws.TempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Workspace{}, err
		}
	}
	return ws, nil
}

func WorkBranch(repo config.RepoConfig, issueNumber int, jobID, requested string) (string, error) {
	_ = repo
	if requested != "" {
		if !safeBranch(requested) {
			return "", fmt.Errorf("unsafe branch")
		}
		return requested, nil
	}
	return fmt.Sprintf("codex/issue-%d-%s", issueNumber, sanitizeBranchPart(jobID)), nil
}

func PrepareRepository(ctx context.Context, repo config.RepoConfig, ws Workspace, baseBranch, workBranch string) error {
	source := repo.CloneURL
	if repo.LocalFixturePath != "" {
		source = repo.LocalFixturePath
	}
	if err := runGit(ctx, "", "clone", source, ws.RepoDir); err != nil {
		return err
	}
	if baseBranch != "" {
		if err := runGit(ctx, ws.RepoDir, "checkout", baseBranch); err != nil {
			return err
		}
	}
	if workBranch != "" {
		if !safeBranch(workBranch) {
			return fmt.Errorf("unsafe work branch")
		}
		if err := runGit(ctx, ws.RepoDir, "checkout", "-B", workBranch); err != nil {
			return err
		}
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, string(out))
	}
	return nil
}

func safeBranch(s string) bool {
	if len(s) == 0 || len(s) > 80 || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '/' {
			continue
		}
		return false
	}
	return true
}

func sanitizeBranchPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return strings.Trim(b.String(), "-")
}
