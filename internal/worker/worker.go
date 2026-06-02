package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/commands"
	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/diffpolicy"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/issuecontext"
	"github.com/hellcatjack/codex-issue-gateway/internal/issueimage"
	"github.com/hellcatjack/codex-issue-gateway/internal/publicartifact"
	"github.com/hellcatjack/codex-issue-gateway/internal/publicreport"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
	"github.com/hellcatjack/codex-issue-gateway/internal/sandbox"
)

type CodexRunner interface {
	RunCodex(ctx context.Context, input CodexInput, onActivity func()) (CodexResult, error)
	RunCommands(ctx context.Context, repoDir string, commands []string, onActivity func()) (CommandResult, error)
	RunTests(ctx context.Context, repoDir string, commands []string, onActivity func()) (TestResult, error)
}

type DiffScanner interface {
	ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error)
}

type IssueImageCollector interface {
	Collect(ctx context.Context, snapshot issuecontext.Snapshot, outputDir string) (issueimage.Context, error)
}

type installationTokenProvider interface {
	InstallationToken(ctx context.Context) (string, error)
}

type Worker struct {
	Queue          *queue.Store
	GitHub         github.Client
	Runner         CodexRunner
	Diff           DiffScanner
	ImageCollector IssueImageCollector
	JobRoot        string
	Repo           config.RepoConfig
	Config         *config.Config
}

const commandActor = "hellcatjack"

type CodexInput struct {
	Job        queue.Job
	Repo       config.RepoConfig
	Workspace  sandbox.Workspace
	Prompt     string
	ImageFiles []string
}

type CodexResult struct {
	Status                 string
	Summary                string
	PublicReport           string
	ReadyForImplementation bool
	AcceptanceCriteria     []string
	BlockingReasons        []string
}

type CommandResult struct {
	Passed bool
	Output string
}

type TestResult struct {
	Passed bool
	Output string
}

type preparedPrompt struct {
	Text       string
	ImageFiles []string
}

const (
	screenshotArtifactRoot = ".codex-gateway-artifacts"
	screenshotArtifactDir  = ".codex-gateway-artifacts/screenshots"
	maxScreenshotArtifacts = 12
	maxScreenshotBytes     = 10 * 1024 * 1024
)

func (w *Worker) RunOne(ctx context.Context) error {
	job, ok, err := w.Queue.LeaseNext(ctx)
	if err != nil || !ok {
		return err
	}
	ws, err := sandbox.CreateWorkspace(w.JobRoot, job.ID)
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	repo, err := w.repoFor(job)
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	if err := sandbox.PrepareCodexHome(repo.Codex.AuthSourceDir, ws.CodexHome); err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	branch, err := sandbox.WorkBranch(repo, job.IssueNumber, job.DeliveryID, job.WorkBranch)
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	job.WorkBranch = branch
	if repo.CloneURL != "" || repo.LocalFixturePath != "" {
		if err := sandbox.PrepareRepository(ctx, repo, ws, job.BaseBranch, job.WorkBranch); err != nil {
			_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
			return err
		}
	}
	if err := w.prepareAgentWorkspace(ctx, repo, job, ws); err != nil {
		return err
	}
	switch job.Command {
	case "plan":
		return w.runPlan(ctx, repo, job, ws)
	case "implement", "fix":
		return w.runImplementation(ctx, repo, job, ws)
	default:
		return w.Queue.SetState(ctx, job.ID, queue.StateFailed, "unsupported command")
	}
}

func (w *Worker) prepareAgentWorkspace(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace) error {
	if len(repo.AgentSetupCommands) == 0 {
		return nil
	}
	result, err := w.Runner.RunCommands(ctx, ws.RepoDir, repo.AgentSetupCommands, func() {
		_ = w.Queue.TouchActivity(ctx, job.ID)
	})
	if err != nil || !result.Passed {
		report := result.Output
		if err != nil {
			return w.codexFailure(ctx, repo, job, ws, "setup", report, err)
		}
		return w.codexFailure(ctx, repo, job, ws, "setup", report, errors.New("setup_failed"))
	}
	return nil
}

