package sandbox

import (
	"context"
	"fmt"
	"io"
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
	absJobRoot, err := filepath.Abs(jobRoot)
	if err != nil {
		return Workspace{}, err
	}
	root := filepath.Join(absJobRoot, jobID)
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

func PrepareCodexHome(sourceDir, targetDir string) error {
	if strings.TrimSpace(sourceDir) == "" {
		return nil
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return err
	}
	source := filepath.Join(sourceDir, "auth.json")
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("codex auth file: %w", err)
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("codex auth file must be a regular file")
	}
	target := filepath.Join(targetDir, "auth.json")
	if targetInfo, err := os.Lstat(target); err == nil && targetInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codex auth target cannot be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, 0o600)
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

func CommitAll(ctx context.Context, repoDir string, author config.CommitAuthor, message string) (bool, error) {
	name := author.Name
	if name == "" {
		name = "Codex"
	}
	email := author.Email
	if email == "" {
		email = "codex@example.com"
	}
	if err := runGit(ctx, repoDir, "config", "user.name", name); err != nil {
		return false, err
	}
	if err := runGit(ctx, repoDir, "config", "user.email", email); err != nil {
		return false, err
	}
	if err := runGit(ctx, repoDir, "add", "-A"); err != nil {
		return false, err
	}
	if err := runGitAllowExit(ctx, repoDir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	if err := runGit(ctx, repoDir, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func PushBranch(ctx context.Context, repoDir, remote, branch string) error {
	return runGit(ctx, repoDir, "push", remote, "HEAD:"+branch)
}

func ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error) {
	var files []string
	seen := map[string]bool{}
	for _, args := range [][]string{
		{"diff", "--name-only", baseBranch + "...HEAD"},
		{"diff", "--name-only"},
		{"diff", "--cached", "--name-only"},
		{"ls-files", "--others", "--exclude-standard"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, string(out))
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				files = append(files, line)
				seen[line] = true
			}
		}
	}
	return files, nil
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

func runGitAllowExit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s exited: %w: %s", strings.Join(args, " "), err, string(out))
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
