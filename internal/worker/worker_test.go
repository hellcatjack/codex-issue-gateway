package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/issuecontext"
	"github.com/hellcatjack/codex-issue-gateway/internal/issueimage"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
)

func TestWorkerPlanStoresReadyArtifactAndComments(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	deps.seedIssueContext(job, "/codex plan")
	deps.Runner.CodexResult = CodexResult{
		Status:                 "completed",
		Summary:                "Plan ready",
		ReadyForImplementation: true,
		AcceptanceCriteria:     []string{"go test ./... passes"},
	}
	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateDone {
		t.Fatalf("state=%s", got.State)
	}
	if len(deps.GitHub.Comments) == 0 {
		t.Fatalf("expected plan comment")
	}
	if !strings.Contains(deps.Runner.LastInput.Prompt, "docs/superpowers/plans") {
		t.Fatalf("plan prompt missing persisted artifact instruction: %s", deps.Runner.LastInput.Prompt)
	}
	if !strings.Contains(deps.Runner.LastInput.Prompt, "Do not modify runtime code during planning") {
		t.Fatalf("plan prompt missing planning scope boundary: %s", deps.Runner.LastInput.Prompt)
	}
}

func TestWorkerNeedsPlanRevisionReleasesLease(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.CodexResult = CodexResult{Status: "needs_plan_revision", BlockingReasons: []string{"OPENAI_API_KEY=sk-proj-secret /home/hellcat/.env"}}
	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateWaitingHuman || got.WorkerHeartbeatAt.IsZero() {
		t.Fatalf("job=%#v", got)
	}
	if len(deps.GitHub.Comments) == 0 {
		t.Fatal("expected public comment")
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	if strings.Contains(body, "sk-proj-secret") || strings.Contains(body, "/home/hellcat") {
		t.Fatalf("public comment leaked blocking reason: %q", body)
	}
}

func TestWorkerCreatesPRAfterTestsAndDiffPass(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.CodexResult = CodexResult{Status: "completed", Summary: "Changed README"}
	deps.Runner.TestResult = TestResult{Passed: true}
	deps.Diff.Files = []string{"README.md"}
	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateDone || len(deps.GitHub.PullRequests) != 1 {
		t.Fatalf("job=%#v prs=%#v", got, deps.GitHub.PullRequests)
	}
	if deps.GitHub.PullRequests[0].Head != "hellcatjack:codex/issue-2-delivery-implement" {
		t.Fatalf("pr head = %q", deps.GitHub.PullRequests[0].Head)
	}
}

func TestWorkerRunsAgentSetupBeforeCodex(t *testing.T) {
	deps := newWorkerTestDeps(t)
	deps.Worker.Repo.AgentSetupCommands = []string{"test -d node_modules || cp -a /cache/node_modules ./node_modules"}
	deps.Runner.CommandResult = CommandResult{Passed: true}
	deps.Runner.CodexResult = CodexResult{Status: "completed", Summary: "Changed README"}
	deps.Runner.TestResult = TestResult{Passed: true}
	deps.Diff.Files = []string{"README.md"}
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(deps.Runner.CommandCalls) != 1 {
		t.Fatalf("setup command calls = %#v", deps.Runner.CommandCalls)
	}
	if got := deps.Runner.CommandCalls[0]; len(got) != 1 || got[0] != deps.Worker.Repo.AgentSetupCommands[0] {
		t.Fatalf("setup commands = %#v", got)
	}
	if deps.Runner.CodexCalls != 1 {
		t.Fatalf("codex calls = %d", deps.Runner.CodexCalls)
	}
}

func TestWorkerStopsBeforeCodexWhenAgentSetupFails(t *testing.T) {
	deps := newWorkerTestDeps(t)
	deps.Worker.Repo.AgentSetupCommands = []string{"prepare local dependencies"}
	deps.Runner.CommandResult = CommandResult{
		Passed: false,
		Output: "Gateway workspace preparation failed:\n- `prepare local dependencies`: failed",
	}
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")

	if err := deps.Worker.RunOne(context.Background()); err == nil || err.Error() != "setup_failed" {
		t.Fatalf("expected setup_failed, got %v", err)
	}

	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateFailed {
		t.Fatalf("state=%s", got.State)
	}
	if deps.Runner.CodexCalls != 0 {
		t.Fatalf("codex calls = %d", deps.Runner.CodexCalls)
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Codex 执行失败", "阶段: `setup`", "Gateway workspace preparation failed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup failure comment missing %q: %s", want, body)
		}
	}
}