func (w *Worker) runPlan(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace) error {
	if err := w.Queue.Transition(ctx, job.ID, queue.StateStarting, queue.StatePlanning, "planning", "planning started", ""); err != nil {
		return err
	}
	prepared, err := w.promptForJob(ctx, repo, job, ws, planInstruction())
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	result, err := w.Runner.RunCodex(ctx, CodexInput{Job: job, Repo: repo, Workspace: ws, Prompt: prepared.Text, ImageFiles: prepared.ImageFiles}, func() {
		_ = w.Queue.TouchActivity(ctx, job.ID)
	})
	if err != nil {
		return w.codexFailure(ctx, repo, job, ws, "plan", result.PublicReport, err)
	}
	if result.Status == "needs_plan_revision" {
		_ = w.comment(ctx, job, "Codex 需要补充计划。\n\n- 状态: `waiting_human`\n- 详情已保存在内部审计日志中。")
		return w.Queue.SetState(ctx, job.ID, queue.StateWaitingHuman, "needs_plan_revision")
	}
	result.PublicReport = appendPublicSection(result.PublicReport, w.publishScreenshotArtifacts(job, ws))
	if files, err := w.changedFiles(ctx, ws.RepoDir, job.BaseBranch); err == nil {
		result.PublicReport = appendPublicSection(result.PublicReport, publicartifact.Build(ws.RepoDir, files, repo.DenyPaths))
	}
	if err := w.Queue.SavePlanArtifact(ctx, queue.PlanArtifact{
		RepoFullName:           job.RepoFullName,
		IssueNumber:            job.IssueNumber,
		IssueHash:              job.ID,
		BaseBranch:             job.BaseBranch,
		AcceptanceCriteria:     result.AcceptanceCriteria,
		ReadyForImplementation: result.ReadyForImplementation,
	}); err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	_ = w.comment(ctx, job, planComment(result))
	_ = ws
	return w.Queue.SetState(ctx, job.ID, queue.StateDone, "")
}

func (w *Worker) runImplementation(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace) error {
	if err := w.Queue.Transition(ctx, job.ID, queue.StateStarting, queue.StateImplementing, "implementing", "implementation started", ""); err != nil {
		return err
	}
	prepared, err := w.promptForJob(ctx, repo, job, ws, "Implement the approved plan.")
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	result, err := w.Runner.RunCodex(ctx, CodexInput{Job: job, Repo: repo, Workspace: ws, Prompt: prepared.Text, ImageFiles: prepared.ImageFiles}, func() {
		_ = w.Queue.TouchActivity(ctx, job.ID)
	})
	if err != nil {
		return w.codexFailure(ctx, repo, job, ws, "implement", result.PublicReport, err)
	}
	if result.Status == "needs_plan_revision" {
		_ = w.comment(ctx, job, "Codex 需要修订计划。\n\n- 状态: `waiting_human`\n- 详情已保存在内部审计日志中。")
		return w.Queue.SetState(ctx, job.ID, queue.StateWaitingHuman, "needs_plan_revision")
	}
	result.PublicReport = appendPublicSection(result.PublicReport, w.publishScreenshotArtifacts(job, ws))
	if err := w.Queue.SetState(ctx, job.ID, queue.StateTesting, ""); err != nil {
		return err
	}
	tests, err := w.Runner.RunTests(ctx, ws.RepoDir, repo.TestCommands, func() { _ = w.Queue.TouchActivity(ctx, job.ID) })
	if err != nil || !tests.Passed {
		report := appendPublicSection(result.PublicReport, tests.Output)
		if err != nil {
			return w.codexFailure(ctx, repo, job, ws, "testing", report, err)
		}
		return w.codexFailure(ctx, repo, job, ws, "testing", report, errors.New("tests_failed"))
	}
	result.PublicReport = appendGatewayVerification(result.PublicReport, repo.TestCommands)
	files, err := w.changedFiles(ctx, ws.RepoDir, job.BaseBranch)
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	diff := diffpolicy.Evaluate(diffpolicy.Input{ChangedFiles: files, DenyPaths: repo.DenyPaths, ReviewRequiredPaths: repo.ReviewRequiredPaths})
	if !diff.Allowed {
		return w.Queue.SetState(ctx, job.ID, queue.StateFailed, diff.Reason)
	}
	result.PublicReport = appendPublicSection(result.PublicReport, publicartifact.Build(ws.RepoDir, files, repo.DenyPaths))
	if err := w.Queue.SetState(ctx, job.ID, queue.StateCreatingPR, ""); err != nil {
		return err
	}
	if isGitWorkspace(ws.RepoDir) {
		committed, err := sandbox.CommitAll(ctx, ws.RepoDir, repo.CommitAuthor, "Codex changes for issue #"+itoa(job.IssueNumber))
		if err != nil {
			_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
			return err
		}
		if !committed {
			_ = w.comment(ctx, job, implementationNoChangesComment(result))
			return w.Queue.SetState(ctx, job.ID, queue.StateDone, "")
		}
		if err := sandbox.PushBranch(ctx, ws.RepoDir, repo.ForkPushRemote, job.WorkBranch); err != nil {
			_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
			return err
		}
	}
	pr, err := w.GitHub.CreatePullRequest(ctx, github.PullRequestInput{
		RepoFullName: job.RepoFullName,
		Title:        "Codex changes for issue #" + itoa(job.IssueNumber),
		Head:         pullRequestHead(repo, job.WorkBranch),
		Base:         job.BaseBranch,
		Body:         pullRequestBody(job, result),
	})
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	_ = w.Queue.SetPRNumber(ctx, job.ID, pr.Number)
	_ = w.comment(ctx, job, implementationComment(pr.Number, result))
	return w.Queue.SetState(ctx, job.ID, queue.StateDone, "")
}

