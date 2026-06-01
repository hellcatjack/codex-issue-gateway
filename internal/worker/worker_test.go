package worker

import (
	"context"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
)

func TestWorkerPlanStoresReadyArtifactAndComments(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
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
}

func TestWorkerNeedsPlanRevisionReleasesLease(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
	deps.Runner.CodexResult = CodexResult{Status: "needs_plan_revision", BlockingReasons: []string{"missing acceptance criteria"}}
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
}

func TestWorkerCreatesPRAfterTestsAndDiffPass(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "implement")
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
	if deps.GitHub.PullRequests[0].Head != "codex/issue-2-delivery-implement" {
		t.Fatalf("pr head = %q", deps.GitHub.PullRequests[0].Head)
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
	CodexResult CodexResult
	TestResult  TestResult
}

func (r *fakeRunner) RunCodex(ctx context.Context, input CodexInput) (CodexResult, error) {
	return r.CodexResult, nil
}

func (r *fakeRunner) RunTests(ctx context.Context, repoDir string, commands []string, onActivity func()) (TestResult, error) {
	if onActivity != nil {
		onActivity()
	}
	return r.TestResult, nil
}

type fakeDiff struct {
	Files []string
}

func (d *fakeDiff) ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error) {
	return d.Files, nil
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
		Actor:        "hellcatjack",
		Command:      command,
		BaseBranch:   "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}