func TestWorkerImplementationCompletesWithoutPRWhenNoChangesNeeded(t *testing.T) {
	deps := newWorkerTestDeps(t)
	fixture := createGitFixtureRepo(t)
	deps.Worker.Repo.LocalFixturePath = fixture
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.CodexResult = CodexResult{
		Status:       "completed",
		PublicReport: "Summary:\n- Existing implementation already matches the requested behavior.\n\nChanges:\n- No file changes were needed.",
	}
	deps.Runner.TestResult = TestResult{Passed: true}
	deps.Diff.Files = nil

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateDone {
		t.Fatalf("state=%s last_error=%q", got.State, got.LastError)
	}
	if len(deps.GitHub.PullRequests) != 0 {
		t.Fatalf("pull requests = %#v", deps.GitHub.PullRequests)
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Codex 已完成检查", "PR: 未创建", "No file changes were needed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment missing %q: %s", want, body)
		}
	}
}

func TestWorkerPromptIncludesExecutionConstraints(t *testing.T) {
	deps := newWorkerTestDeps(t)
	deps.Worker.Repo.AgentSetupCommands = []string{"test -d node_modules || cp -a /cache/node_modules ./node_modules"}
	deps.Worker.Repo.TestCommands = []string{"npm run test", "git diff --check"}
	deps.Runner.CommandResult = CommandResult{Passed: true}
	deps.Runner.CodexResult = CodexResult{Status: "completed"}
	deps.Runner.TestResult = TestResult{Passed: true}
	deps.Diff.Files = []string{"README.md"}
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	prompt := deps.Runner.LastInput.Prompt
	for _, want := range []string{
		"Do not ask the user questions",
		"Do not include secrets, tokens, credentials, or internal absolute local paths",
		"Do not run dependency installation commands",
		"Gateway has already run these workspace setup commands before Codex starts:",
		"Configured verification commands:",
		"`npm run test`",
		"`git diff --check`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestWorkerPlanCommentDoesNotEchoRawCodexSummary(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	deps.seedIssueContext(job, "/codex plan")
	deps.Runner.CodexResult = CodexResult{
		Status:                 "completed",
		Summary:                "Use secret from /app/devs/repo/.env: ghp_secret",
		PublicReport:           "Summary:\n- Plan updates manifest tests.\n- Internal path /app/devs/repo was inspected.\n- OPENAI_API_KEY=sk-proj-secret",
		ReadyForImplementation: true,
	}
	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	if !strings.Contains(body, "Plan updates manifest tests") {
		t.Fatalf("public comment missing safe report: %q", body)
	}
	if strings.Contains(body, "ghp_secret") || strings.Contains(body, "/app/devs") || strings.Contains(body, "OPENAI_API_KEY") || strings.Contains(body, "Use secret") {
		t.Fatalf("public comment leaked raw Codex summary: %q", body)
	}
}

func TestWorkerPlanCommentIncludesSafeArtifactPreview(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	deps.seedIssueContext(job, "/codex plan")
	deps.Runner.CodexResult = CodexResult{
		Status:                 "completed",
		PublicReport:           "Summary:\n- Created a plan artifact.",
		ReadyForImplementation: true,
	}
	deps.Runner.Files = map[string]string{
		"docs/superpowers/plans/example.md": strings.Join([]string{
			"# Example Implementation Plan",
			"",
			"**Goal:** Show plan content in public comments.",
			"",
			"**Architecture:** Use /app/devs/private internally.",
			"",
			"### Task 1: Add public artifact preview",
			"",
			"**Files:**",
			"- Create: `internal/publicartifact/publicartifact.go`",
			"",
			"Run: `go test ./internal/publicartifact`",
			"",
		}, "\n"),
	}
	deps.Diff.Files = []string{"docs/superpowers/plans/example.md"}

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Artifact preview:", "`docs/superpowers/plans/example.md`", "**Goal:** Show plan content", "### Task 1: Add public artifact preview", "Run: `go test ./internal/publicartifact`"} {
		if !strings.Contains(body, want) {
			t.Fatalf("plan comment missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "/app/devs") {
		t.Fatalf("plan comment leaked local path: %s", body)
	}
}

func TestWorkerPassesIssueImagesToCodex(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	deps.GitHub.Issues[issueKey(job)] = github.IssueContext{
		RepoFullName:      job.RepoFullName,
		Number:            job.IssueNumber,
		Title:             "Task",
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "Use the screenshot.",
		State:             "open",
		Comments: []github.IssueContextComment{
			{ID: job.CommentID, Author: "hellcatjack", AuthorAssociation: "OWNER", Body: "/codex plan\n![Reader state](https://github.com/user-attachments/assets/abc)"},
		},
	}
	imagePath := filepath.Join(t.TempDir(), "issue-image.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := &fakeIssueImageCollector{Context: issueimage.Context{
		Images: []issueimage.Image{{
			Alt:         "Reader state",
			SourceHost:  "github.com",
			ContentType: "image/png",
			Bytes:       3,
			Width:       2,
			Height:      1,
			Path:        imagePath,
		}},
	}}
	deps.Worker.ImageCollector = collector
	deps.Runner.CodexResult = CodexResult{Status: "completed", ReadyForImplementation: true}

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(deps.Runner.LastInput.ImageFiles) != 1 || deps.Runner.LastInput.ImageFiles[0] != imagePath {
		t.Fatalf("image files = %#v", deps.Runner.LastInput.ImageFiles)
	}
	prompt := deps.Runner.LastInput.Prompt
	for _, want := range []string{"Issue image inputs:", `alt "Reader state"`, "attached to Codex as image input"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	for _, leaked := range []string{imagePath, "https://github.com/user-attachments/assets/abc"} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("prompt leaked %q: %s", leaked, prompt)
		}
	}
	if collector.OutputDir == "" {
		t.Fatalf("collector did not receive output dir")
	}
	if collector.Snapshot.Number != job.IssueNumber {
		t.Fatalf("collector snapshot = %#v", collector.Snapshot)
	}
}