func planComment(result CodexResult) string {
	return withPublicReport("Codex Plan 完成。\n\n- 状态: `ready_for_implementation`", result.PublicReport)
}

func planInstruction() string {
	return strings.Join([]string{
		"Create an implementation plan.",
		"Write the plan as a Markdown artifact under `docs/superpowers/plans/` with a date-prefixed descriptive filename.",
		"Do not modify runtime code during planning.",
		"Include goal, architecture, task list, changed files, and verification commands in the plan artifact.",
	}, "\n")
}

func implementationComment(prNumber int, result CodexResult) string {
	return withPublicReport("Codex 已创建 PR。\n\n- PR: #"+itoa(prNumber), result.PublicReport)
}

func implementationNoChangesComment(result CodexResult) string {
	return withPublicReport("Codex 已完成检查。\n\n- PR: 未创建（没有代码变更）", result.PublicReport)
}

func pullRequestBody(job queue.Job, result CodexResult) string {
	return withPublicReport("Closes #"+itoa(job.IssueNumber), result.PublicReport)
}

func appendGatewayVerification(report string, commands []string) string {
	lines := []string{"Gateway verification:", "- Configured test commands passed."}
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
	addition := strings.Join(lines, "\n")
	return appendPublicSection(report, addition)
}

func appendPublicSection(report, section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return strings.TrimSpace(report)
	}
	report = strings.TrimSpace(report)
	if report == "" {
		return section
	}
	return report + "\n\n" + section
}

func withPublicReport(base, report string) string {
	report = publicreport.Sanitize(report)
	if report == "" {
		return base
	}
	return base + "\n\nAgent feedback:\n" + report
}

func (w *Worker) codexFailure(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace, phase, report string, runErr error) error {
	report = appendPublicSection(report, w.publishScreenshotArtifacts(job, ws))
	if files, err := w.changedFiles(ctx, ws.RepoDir, job.BaseBranch); err == nil {
		report = appendPublicSection(report, publicartifact.Build(ws.RepoDir, files, repo.DenyPaths))
	}
	lastError := runErr.Error()
	if err := w.commentWithFallback(ctx, job, codexFailureComment(phase, report), codexFailureComment(phase, "")); err != nil {
		lastError = appendInternalError(lastError, "public_comment_failed")
	}
	_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, lastError)
	return runErr
}

