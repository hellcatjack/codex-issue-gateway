package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (
			delivery_id TEXT PRIMARY KEY,
			event_type TEXT NOT NULL,
			repo_full_name TEXT NOT NULL,
			issue_number INTEGER NOT NULL,
			actor TEXT NOT NULL,
			received_at TEXT NOT NULL,
			body_sha256 TEXT NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			delivery_id TEXT NOT NULL,
			repo_full_name TEXT NOT NULL,
			issue_number INTEGER NOT NULL,
			comment_id INTEGER NOT NULL DEFAULT 0,
			actor TEXT NOT NULL,
			command TEXT NOT NULL,
			flags_json TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			base_branch TEXT NOT NULL DEFAULT '',
			work_branch TEXT NOT NULL DEFAULT '',
			pr_number INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT '',
			worker_heartbeat_at TEXT NOT NULL DEFAULT '',
			job_activity_at TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			from_state TEXT NOT NULL,
			to_state TEXT NOT NULL,
			reason TEXT NOT NULL,
			public_message TEXT NOT NULL,
			internal_message TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS job_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS plan_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_full_name TEXT NOT NULL,
			issue_number INTEGER NOT NULL,
			issue_hash TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			assumptions_json TEXT NOT NULL,
			acceptance_criteria_json TEXT NOT NULL,
			open_questions_json TEXT NOT NULL,
			ready_for_implementation INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RecordDelivery(ctx context.Context, delivery Delivery) (Delivery, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO webhook_deliveries
		(delivery_id, event_type, repo_full_name, issue_number, actor, received_at, body_sha256, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.ID, delivery.EventType, delivery.RepoFullName, delivery.IssueNumber, delivery.Actor, now, delivery.BodySHA256, "received")
	if err != nil {
		return Delivery{}, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Delivery{}, err
	}
	delivery.Duplicate = rows == 0
	return delivery, nil
}

func (s *Store) CreateJob(ctx context.Context, input CreateJobInput) (Job, error) {
	now := time.Now()
	job := Job{
		ID:            fmt.Sprintf("job_%d", now.UnixNano()),
		DeliveryID:    input.DeliveryID,
		RepoFullName:  input.RepoFullName,
		IssueNumber:   input.IssueNumber,
		CommentID:     input.CommentID,
		Actor:         input.Actor,
		Command:       input.Command,
		FlagsJSON:     input.FlagsJSON,
		State:         StateQueued,
		BaseBranch:    input.BaseBranch,
		WorkBranch:    input.WorkBranch,
		CreatedAt:     now,
		JobActivityAt: now,
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO jobs
		(id, delivery_id, repo_full_name, issue_number, comment_id, actor, command, flags_json, state, base_branch, work_branch, created_at, job_activity_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.DeliveryID, job.RepoFullName, job.IssueNumber, job.CommentID, job.Actor, job.Command, job.FlagsJSON, job.State, job.BaseBranch, job.WorkBranch, formatTime(job.CreatedAt), formatTime(job.JobActivityAt)); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Store) Transition(ctx context.Context, jobID string, from, to State, reason, publicMessage, internalMessage string) error {
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE jobs
		SET state = ?, job_activity_at = ?, worker_heartbeat_at = ?
		WHERE id = ? AND state = ?`,
		to, formatTime(now), formatTime(now), jobID, from)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("job %s is not in state %s", jobID, from)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO job_events
		(job_id, from_state, to_state, reason, public_message, internal_message, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		jobID, from, to, reason, publicMessage, internalMessage, formatTime(now))
	return err
}

func (s *Store) GetJob(ctx context.Context, jobID string) (Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, delivery_id, repo_full_name, issue_number, comment_id, actor, command,
		flags_json, state, base_branch, work_branch, pr_number, created_at, worker_heartbeat_at, job_activity_at, last_error
		FROM jobs WHERE id = ?`, jobID)
	return scanJob(row)
}

func (s *Store) JobsByIssue(ctx context.Context, repoFullName string, issueNumber int) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, delivery_id, repo_full_name, issue_number, comment_id, actor, command,
		flags_json, state, base_branch, work_branch, pr_number, created_at, worker_heartbeat_at, job_activity_at, last_error
		FROM jobs WHERE repo_full_name = ? AND issue_number = ? ORDER BY created_at`, repoFullName, issueNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) SavePlanArtifact(ctx context.Context, plan PlanArtifact) error {
	assumptions, err := json.Marshal(plan.Assumptions)
	if err != nil {
		return err
	}
	criteria, err := json.Marshal(plan.AcceptanceCriteria)
	if err != nil {
		return err
	}
	questions, err := json.Marshal(plan.OpenQuestions)
	if err != nil {
		return err
	}
	createdAt := plan.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ready := 0
	if plan.ReadyForImplementation {
		ready = 1
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO plan_artifacts
		(repo_full_name, issue_number, issue_hash, base_branch, assumptions_json, acceptance_criteria_json, open_questions_json, ready_for_implementation, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.RepoFullName, plan.IssueNumber, plan.IssueHash, plan.BaseBranch, string(assumptions), string(criteria), string(questions), ready, formatTime(createdAt))
	return err
}

func (s *Store) LatestReadyPlan(ctx context.Context, repoFullName string, issueNumber int, issueHash string) (PlanArtifact, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, repo_full_name, issue_number, issue_hash, base_branch,
		assumptions_json, acceptance_criteria_json, open_questions_json, ready_for_implementation, created_at
		FROM plan_artifacts
		WHERE repo_full_name = ? AND issue_number = ? AND issue_hash = ? AND ready_for_implementation = 1
		ORDER BY created_at DESC, id DESC LIMIT 1`, repoFullName, issueNumber, issueHash)
	plan, err := scanPlan(row)
	if err == sql.ErrNoRows {
		return PlanArtifact{}, false, nil
	}
	if err != nil {
		return PlanArtifact{}, false, err
	}
	return plan, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var state string
	var createdAt, heartbeatAt, activityAt string
	if err := row.Scan(&job.ID, &job.DeliveryID, &job.RepoFullName, &job.IssueNumber, &job.CommentID, &job.Actor,
		&job.Command, &job.FlagsJSON, &state, &job.BaseBranch, &job.WorkBranch, &job.PRNumber, &createdAt,
		&heartbeatAt, &activityAt, &job.LastError); err != nil {
		return Job{}, err
	}
	job.State = State(state)
	job.CreatedAt = parseTime(createdAt)
	job.WorkerHeartbeatAt = parseTime(heartbeatAt)
	job.JobActivityAt = parseTime(activityAt)
	return job, nil
}

func scanPlan(row rowScanner) (PlanArtifact, error) {
	var plan PlanArtifact
	var assumptions, criteria, questions string
	var ready int
	var createdAt string
	if err := row.Scan(&plan.ID, &plan.RepoFullName, &plan.IssueNumber, &plan.IssueHash, &plan.BaseBranch,
		&assumptions, &criteria, &questions, &ready, &createdAt); err != nil {
		return PlanArtifact{}, err
	}
	_ = json.Unmarshal([]byte(assumptions), &plan.Assumptions)
	_ = json.Unmarshal([]byte(criteria), &plan.AcceptanceCriteria)
	_ = json.Unmarshal([]byte(questions), &plan.OpenQuestions)
	plan.ReadyForImplementation = ready == 1
	plan.CreatedAt = parseTime(createdAt)
	return plan, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