func TestWorkerPRIncludesSafeCodexReport(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.CodexResult = CodexResult{
		Status:       "completed",
		Summary:      "raw summary should not be used",
		PublicReport: "Summary:\n- Added PWA manifest metadata.\n\nVerification:\n- `npm run test`: passed\n- log at /tmp/job/output.log",
	}
	deps.Runner.Files = map[string]string{
		"docs/release.md": "**Goal:** Document the release workflow.\n\n### Task 1: Update docs\n\nRun: `go test ./...`\n",
	}
	deps.Runner.TestResult = TestResult{Passed: true}
	deps.Diff.Files = []string{"docs/release.md"}

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(deps.GitHub.PullRequests) != 1 {
		t.Fatalf("pull requests = %#v", deps.GitHub.PullRequests)
	}
	body := deps.GitHub.PullRequests[0].Body
	for _, want := range []string{"Closes #2", "Added PWA manifest metadata", "`npm run test`: passed", "Gateway verification", "`go test ./...`", "Artifact preview:", "`docs/release.md`", "Goal: Document the release workflow"} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{"raw summary", "/tmp/job"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("PR body leaked %q: %s", leaked, body)
		}
	}
}

func TestWorkerImplementationFailureCommentsWithSafeArtifactPreview(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.Err = errors.New("process failed with log at /tmp/job/output.log and OPENAI_API_KEY=sk-proj-secret")
	deps.Runner.Files = map[string]string{
		"docs/failure-preview.md": "# Failure Preview\n\nSafe partial artifact content.\n",
	}
	deps.Diff.Files = []string{"docs/failure-preview.md"}

	if err := deps.Worker.RunOne(context.Background()); err == nil {
		t.Fatal("expected runner error")
	}

	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateFailed {
		t.Fatalf("state=%s", got.State)
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Codex 执行失败", "阶段: `implement`", "Artifact preview:", "`docs/failure-preview.md`", "Safe partial artifact content"} {
		if !strings.Contains(body, want) {
			t.Fatalf("failure comment missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{"/tmp/job", "OPENAI_API_KEY", "sk-proj-secret"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("failure comment leaked %q: %s", leaked, body)
		}
	}
}

func TestWorkerPublishesScreenshotArtifactsInFailureComment(t *testing.T) {
	deps := newWorkerTestDeps(t)
	deps.Worker.Config = &config.Config{Server: config.ServerConfig{PublicBaseURL: "https://gateway.example.test"}}
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.Err = errors.New("process failed")
	deps.Runner.BinaryFiles = map[string][]byte{
		".codex-gateway-artifacts/screenshots/failure.png": tinyPNG(),
	}

	if err := deps.Worker.RunOne(context.Background()); err == nil {
		t.Fatal("expected runner error")
	}

	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Visual artifacts:", "![failure.png](https://gateway.example.test/artifacts/" + job.ID + "/failure.png)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("failure comment missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{".codex-gateway-artifacts", deps.Worker.JobRoot} {
		if strings.Contains(body, leaked) {
			t.Fatalf("failure comment leaked %q: %s", leaked, body)
		}
	}
	published := filepath.Join(deps.Worker.JobRoot, job.ID, "artifacts", "public", "failure.png")
	if data, err := os.ReadFile(published); err != nil || string(data) != string(tinyPNG()) {
		t.Fatalf("published screenshot data err=%v len=%d", err, len(data))
	}
	if _, err := os.Stat(filepath.Join(deps.Worker.JobRoot, job.ID, "repo", ".codex-gateway-artifacts")); !os.IsNotExist(err) {
		t.Fatalf("staging screenshot dir still exists: %v", err)
	}
}

func TestWorkerPublishesScreenshotArtifactsCreatedDuringSuccessfulTests(t *testing.T) {
	deps := newWorkerTestDeps(t)
	deps.Worker.Config = &config.Config{Server: config.ServerConfig{PublicBaseURL: "https://gateway.example.test"}}
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.CodexResult = CodexResult{
		Status:       "completed",
		PublicReport: "Summary:\n- Updated reader placement logic.",
	}
	deps.Runner.TestResult = TestResult{Passed: true}
	deps.Runner.TestBinaryFiles = map[string][]byte{
		".codex-gateway-artifacts/screenshots/reader-1200px.png": tinyPNG(),
	}
	deps.Diff.Files = []string{"README.md"}

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(deps.GitHub.Comments) == 0 {
		t.Fatal("expected implementation comment")
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Codex 已创建 PR", "Visual artifacts:", "![reader-1200px.png](https://gateway.example.test/artifacts/" + job.ID + "/reader-1200px.png)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("implementation comment missing %q: %s", want, body)
		}
	}
	if len(deps.GitHub.PullRequests) != 1 {
		t.Fatalf("pull requests = %#v", deps.GitHub.PullRequests)
	}
	prBody := deps.GitHub.PullRequests[0].Body
	if !strings.Contains(prBody, "![reader-1200px.png](https://gateway.example.test/artifacts/"+job.ID+"/reader-1200px.png)") {
		t.Fatalf("pull request body missing visual artifact: %s", prBody)
	}
	published := filepath.Join(deps.Worker.JobRoot, job.ID, "artifacts", "public", "reader-1200px.png")
	if data, err := os.ReadFile(published); err != nil || string(data) != string(tinyPNG()) {
		t.Fatalf("published screenshot data err=%v len=%d", err, len(data))
	}
}

func TestWorkerRejectsScreenshotSymlinkStagingDir(t *testing.T) {
	deps := newWorkerTestDeps(t)
	deps.Worker.Config = &config.Config{Server: config.ServerConfig{PublicBaseURL: "https://gateway.example.test"}}
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "screenshots"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "screenshots", "failure.png"), tinyPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	deps.Runner.Err = errors.New("process failed")
	deps.Runner.Symlinks = map[string]string{
		".codex-gateway-artifacts": outside,
	}

	if err := deps.Worker.RunOne(context.Background()); err == nil {
		t.Fatal("expected runner error")
	}

	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	if strings.Contains(body, "Visual artifacts:") || strings.Contains(body, "failure.png") {
		t.Fatalf("symlinked staging screenshot was published: %s", body)
	}
	if _, err := os.Stat(filepath.Join(outside, "screenshots", "failure.png")); err != nil {
		t.Fatalf("outside target was touched: %v", err)
	}
}

