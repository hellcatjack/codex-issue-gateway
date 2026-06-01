package queue

import (
	"context"
	"testing"
	"time"
)

func TestRecordDeliveryIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first, err := store.RecordDelivery(ctx, Delivery{ID: "d1", EventType: "issue_comment", RepoFullName: "owner/repo", IssueNumber: 1, Actor: "alice", BodySHA256: "abc"})
	if err != nil || first.Duplicate {
		t.Fatalf("first delivery = %#v err=%v", first, err)
	}
	second, err := store.RecordDelivery(ctx, Delivery{ID: "d1", EventType: "issue_comment", RepoFullName: "owner/repo", IssueNumber: 1, Actor: "alice", BodySHA256: "abc"})
	if err != nil || !second.Duplicate {
		t.Fatalf("second delivery = %#v err=%v", second, err)
	}
}

func TestStateTransitionRecordsEventAndActivity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	job, err := store.CreateJob(ctx, CreateJobInput{DeliveryID: "d1", RepoFullName: "owner/repo", IssueNumber: 1, Actor: "alice", Command: "plan", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, job.ID, StateQueued, StateStarting, "leased", "worker leased job", "worker-1"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateStarting || got.JobActivityAt.IsZero() {
		t.Fatalf("job = %#v", got)
	}
}

func TestLatestReadyPlanRequiresMatchingIssueHash(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	plan := PlanArtifact{RepoFullName: "owner/repo", IssueNumber: 1, IssueHash: "h1", ReadyForImplementation: true, CreatedAt: time.Now()}
	if err := store.SavePlanArtifact(ctx, plan); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LatestReadyPlan(ctx, "owner/repo", 1, "h1")
	if err != nil || !ok || !got.ReadyForImplementation {
		t.Fatalf("plan=%#v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := store.LatestReadyPlan(ctx, "owner/repo", 1, "changed"); err != nil || ok {
		t.Fatalf("expected no ready plan for changed hash, ok=%v err=%v", ok, err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