func (w *Worker) publishScreenshotArtifacts(job queue.Job, ws sandbox.Workspace) string {
	root := filepath.Join(ws.RepoDir, screenshotArtifactRoot)
	sourceDir := filepath.Join(ws.RepoDir, filepath.FromSlash(screenshotArtifactDir))
	defer func() { _ = os.RemoveAll(root) }()
	baseURL := w.publicBaseURL()
	if baseURL == "" {
		return ""
	}
	if !safeArtifactDirectory(root) || !safeArtifactDirectory(sourceDir) {
		return ""
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	publicDir := filepath.Join(ws.ArtifactsDir, "public")
	if err := os.MkdirAll(publicDir, 0o700); err != nil {
		return ""
	}
	used := map[string]bool{}
	lines := []string{"Visual artifacts:"}
	published := 0
	for _, entry := range entries {
		if published >= maxScreenshotArtifacts {
			break
		}
		sourceName := entry.Name()
		if entry.IsDir() || suspiciousArtifactName(sourceName) {
			continue
		}
		sourcePath := filepath.Join(sourceDir, sourceName)
		info, err := os.Lstat(sourcePath)
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() <= 0 || info.Size() > maxScreenshotBytes || !allowedScreenshotExt(filepath.Ext(sourceName)) {
			continue
		}
		if !looksLikeImage(sourcePath) {
			continue
		}
		publicName := safeScreenshotFileName(sourceName, used)
		if publicName == "" {
			continue
		}
		if err := copyPublicArtifact(filepath.Join(publicDir, publicName), sourcePath); err != nil {
			continue
		}
		lines = append(lines, "- `"+publicName+"`:\n  !["+publicName+"]("+artifactURL(baseURL, job.ID, publicName)+")")
		published++
	}
	if published == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func safeArtifactDirectory(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func (w *Worker) publicBaseURL() string {
	if w.Config == nil {
		return ""
	}
	raw := strings.TrimRight(strings.TrimSpace(w.Config.Server.PublicBaseURL), "/")
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return raw
}

func copyPublicArtifact(destPath, sourcePath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		return err
	}
	return dest.Close()
}

func looksLikeImage(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 512)
	n, _ := file.Read(header)
	return strings.HasPrefix(http.DetectContentType(header[:n]), "image/")
}

func artifactURL(baseURL, jobID, fileName string) string {
	return baseURL + "/artifacts/" + url.PathEscape(jobID) + "/" + url.PathEscape(fileName)
}

func allowedScreenshotExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func safeScreenshotFileName(name string, used map[string]bool) string {
	ext := strings.ToLower(filepath.Ext(name))
	if !allowedScreenshotExt(ext) {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	var b strings.Builder
	lastDash := false
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.', r == '_', r == '-':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	stem = strings.Trim(b.String(), "-")
	if stem == "" {
		stem = "screenshot"
	}
	candidate := strings.ToLower(stem + ext)
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d%s", strings.ToLower(stem), i, ext)
	}
	used[candidate] = true
	return candidate
}

func suspiciousArtifactName(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"secret", "token", "password", "passwd", "private-key", "private_key", "api-key", "api_key", "access-key", "access_key"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func codexFailureComment(phase, report string) string {
	base := "Codex 执行失败。\n\n- 阶段: `" + phase + "`\n- 状态: `failed`\n- 详情已保存在内部审计日志中。"
	return withPublicReport(base, report)
}

func (w *Worker) changedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error) {
	if w.Diff != nil {
		return w.Diff.ChangedFiles(ctx, repoDir, baseBranch)
	}
	return sandbox.ChangedFiles(ctx, repoDir, baseBranch)
}

func (w *Worker) comment(ctx context.Context, job queue.Job, body string) error {
	if w.GitHub == nil {
		return nil
	}
	return w.GitHub.CreateIssueComment(ctx, job.RepoFullName, job.IssueNumber, body)
}

func (w *Worker) commentWithFallback(ctx context.Context, job queue.Job, body, fallback string) error {
	if err := w.comment(ctx, job, body); err != nil {
		if strings.TrimSpace(fallback) != "" && fallback != body {
			if fallbackErr := w.comment(ctx, job, fallback); fallbackErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func appendInternalError(base, marker string) string {
	base = strings.TrimSpace(base)
	marker = strings.TrimSpace(marker)
	if base == "" {
		return marker
	}
	if marker == "" || strings.Contains(base, marker) {
		return base
	}
	return base + "; " + marker
}

func (w *Worker) promptForJob(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace, instruction string) (preparedPrompt, error) {
	if w.GitHub == nil {
		return preparedPrompt{}, fmt.Errorf("github_client_required")
	}
	ghIssue, err := w.GitHub.FetchIssueContext(ctx, job.RepoFullName, job.IssueNumber)
	if err != nil {
		return preparedPrompt{}, err
	}
	snapshot := toIssueSnapshot(ghIssue)
	if snapshot.Locked || (snapshot.State == "closed" && job.Command != string(commands.Status)) {
		return preparedPrompt{}, fmt.Errorf("issue_unavailable")
	}
	source, ok := issuecontext.CommandSource(snapshot, job.CommentID, commandActor)
	if !ok {
		return preparedPrompt{}, fmt.Errorf("command_actor_not_allowed")
	}
	cmds, err := commands.ParseBody(source, commands.Options{AllowedBases: repo.BaseBranches, MaxNoActivityMinutes: 240})
	if err != nil {
		return preparedPrompt{}, err
	}
	if !containsJobCommand(cmds, job.Command) {
		return preparedPrompt{}, fmt.Errorf("command_not_current")
	}
	text := instruction + "\n\n" + executionConstraints(repo) + "\n\nGitHub issue context from collaborators only:\n" + issuecontext.BuildPromptContext(snapshot)
	imageCtx, imageErr := w.collectIssueImages(ctx, snapshot, ws)
	if imageErr != nil {
		text += "\n\nIssue image inputs:\n- Image processing failed safely; continue from the textual issue context."
	} else if section := imageCtx.PromptSection(); section != "" {
		text += "\n\n" + section
	}
	return preparedPrompt{Text: text, ImageFiles: imageCtx.ImageFiles()}, nil
}

func executionConstraints(repo config.RepoConfig) string {
	lines := []string{
		"Gateway execution constraints:",
		"- Run non-interactively and complete with one final response.",
		"- Do not ask the user questions; make conservative assumptions from the issue context and repository.",
		"- Do not wait for manual approval or interactive input.",
		"- Do not include secrets, tokens, credentials, or internal absolute local paths in the final response.",
		"- Do not run dependency installation commands such as `npm install`, `npm ci`, `yarn install`, `pnpm install`, `pip install`, `bundle install`, or external package fetches unless the issue explicitly requires dependency changes.",
		"- If verification needs dependencies, use the workspace as prepared by the gateway and the configured verification commands below.",
	}
	if len(repo.AgentSetupCommands) > 0 {
		lines = append(lines, "", "Gateway has already run these workspace setup commands before Codex starts:")
		lines = appendCommandLines(lines, repo.AgentSetupCommands)
	}
	if len(repo.TestCommands) > 0 {
		lines = append(lines, "", "Configured verification commands:")
		lines = appendCommandLines(lines, repo.TestCommands)
	}
	return strings.Join(lines, "\n")
}

func appendCommandLines(lines []string, commands []string) []string {
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		lines = append(lines, "- `"+command+"`")
	}
	return lines
}

func (w *Worker) collectIssueImages(ctx context.Context, snapshot issuecontext.Snapshot, ws sandbox.Workspace) (issueimage.Context, error) {
	outputDir := filepath.Join(ws.ArtifactsDir, "issue-images")
	if w.ImageCollector != nil {
		return w.ImageCollector.Collect(ctx, snapshot, outputDir)
	}
	collector := issueimage.DefaultCollector(outputDir)
	if provider, ok := w.GitHub.(installationTokenProvider); ok {
		if token, err := provider.InstallationToken(ctx); err == nil {
			collector.BearerToken = token
		}
	}
	return collector.Collect(ctx, snapshot)
}

func (w *Worker) repoFor(job queue.Job) (config.RepoConfig, error) {
	if w.Repo.FullName == job.RepoFullName || (w.Repo.FullName == "" && w.Config == nil) {
		return w.Repo, nil
	}
	if w.Config != nil {
		if repo, ok := w.Config.Repo(job.RepoFullName); ok {
			return repo, nil
		}
	}
	return config.RepoConfig{}, fmt.Errorf("repo_not_configured")
}

func isGitWorkspace(repoDir string) bool {
	info, err := os.Stat(filepath.Join(repoDir, ".git"))
	return err == nil && info.IsDir()
}

func pullRequestHead(repo config.RepoConfig, branch string) string {
	baseOwner := remoteOwner(repo.CloneURL)
	if baseOwner == "" {
		parts := strings.Split(repo.FullName, "/")
		if len(parts) == 2 {
			baseOwner = parts[0]
		}
	}
	pushOwner := remoteOwner(repo.ForkPushRemote)
	if pushOwner == "" || pushOwner == baseOwner {
		return branch
	}
	return pushOwner + ":" + branch
}

func remoteOwner(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if strings.HasPrefix(remote, "git@github.com:") {
		path := strings.TrimPrefix(remote, "git@github.com:")
		parts := strings.Split(path, "/")
		if len(parts) >= 2 {
			return parts[0]
		}
	}
	if strings.HasPrefix(remote, "https://github.com/") {
		path := strings.TrimPrefix(remote, "https://github.com/")
		parts := strings.Split(path, "/")
		if len(parts) >= 2 {
			return parts[0]
		}
	}
	return ""
}

func containsJobCommand(cmds []commands.Command, want string) bool {
	for _, cmd := range cmds {
		if string(cmd.Name) == want {
			return true
		}
	}
	return false
}

func toIssueSnapshot(ghIssue github.IssueContext) issuecontext.Snapshot {
	comments := make([]issuecontext.Comment, 0, len(ghIssue.Comments))
	for _, comment := range ghIssue.Comments {
		comments = append(comments, issuecontext.Comment{
			ID:                comment.ID,
			Author:            comment.Author,
			AuthorAssociation: comment.AuthorAssociation,
			Body:              comment.Body,
		})
	}
	return issuecontext.Snapshot{
		RepoFullName:      ghIssue.RepoFullName,
		Number:            ghIssue.Number,
		Title:             ghIssue.Title,
		Body:              ghIssue.Body,
		Author:            ghIssue.Author,
		AuthorAssociation: ghIssue.AuthorAssociation,
		State:             ghIssue.State,
		Locked:            ghIssue.Locked,
		Labels:            ghIssue.Labels,
		Comments:          comments,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