func TestWorkerImplementationTestFailureCommentsWithSafeSummary(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Runner.CodexResult = CodexResult{
		Status:       "completed",
		PublicReport: "Summary:\n- Updated reader placement logic.",
	}
	deps.Runner.TestResult = TestResult{
		Passed: false,
		Output: "Gateway verification failed:\n- `go test ./...`: failed",
	}
	deps.Runner.Files = map[string]string{
		"docs/failure-preview.md": "# Failure Preview\n\nSafe partial artifact content.\n",
	}
	deps.Diff.Files = []string{"docs/failure-preview.md"}

	if err := deps.Worker.RunOne(context.Background()); err == nil || err.Error() != "tests_failed" {
		t.Fatalf("expected tests_failed error, got %v", err)
	}

	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateFailed {
		t.Fatalf("state=%s", got.State)
	}
	if len(deps.GitHub.Comments) == 0 {
		t.Fatal("expected failure comment")
	}
	body := deps.GitHub.Comments[len(deps.GitHub.Comments)-1].Body
	for _, want := range []string{"Codex 执行失败", "阶段: `testing`", "Updated reader placement logic", "Gateway verification failed", "`go test ./...`: failed", "Artifact preview:", "Safe partial artifact content"} {
		if !strings.Contains(body, want) {
			t.Fatalf("failure comment missing %q: %s", want, body)
		}
	}
}

