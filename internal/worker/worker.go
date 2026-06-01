package worker

import (
	"context"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/diffpolicy"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
	"github.com/hellcatjack/codex-issue-gateway/internal/sandbox"
)

type CodexRunner interface {
	RunCodex(ctx context.Context, input CodexInput) (CodexResult, error)
	RunTests(ctx context.Context, repoDir string, commands []string, onActivity func()) (TestResult, error)
}

type DiffScanner interface {
	ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error)
}

type Worker struct {
	Queue   *queue.Store
	GitHub  github.Client
	Runner  CodexRunner
	Diff    DiffScanner
	JobRoot string
	Repo    config.RepoConfig
}

type CodexInput struct {
	Job    queue.Job
	Prompt string
}

type CodexResult struct {
	Status                 string
	Summary                string
	ReadyForImplementation bool
	AcceptanceCriteria     []string
	BlockingReasons        []string
}

type TestResult struct {
	Passed bool
	Output string
}

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
	branch, err := sandbox.WorkBranch(w.Repo, job.IssueNumber, job.DeliveryID, job.WorkBranch)
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	job.WorkBranch = branch
	switch job.Command {
	case "plan":
		return w.runPlan(ctx, job, ws)
	case "implement", "fix":
		return w.runImplementation(ctx, job, ws)
	default:
		return w.Queue.SetState(ctx, job.ID, queue.StateFailed, "unsupported command")
	}
}

func (w *Worker) runPlan(ctx context.Context, job queue.Job, ws sandbox.Workspace) error {
	if err := w.Queue.Transition(ctx, job.ID, queue.StateStarting, queue.StatePlanning, "planning", "planning started", ""); err != nil {
		return err
	}
	result, err := w.Runner.RunCodex(ctx, CodexInput{Job: job, Prompt: "Create an implementation plan."})
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	if result.Status == "needs_plan_revision" {
		_ = w.comment(ctx, job, "Codex 需要补充计划。\n\n- 原因: "+strings.Join(result.BlockingReasons, "; "))
		return w.Queue.SetState(ctx, job.ID, queue.StateWaitingHuman, strings.Join(result.BlockingReasons, "; "))
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
	_ = w.comment(ctx, job, "Codex Plan 完成。\n\n摘要:\n- "+result.Summary)
	_ = ws
	return w.Queue.SetState(ctx, job.ID, queue.StateDone, "")
}

func (w *Worker) runImplementation(ctx context.Context, job queue.Job, ws sandbox.Workspace) error {
	if err := w.Queue.Transition(ctx, job.ID, queue.StateStarting, queue.StateImplementing, "implementing", "implementation started", ""); err != nil {
		return err
	}
	result, err := w.Runner.RunCodex(ctx, CodexInput{Job: job, Prompt: "Implement the approved plan."})
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	if result.Status == "needs_plan_revision" {
		_ = w.comment(ctx, job, "Codex 需要修订计划。\n\n- 原因: "+strings.Join(result.BlockingReasons, "; "))
		return w.Queue.SetState(ctx, job.ID, queue.StateWaitingHuman, strings.Join(result.BlockingReasons, "; "))
	}
	if err := w.Queue.SetState(ctx, job.ID, queue.StateTesting, ""); err != nil {
		return err
	}
	tests, err := w.Runner.RunTests(ctx, ws.RepoDir, w.Repo.TestCommands, func() { _ = w.Queue.TouchActivity(ctx, job.ID) })
	if err != nil || !tests.Passed {
		if err != nil {
			_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
			return err
		}
		return w.Queue.SetState(ctx, job.ID, queue.StateFailed, "tests_failed")
	}
	files, err := w.Diff.ChangedFiles(ctx, ws.RepoDir, job.BaseBranch)
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	diff := diffpolicy.Evaluate(diffpolicy.Input{ChangedFiles: files, DenyPaths: w.Repo.DenyPaths, ReviewRequiredPaths: w.Repo.ReviewRequiredPaths})
	if !diff.Allowed {
		return w.Queue.SetState(ctx, job.ID, queue.StateFailed, diff.Reason)
	}
	if err := w.Queue.SetState(ctx, job.ID, queue.StateCreatingPR, ""); err != nil {
		return err
	}
	pr, err := w.GitHub.CreatePullRequest(ctx, github.PullRequestInput{
		RepoFullName: job.RepoFullName,
		Title:        "Codex changes for issue #" + itoa(job.IssueNumber),
		Head:         job.WorkBranch,
		Base:         job.BaseBranch,
		Body:         "Closes #" + itoa(job.IssueNumber),
	})
	if err != nil {
		_ = w.Queue.SetState(ctx, job.ID, queue.StateFailed, err.Error())
		return err
	}
	_ = w.Queue.SetPRNumber(ctx, job.ID, pr.Number)
	_ = w.comment(ctx, job, "Codex 已创建 PR。\n\n- PR: #"+itoa(pr.Number))
	return w.Queue.SetState(ctx, job.ID, queue.StateDone, "")
}

func (w *Worker) comment(ctx context.Context, job queue.Job, body string) error {
	if w.GitHub == nil {
		return nil
	}
	return w.GitHub.CreateIssueComment(ctx, job.RepoFullName, job.IssueNumber, body)
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
