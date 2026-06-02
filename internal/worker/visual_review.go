package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/publicreport"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
	"github.com/hellcatjack/codex-issue-gateway/internal/sandbox"
)

type visualReviewOutcome struct {
	Enabled    bool
	Passed     bool
	Report     string
	ImageFiles []string
	LastError  string
}

func (w *Worker) visualReviewAttempts() int {
	if w.Config != nil && w.Config.Worker.VisualReviewAttempts > 0 {
		return w.Config.Worker.VisualReviewAttempts
	}
	return 3
}

func (w *Worker) runVisualReview(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace) (visualReviewOutcome, error) {
	if len(repo.VisualReviewCommands) == 0 {
		return visualReviewOutcome{Enabled: false, Passed: true}, nil
	}
	before := screenshotFileSet(publicScreenshotImageFiles(ws))
	if err := w.Queue.SetState(ctx, job.ID, queue.StateTesting, "visual_review"); err != nil {
		return visualReviewOutcome{}, err
	}
	result, err := w.Runner.RunTests(ctx, ws.RepoDir, repo.VisualReviewCommands, func() {
		_ = w.Queue.TouchActivity(ctx, job.ID)
	})
	screenshotReport := w.publishScreenshotArtifacts(job, ws)
	images := publicScreenshotImageFilesSince(ws, before)
	report := visualReviewReport(repo.VisualReviewCommands, result, screenshotReport, len(images))
	if err != nil || !result.Passed {
		if err != nil {
			return visualReviewOutcome{Enabled: true, Passed: false, Report: report, ImageFiles: images, LastError: appendInternalError(err.Error(), "visual_review_failed")}, nil
		}
		return visualReviewOutcome{Enabled: true, Passed: false, Report: report, ImageFiles: images, LastError: "visual_review_failed"}, nil
	}
	if len(images) == 0 {
		report = appendPublicSection(report, "Gateway visual review did not produce any safe screenshots under `.codex-gateway-artifacts/screenshots/`.")
		return visualReviewOutcome{Enabled: true, Passed: false, Report: report, LastError: "visual_review_missing_screenshots"}, nil
	}
	return visualReviewOutcome{Enabled: true, Passed: true, Report: report, ImageFiles: images}, nil
}

func visualReviewReport(commands []string, result TestResult, screenshotReport string, imageCount int) string {
	lines := []string{"Gateway visual review:"}
	if result.Passed {
		lines = append(lines, "- Configured visual review commands passed.")
	} else {
		lines = append(lines, "- Configured visual review commands did not pass.")
	}
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		line := "- `" + command + "`"
		if publicreport.Sanitize(line) == line {
			lines = append(lines, line)
		}
	}
	if imageCount > 0 {
		lines = append(lines, "- Latest screenshots were captured for Codex inspection.")
	}
	report := strings.Join(lines, "\n")
	report = appendPublicSection(report, result.Output)
	return appendPublicSection(report, screenshotReport)
}

func visualReviewPrompt(basePrompt string, attempt, maxAttempts int, report string) string {
	report = publicreport.Sanitize(report)
	if report == publicreport.Fallback {
		report = "Safe visual review diagnostics were unavailable."
	}
	return strings.Join([]string{
		basePrompt,
		"",
		"Inspect the latest gateway visual review screenshots attached as image inputs.",
		"Do not start preview servers or browser automation inside the Codex sandbox.",
		"Use the screenshots to decide whether the issue is visually satisfied.",
		"If the screenshots satisfy the issue, do not modify files and return one final completed response.",
		"If the screenshots reveal a problem, modify the implementation in the existing workspace, run focused non-browser tests, and return one final completed response.",
		"Do not ask the user questions, do not wait for approval, and do not stop at analysis.",
		"",
		"Gateway visual review attempt " + itoa(attempt) + " of " + itoa(maxAttempts) + ":",
		report,
	}, "\n")
}

func visualReviewSummary(attempts int) string {
	if attempts <= 0 {
		return ""
	}
	return "Gateway visual review:\n- Visual review cycles: " + itoa(attempts) + "\n- Latest safe screenshots were inspected by Codex before completion."
}

func publicScreenshotImageFilesSince(ws sandbox.Workspace, before map[string]bool) []string {
	files := publicScreenshotImageFiles(ws)
	var out []string
	for _, file := range files {
		if !before[filepath.Base(file)] {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out
}

func screenshotFileSet(files []string) map[string]bool {
	set := make(map[string]bool, len(files))
	for _, file := range files {
		set[filepath.Base(file)] = true
	}
	return set
}

func workspacePatchFingerprint(ctx context.Context, repoDir string) (string, error) {
	if !isGitWorkspace(repoDir) {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, "git", "diff", "--binary", "--no-ext-diff", "--")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", errors.New(string(exitErr.Stderr))
		}
		return "", err
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:]), nil
}