func TestWorkerFailureRecordsCommentError(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.seedIssueContext(job, "/codex implement")
	deps.Worker.GitHub = failingCommentClient{Client: deps.GitHub, Err: errors.New("github unavailable")}
	deps.Runner.Err = errors.New("process failed")

	if err := deps.Worker.RunOne(context.Background()); err == nil {
		t.Fatal("expected runner error")
	}

	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateFailed {
		t.Fatalf("state=%s", got.State)
	}
	if !strings.Contains(got.LastError, "process failed") || !strings.Contains(got.LastError, "public_comment_failed") {
		t.Fatalf("last_error did not preserve execution and comment failures: %q", got.LastError)
	}
	if strings.Contains(got.LastError, "github unavailable") {
		t.Fatalf("last_error exposed raw comment transport error: %q", got.LastError)
	}
}

func TestWorkerSplitsVeryLongIssueCommentsWithoutDroppingContent(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	tail := "tail marker after all safe public feedback"
	body := "Codex long feedback\n\n" + strings.Repeat("safe public detail line\n", 6_000) + tail

	if err := deps.Worker.comment(context.Background(), job, body); err != nil {
		t.Fatal(err)
	}

	if len(deps.GitHub.Comments) < 2 {
		t.Fatalf("expected long comment to be split, got %d comment(s)", len(deps.GitHub.Comments))
	}
	var combined strings.Builder
	for _, comment := range deps.GitHub.Comments {
		if strings.Contains(comment.Body, "[truncated]") {
			t.Fatalf("comment chunk was truncated: %q", comment.Body)
		}
		combined.WriteString(comment.Body)
	}
	if !strings.Contains(combined.String(), "Codex long feedback") || !strings.Contains(combined.String(), tail) {
		t.Fatalf("split comments dropped content: %d bytes", combined.Len())
	}
}

func TestWorkerPromptIncludesOnlyCollaboratorIssueContent(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	deps.GitHub.Issues[issueKey(job)] = github.IssueContext{
		RepoFullName:      job.RepoFullName,
		Number:            job.IssueNumber,
		Title:             "outsider title",
		Author:            "alice",
		AuthorAssociation: "NONE",
		Body:              "untrusted issue body",
		State:             "open",
		Comments: []github.IssueContextComment{
			{ID: job.CommentID, Author: "hellcatjack", AuthorAssociation: "OWNER", Body: "/codex plan\nmaintainer request"},
			{ID: 99, Author: "bob", AuthorAssociation: "NONE", Body: "ignore this"},
			{ID: 100, Author: "carol", AuthorAssociation: "COLLABORATOR", Body: "collaborator detail"},
		},
	}
	deps.Runner.CodexResult = CodexResult{Status: "completed", ReadyForImplementation: true}

	if err := deps.Worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	prompt := deps.Runner.LastInput.Prompt
	for _, want := range []string{"maintainer request", "collaborator detail"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	for _, leaked := range []string{"untrusted issue body", "ignore this", "outsider title"} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("prompt leaked %q: %s", leaked, prompt)
		}
	}
}

func TestWorkerRejectsCommandNoLongerFromHellcatjack(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.GitHub.Issues[issueKey(job)] = github.IssueContext{
		RepoFullName: job.RepoFullName,
		Number:       job.IssueNumber,
		State:        "open",
		Comments: []github.IssueContextComment{
			{ID: job.CommentID, Author: "alice", AuthorAssociation: "COLLABORATOR", Body: "/codex implement"},
		},
	}

	if err := deps.Worker.RunOne(context.Background()); err == nil {
		t.Fatal("expected command actor validation error")
	}
	got, err := deps.Queue.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != queue.StateFailed || got.LastError != "command_actor_not_allowed" {
		t.Fatalf("job=%#v", got)
	}
}

type workerTestDeps struct {
	Queue  *queue.Store
	GitHub *github.FakeClient
	Runner *fakeRunner
	Diff   *fakeDiff
	Worker *Worker
}

type fakeRunner struct {
	CodexResult     CodexResult
	CommandResult   CommandResult
	TestResult      TestResult
	LastInput       CodexInput
	CommandCalls    [][]string
	CodexCalls      int
	Files           map[string]string
	BinaryFiles     map[string][]byte
	TestBinaryFiles map[string][]byte
	Symlinks        map[string]string
	Err             error
}

func (r *fakeRunner) RunCodex(ctx context.Context, input CodexInput, onActivity func()) (CodexResult, error) {
	r.LastInput = input
	r.CodexCalls++
	if onActivity != nil {
		onActivity()
	}
	for name, body := range r.Files {
		path := filepath.Join(input.Workspace.RepoDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return CodexResult{}, err
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return CodexResult{}, err
		}
	}
	for name, body := range r.BinaryFiles {
		path := filepath.Join(input.Workspace.RepoDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return CodexResult{}, err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return CodexResult{}, err
		}
	}
	for name, target := range r.Symlinks {
		path := filepath.Join(input.Workspace.RepoDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return CodexResult{}, err
		}
		if err := os.Symlink(target, path); err != nil {
			return CodexResult{}, err
		}
	}
	if r.Err != nil {
		return CodexResult{}, r.Err
	}
	return r.CodexResult, nil
}

func (r *fakeRunner) RunCommands(ctx context.Context, repoDir string, commands []string, onActivity func()) (CommandResult, error) {
	if onActivity != nil {
		onActivity()
	}
	r.CommandCalls = append(r.CommandCalls, append([]string(nil), commands...))
	return r.CommandResult, nil
}

func (r *fakeRunner) RunTests(ctx context.Context, repoDir string, commands []string, onActivity func()) (TestResult, error) {
	if onActivity != nil {
		onActivity()
	}
	for name, body := range r.TestBinaryFiles {
		path := filepath.Join(repoDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return TestResult{}, err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return TestResult{}, err
		}
	}
	return r.TestResult, nil
}

type fakeDiff struct {
	Files []string
}

func (d *fakeDiff) ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error) {
	return d.Files, nil
}

type fakeIssueImageCollector struct {
	Context   issueimage.Context
	Err       error
	Snapshot  issuecontext.Snapshot
	OutputDir string
}

func (c *fakeIssueImageCollector) Collect(ctx context.Context, snapshot issuecontext.Snapshot, outputDir string) (issueimage.Context, error) {
	_ = ctx
	c.Snapshot = snapshot
	c.OutputDir = outputDir
	return c.Context, c.Err
}

type failingCommentClient struct {
	github.Client
	Err error
}

func (c failingCommentClient) CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error {
	_ = ctx
	_ = repoFullName
	_ = issueNumber
	_ = body
	return c.Err
}

func newWorkerTestDeps(t *testing.T) workerTestDeps {
	t.Helper()
	store, err := queue.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	gh := github.NewFake()
	runner := &fakeRunner{}
	diff := &fakeDiff{}
	w := &Worker{
		Queue:   store,
		GitHub:  gh,
		Runner:  runner,
		Diff:    diff,
		JobRoot: t.TempDir(),
		Repo: config.RepoConfig{
			FullName:       "funland/foliospace-Library",
			ForkPushRemote: "git@github.com:hellcatjack/foliospace-Library.git",
			BaseBranches:   []string{"main"},
			TestCommands:   []string{"go test ./..."},
			DenyPaths:      []string{".env", "docker-compose.yml"},
			CommitAuthor:   config.CommitAuthor{Name: "Codex", Email: "codex@example.com"},
		},
	}
	return workerTestDeps{Queue: store, GitHub: gh, Runner: runner, Diff: diff, Worker: w}
}

func (d workerTestDeps) createQueuedJob(t *testing.T, command string) queue.Job {
	t.Helper()
	job, err := d.Queue.CreateJob(context.Background(), queue.CreateJobInput{
		DeliveryID:   "delivery-" + command,
		RepoFullName: "funland/foliospace-Library",
		IssueNumber:  2,
		CommentID:    1001,
		Actor:        "hellcatjack",
		Command:      command,
		BaseBranch:   "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (d workerTestDeps) seedIssueContext(job queue.Job, command string) {
	d.GitHub.Issues[issueKey(job)] = github.IssueContext{
		RepoFullName:      job.RepoFullName,
		Number:            job.IssueNumber,
		Title:             "Task",
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "Body",
		State:             "open",
		Comments: []github.IssueContextComment{
			{ID: job.CommentID, Author: "hellcatjack", AuthorAssociation: "OWNER", Body: command},
		},
	}
}

func issueKey(job queue.Job) string {
	return job.RepoFullName + "#" + itoa(job.IssueNumber)
}

func createGitFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitForWorkerTest(t, dir, "init", "-b", "main")
	runGitForWorkerTest(t, dir, "config", "user.name", "Test")
	runGitForWorkerTest(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForWorkerTest(t, dir, "add", "README.md")
	runGitForWorkerTest(t, dir, "commit", "-m", "base")
	return dir
}

func tinyPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
}
