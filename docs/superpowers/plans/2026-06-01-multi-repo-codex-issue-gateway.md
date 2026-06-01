# Multi-Repo Codex Issue Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go-based multi-repo GitHub Issue automation gateway that safely accepts `/codex` commands, queues jobs, runs non-interactive Codex workers in isolated workspaces, enforces tests and diff policy, and creates Pull Requests.

**Architecture:** One Go binary starts the webhook gateway and optional worker loop. Gateway and worker communicate only through SQLite-backed delivery/job state. Repository behavior is driven by YAML policies, so `funland/foliospace-Library` is an integration fixture and sample config, not a hardcoded target.

**Tech Stack:** Go 1.22, `net/http`, `database/sql`, `modernc.org/sqlite`, `gopkg.in/yaml.v3`, `github.com/golang-jwt/jwt/v5`, Git CLI, Codex CLI, GitHub App REST API.

---

## File Structure

- `go.mod`: Go module `github.com/hellcatjack/codex-issue-gateway`.
- `cmd/codex-issue-gateway/main.go`: CLI entry point and process wiring.
- `internal/config/config.go`: config structs, defaults, validation, repo lookup.
- `internal/config/config_test.go`: config validation tests.
- `configs/example.yml`: multi-repo sample config with `funland/foliospace-Library`.
- `internal/commands/parser.go`: `/codex` command parser and flag validation.
- `internal/commands/parser_test.go`: command parsing tests.
- `internal/webhook/signature.go`: GitHub HMAC SHA-256 verification.
- `internal/webhook/event.go`: supported event normalization.
- `internal/webhook/webhook_test.go`: signature and event tests.
- `internal/queue/store.go`: SQLite schema and store API.
- `internal/queue/models.go`: job, delivery, state, artifact, plan models.
- `internal/queue/store_test.go`: idempotency and state transition tests.
- `internal/authz/authz.go`: role, repo, label, readiness, active-job decisions.
- `internal/authz/authz_test.go`: authorization tests.
- `internal/github/client.go`: GitHub interface and real GitHub App client.
- `internal/github/fake.go`: fake GitHub client for tests.
- `internal/github/client_test.go`: token request and API request tests.
- `internal/audit/audit.go`: structured audit logger and redaction helpers.
- `internal/audit/audit_test.go`: log redaction tests.
- `internal/server/server.go`: HTTP routes `/github/webhook`, `/healthz`, `/readyz`.
- `internal/server/server_test.go`: HTTP behavior tests.
- `internal/sandbox/sandbox.go`: job directory and repository workspace preparation.
- `internal/sandbox/sandbox_test.go`: isolated directory and branch tests.
- `internal/runner/codex.go`: non-interactive Codex command assembly and execution.
- `internal/runner/process.go`: process execution with output capture and activity callbacks.
- `internal/runner/watchdog.go`: progress-based watchdog helpers.
- `internal/runner/runner_test.go`: non-interactive and activity tests.
- `internal/diffpolicy/policy.go`: changed file scanning and limits.
- `internal/diffpolicy/policy_test.go`: denylist and review-required tests.
- `internal/worker/worker.go`: job leasing and phase orchestration.
- `internal/worker/worker_test.go`: fake end-to-end worker tests.
- `tests/fixtures/github/issue_comment_created.json`: webhook payload fixture.
- `tests/integration/foliospace_test.go`: guarded local fixture integration test.
- `docs/local-development.md`: local run and test instructions.

## Task 1: Bootstrap Go Module And Config Model

**Files:**
- Create: `go.mod`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `configs/example.yml`

- [ ] **Step 1: Write failing config tests**

```go
package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsDangerFullAccess(t *testing.T) {
	yml := `
server:
  listen: "127.0.0.1:18090"
  max_body_bytes: 2097152
github:
  app_id: 1
  installation_id: 2
  private_key_file: "/tmp/app.pem"
  webhook_secret_file: "/tmp/webhook-secret"
queue:
  dsn: "/tmp/gateway.db"
repos:
  - full_name: "owner/repo"
    clone_url: "git@github.com:owner/repo.git"
    fork_push_remote: "git@github.com:bot/repo.git"
    base_branches: ["main"]
    allowed_actors:
      maintainers: ["alice"]
    deny_paths: [".env"]
    test_commands: ["go test ./..."]
    codex:
      sandbox: "danger-full-access"
`
	_, err := Load(strings.NewReader(yml))
	if err == nil || !strings.Contains(err.Error(), "danger-full-access") {
		t.Fatalf("expected danger-full-access validation error, got %v", err)
	}
}

func TestLoadBuildsRepoIndexAndDefaults(t *testing.T) {
	cfg, err := Load(strings.NewReader(validConfigYAML()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	repo, ok := cfg.Repo("funland/foliospace-Library")
	if !ok {
		t.Fatalf("expected sample repo to be indexed")
	}
	if repo.Codex.Sandbox != "workspace-write" {
		t.Fatalf("sandbox = %q", repo.Codex.Sandbox)
	}
	if cfg.Worker.NoActivityTimeoutMinutes != 45 {
		t.Fatalf("no activity timeout = %d", cfg.Worker.NoActivityTimeoutMinutes)
	}
}

func validConfigYAML() string {
	return `
server:
  listen: "127.0.0.1:18090"
github:
  app_id: 1
  installation_id: 2
  private_key_file: "/tmp/app.pem"
  webhook_secret_file: "/tmp/webhook-secret"
queue:
  dsn: "/tmp/gateway.db"
repos:
  - full_name: "funland/foliospace-Library"
    clone_url: "git@github.com:funland/foliospace-Library.git"
    fork_push_remote: "git@github.com:hellcatjack/foliospace-Library.git"
    base_branches: ["main"]
    allowed_actors:
      maintainers: ["hellcatjack"]
    deny_paths: [".env", "docker-compose.yml"]
    test_commands: ["go test ./..."]
`
}
```

Run: `go test ./internal/config -run 'TestLoad' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Create module and minimal config implementation**

```go
package config

import (
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	GitHub GitHubConfig `yaml:"github"`
	Queue  QueueConfig  `yaml:"queue"`
	Worker WorkerConfig `yaml:"worker"`
	Repos  []RepoConfig `yaml:"repos"`
	index  map[string]RepoConfig
}

type ServerConfig struct {
	Listen       string `yaml:"listen"`
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
}

type GitHubConfig struct {
	AppID             int64  `yaml:"app_id"`
	InstallationID    int64  `yaml:"installation_id"`
	PrivateKeyFile    string `yaml:"private_key_file"`
	WebhookSecretFile string `yaml:"webhook_secret_file"`
}

type QueueConfig struct {
	DSN              string `yaml:"dsn"`
	MaxGlobalRunning int    `yaml:"max_global_running"`
}

type WorkerConfig struct {
	Enabled                   bool           `yaml:"enabled"`
	JobRoot                   string         `yaml:"job_root"`
	StaleLeaseAfterMinutes    int            `yaml:"stale_lease_after_minutes"`
	NoActivityTimeoutMinutes  int            `yaml:"no_activity_timeout_minutes"`
	PhaseNoActivityTimeouts   map[string]int `yaml:"phase_no_activity_timeout_minutes"`
	AbsoluteJobTimeoutMinutes int            `yaml:"absolute_job_timeout_minutes"`
}

type RepoConfig struct {
	FullName                   string            `yaml:"full_name"`
	CloneURL                   string            `yaml:"clone_url"`
	LocalFixturePath           string            `yaml:"local_fixture_path"`
	ForkPushRemote             string            `yaml:"fork_push_remote"`
	BaseBranches               []string          `yaml:"base_branches"`
	ProtectedBranches          []string          `yaml:"protected_branches"`
	RequiredLabelsForImplement []string          `yaml:"required_labels_for_implement"`
	AllowedActors              ActorRoles        `yaml:"allowed_actors"`
	CommitAuthor               CommitAuthor      `yaml:"commit_author"`
	TestCommands               []string          `yaml:"test_commands"`
	DenyPaths                  []string          `yaml:"deny_paths"`
	ReviewRequiredPaths        []string          `yaml:"review_required_paths"`
	Codex                      CodexConfig       `yaml:"codex"`
	Concurrency                RepoConcurrency   `yaml:"concurrency"`
	Labels                     map[string]string `yaml:"labels"`
}

type ActorRoles struct {
	Admins      []string `yaml:"admins"`
	Maintainers []string `yaml:"maintainers"`
	Operators   []string `yaml:"operators"`
	Requesters  []string `yaml:"requesters"`
}

type CommitAuthor struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type CodexConfig struct {
	Sandbox        string `yaml:"sandbox"`
	AskForApproval string `yaml:"ask_for_approval"`
	Ephemeral      bool   `yaml:"ephemeral"`
	JSONEvents     bool   `yaml:"json_events"`
}

type RepoConcurrency struct {
	MaxRunning int `yaml:"max_running"`
}

func Load(r io.Reader) (*Config, error) {
	var cfg Config
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.index = map[string]RepoConfig{}
	for _, repo := range cfg.Repos {
		cfg.index[repo.FullName] = repo
	}
	return &cfg, nil
}

func (c *Config) Repo(fullName string) (RepoConfig, bool) {
	repo, ok := c.index[fullName]
	return repo, ok
}

func (c *Config) applyDefaults() {
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 2 * 1024 * 1024
	}
	if c.Queue.MaxGlobalRunning == 0 {
		c.Queue.MaxGlobalRunning = 1
	}
	if c.Worker.JobRoot == "" {
		c.Worker.JobRoot = "/tmp/codex-issue-gateway/jobs"
	}
	if c.Worker.NoActivityTimeoutMinutes == 0 {
		c.Worker.NoActivityTimeoutMinutes = 45
	}
	if c.Worker.StaleLeaseAfterMinutes == 0 {
		c.Worker.StaleLeaseAfterMinutes = 15
	}
	if c.Worker.AbsoluteJobTimeoutMinutes == 0 {
		c.Worker.AbsoluteJobTimeoutMinutes = 720
	}
	if c.Worker.PhaseNoActivityTimeouts == nil {
		c.Worker.PhaseNoActivityTimeouts = map[string]int{"planning": 30, "implementing": 60, "testing": 45, "creating_pr": 20}
	}
	for i := range c.Repos {
		if c.Repos[i].Codex.Sandbox == "" {
			c.Repos[i].Codex.Sandbox = "workspace-write"
		}
		if c.Repos[i].Codex.AskForApproval == "" {
			c.Repos[i].Codex.AskForApproval = "never"
		}
		if c.Repos[i].Concurrency.MaxRunning == 0 {
			c.Repos[i].Concurrency.MaxRunning = 1
		}
	}
}

func (c *Config) validate() error {
	if c.GitHub.WebhookSecretFile == "" {
		return fmt.Errorf("github.webhook_secret_file is required")
	}
	if c.Queue.DSN == "" {
		return fmt.Errorf("queue.dsn is required")
	}
	if len(c.Repos) == 0 {
		return fmt.Errorf("at least one repo is required")
	}
	seen := map[string]bool{}
	for _, repo := range c.Repos {
		if repo.FullName == "" || repo.CloneURL == "" || repo.ForkPushRemote == "" {
			return fmt.Errorf("repo full_name, clone_url, and fork_push_remote are required")
		}
		if seen[repo.FullName] {
			return fmt.Errorf("duplicate repo %q", repo.FullName)
		}
		seen[repo.FullName] = true
		if len(repo.BaseBranches) == 0 {
			return fmt.Errorf("repo %s requires base_branches", repo.FullName)
		}
		if len(repo.DenyPaths) == 0 {
			return fmt.Errorf("repo %s requires deny_paths", repo.FullName)
		}
		if len(repo.TestCommands) == 0 {
			return fmt.Errorf("repo %s requires test_commands", repo.FullName)
		}
		if repo.Codex.Sandbox == "danger-full-access" {
			return fmt.Errorf("repo %s cannot use danger-full-access", repo.FullName)
		}
		if repo.Codex.AskForApproval != "never" {
			return fmt.Errorf("repo %s must use ask_for_approval never", repo.FullName)
		}
		if slices.Contains(repo.ProtectedBranches, "") {
			return fmt.Errorf("repo %s has an empty protected branch pattern", repo.FullName)
		}
	}
	return nil
}
```

Create `configs/example.yml` with the sample repo from the spec and `worker.no_activity_timeout_minutes: 45`.

- [ ] **Step 3: Run config tests**

Run: `go test ./internal/config -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/config configs/example.yml
git commit -m "feat: add multi-repo config model"
```

## Task 2: Command Parser And Flags

**Files:**
- Create: `internal/commands/parser.go`
- Create: `internal/commands/parser_test.go`

- [ ] **Step 1: Write failing parser tests**

```go
package commands

import "testing"

func TestParseFindsStandaloneCommandOnly(t *testing.T) {
	cmds, err := ParseBody("please run `/codex implement`\n/codex plan --base main\ntext", Options{AllowedBases: []string{"main"}, MaxNoActivityMinutes: 240})
	if err != nil {
		t.Fatalf("ParseBody error: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Name != Plan {
		t.Fatalf("commands = %#v", cmds)
	}
}

func TestParseRejectsMaxMinutesAndAcceptsNoActivity(t *testing.T) {
	_, err := ParseBody("/codex implement --max-minutes 5", Options{AllowedBases: []string{"main"}, MaxNoActivityMinutes: 240})
	if err == nil {
		t.Fatalf("expected --max-minutes to be rejected")
	}
	cmds, err := ParseBody("/codex implement --no-activity-minutes 60 --base main --branch codex/issue-1", Options{AllowedBases: []string{"main"}, MaxNoActivityMinutes: 240})
	if err != nil {
		t.Fatalf("ParseBody error: %v", err)
	}
	if cmds[0].Flags.NoActivityMinutes != 60 {
		t.Fatalf("no activity minutes = %d", cmds[0].Flags.NoActivityMinutes)
	}
}
```

Run: `go test ./internal/commands -run 'TestParse' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement parser**

```go
package commands

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type Name string

const (
	Plan      Name = "plan"
	Implement Name = "implement"
	Fix       Name = "fix"
	Review    Name = "review"
	Retry     Name = "retry"
	Cancel    Name = "cancel"
	Status    Name = "status"
)

type Options struct {
	AllowedBases          []string
	MaxNoActivityMinutes  int
}

type Command struct {
	Name  Name
	Flags Flags
	Raw   string
}

type Flags struct {
	Branch            string
	Base              string
	DryRun            bool
	NoActivityMinutes int
}

func ParseBody(body string, opts Options) ([]Command, error) {
	var out []Command
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "/codex ") {
			continue
		}
		cmd, err := parseLine(trimmed, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, cmd)
	}
	return out, nil
}

func parseLine(line string, opts Options) (Command, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 || parts[0] != "/codex" {
		return Command{}, fmt.Errorf("invalid command line")
	}
	name := Name(parts[1])
	if !slices.Contains([]Name{Plan, Implement, Fix, Review, Retry, Cancel, Status}, name) {
		return Command{}, fmt.Errorf("unknown command %q", name)
	}
	cmd := Command{Name: name, Raw: line}
	for i := 2; i < len(parts); i++ {
		switch parts[i] {
		case "--dry-run":
			cmd.Flags.DryRun = true
		case "--branch":
			i++
			if i >= len(parts) || !safeBranch(parts[i]) {
				return Command{}, fmt.Errorf("invalid --branch")
			}
			cmd.Flags.Branch = parts[i]
		case "--base":
			i++
			if i >= len(parts) || !slices.Contains(opts.AllowedBases, parts[i]) {
				return Command{}, fmt.Errorf("invalid --base")
			}
			cmd.Flags.Base = parts[i]
		case "--no-activity-minutes":
			i++
			if i >= len(parts) {
				return Command{}, fmt.Errorf("missing --no-activity-minutes value")
			}
			n, err := strconv.Atoi(parts[i])
			if err != nil || n < 30 || n > opts.MaxNoActivityMinutes {
				return Command{}, fmt.Errorf("invalid --no-activity-minutes")
			}
			cmd.Flags.NoActivityMinutes = n
		default:
			return Command{}, fmt.Errorf("unknown flag %q", parts[i])
		}
	}
	return cmd, nil
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
```

- [ ] **Step 3: Run parser tests**

Run: `go test ./internal/commands -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/commands
git commit -m "feat: parse codex issue commands"
```

## Task 3: Webhook Signature And Event Normalization

**Files:**
- Create: `internal/webhook/signature.go`
- Create: `internal/webhook/event.go`
- Create: `internal/webhook/webhook_test.go`
- Create: `tests/fixtures/github/issue_comment_created.json`

- [ ] **Step 1: Write failing webhook tests**

```go
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestVerifySignatureAcceptsGitHubHeader(t *testing.T) {
	body := []byte(`{"zen":"Keep it logically awesome."}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if err := VerifySignature(body, header, []byte("secret")); err != nil {
		t.Fatalf("VerifySignature error: %v", err)
	}
}

func TestVerifySignatureRejectsMissingOrInvalid(t *testing.T) {
	if err := VerifySignature([]byte("{}"), "", []byte("secret")); err == nil {
		t.Fatalf("expected missing signature error")
	}
	if err := VerifySignature([]byte("{}"), "sha256=bad", []byte("secret")); err == nil {
		t.Fatalf("expected invalid signature error")
	}
}

func TestNormalizeIssueCommentCreated(t *testing.T) {
	body, err := os.ReadFile("../../tests/fixtures/github/issue_comment_created.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := Normalize("issue_comment", "delivery-1", body)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if event.RepoFullName != "funland/foliospace-Library" || event.Actor != "hellcatjack" || event.IssueNumber != 2 {
		t.Fatalf("event = %#v", event)
	}
}
```

Run: `go test ./internal/webhook -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement signature verifier and normalized event**

```go
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type NormalizedEvent struct {
	DeliveryID   string
	EventType    string
	Action       string
	RepoFullName string
	IssueNumber  int
	CommentID    int64
	Actor        string
	IssueTitle   string
	IssueBody    string
	CommentBody  string
	Labels       []string
	Closed       bool
	Locked       bool
}

func VerifySignature(body []byte, header string, secret []byte) error {
	if header == "" {
		return fmt.Errorf("signature missing")
	}
	if !strings.HasPrefix(header, "sha256=") {
		return fmt.Errorf("signature scheme invalid")
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return fmt.Errorf("signature encoding invalid")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return fmt.Errorf("signature invalid")
	}
	return nil
}

func Normalize(eventType, deliveryID string, body []byte) (NormalizedEvent, error) {
	var raw struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
		Issue struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			State  string `json:"state"`
			Locked bool   `json:"locked"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"issue"`
		Comment struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return NormalizedEvent{}, err
	}
	labels := make([]string, 0, len(raw.Issue.Labels))
	for _, label := range raw.Issue.Labels {
		labels = append(labels, label.Name)
	}
	return NormalizedEvent{
		DeliveryID: deliveryID, EventType: eventType, Action: raw.Action,
		RepoFullName: raw.Repository.FullName, IssueNumber: raw.Issue.Number,
		CommentID: raw.Comment.ID, Actor: raw.Sender.Login,
		IssueTitle: raw.Issue.Title, IssueBody: raw.Issue.Body, CommentBody: raw.Comment.Body,
		Labels: labels, Closed: raw.Issue.State == "closed", Locked: raw.Issue.Locked,
	}, nil
}
```

Create the fixture with a minimal `issue_comment.created` payload for `funland/foliospace-Library`, actor `hellcatjack`, issue `2`, and comment body `/codex plan`.

- [ ] **Step 3: Run webhook tests**

Run: `go test ./internal/webhook -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/webhook tests/fixtures/github
git commit -m "feat: verify and normalize github webhooks"
```

## Task 4: SQLite Queue, State Machine, And Plan Artifacts

**Files:**
- Create: `internal/queue/models.go`
- Create: `internal/queue/store.go`
- Create: `internal/queue/store_test.go`

- [ ] **Step 1: Write failing queue tests**

```go
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
```

Run: `go test ./internal/queue -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement schema and store API**

Create `models.go` with states:

```go
package queue

import "time"

type State string

const (
	StateReceived     State = "received"
	StateValidating   State = "validating"
	StateRejected     State = "rejected"
	StateQueued       State = "queued"
	StateStarting     State = "starting"
	StatePlanning     State = "planning"
	StateImplementing State = "implementing"
	StateTesting      State = "testing"
	StateReviewing    State = "reviewing"
	StateCreatingPR   State = "creating_pr"
	StateWaitingHuman State = "waiting_human"
	StateDone         State = "done"
	StateFailed       State = "failed"
	StateCancelled    State = "cancelled"
	StateExpired      State = "expired"
)

type Delivery struct {
	ID string
	EventType string
	RepoFullName string
	IssueNumber int
	Actor string
	BodySHA256 string
	Duplicate bool
}

type Job struct {
	ID string
	DeliveryID string
	RepoFullName string
	IssueNumber int
	CommentID int64
	Actor string
	Command string
	FlagsJSON string
	State State
	BaseBranch string
	WorkBranch string
	PRNumber int
	CreatedAt time.Time
	WorkerHeartbeatAt time.Time
	JobActivityAt time.Time
	LastError string
}

type CreateJobInput struct {
	DeliveryID string
	RepoFullName string
	IssueNumber int
	CommentID int64
	Actor string
	Command string
	FlagsJSON string
	BaseBranch string
	WorkBranch string
}

type PlanArtifact struct {
	ID int64
	RepoFullName string
	IssueNumber int
	IssueHash string
	BaseBranch string
	Assumptions []string
	AcceptanceCriteria []string
	OpenQuestions []string
	ReadyForImplementation bool
	CreatedAt time.Time
}
```

`store.go` must create these tables with `CREATE TABLE IF NOT EXISTS`: `webhook_deliveries`, `jobs`, `job_events`, `job_artifacts`, `plan_artifacts`. Use `modernc.org/sqlite` as the blank driver import.

- [ ] **Step 3: Run queue tests**

Run: `go test ./internal/queue -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/queue go.mod go.sum
git commit -m "feat: add sqlite job queue"
```

## Task 5: Authorization And Plan Readiness Gate

**Files:**
- Create: `internal/authz/authz.go`
- Create: `internal/authz/authz_test.go`

- [ ] **Step 1: Write failing authorization tests**

```go
package authz

import (
	"context"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/commands"
	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

func TestMaintainerCanImplementWithoutReadyLabel(t *testing.T) {
	decision := Authorize(context.Background(), Input{
		Repo: repoPolicy(),
		Actor: "hellcatjack",
		Command: commands.Command{Name: commands.Implement},
		IssueLabels: nil,
		IssueHash: "h1",
		HasActiveJob: false,
		ReadyPlan: ReadyPlan{Ready: false},
	})
	if !decision.Allowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRequesterImplementRequiresReadyPlanOrLabel(t *testing.T) {
	decision := Authorize(context.Background(), Input{
		Repo: repoPolicyWithRequester(),
		Actor: "bob",
		Command: commands.Command{Name: commands.Implement},
		IssueLabels: nil,
		IssueHash: "h1",
		HasActiveJob: false,
		ReadyPlan: ReadyPlan{Ready: false},
	})
	if decision.Allowed || decision.Reason != "label_required" {
		t.Fatalf("decision = %#v", decision)
	}
	decision = Authorize(context.Background(), Input{
		Repo: repoPolicyWithRequester(),
		Actor: "bob",
		Command: commands.Command{Name: commands.Implement},
		IssueLabels: []string{"codex:ready"},
		IssueHash: "h1",
		HasActiveJob: false,
		ReadyPlan: ReadyPlan{Ready: false},
	})
	if !decision.Allowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestStatusDoesNotRequireReadyPlan(t *testing.T) {
	decision := Authorize(context.Background(), Input{
		Repo: repoPolicyWithRequester(),
		Actor: "bob",
		Command: commands.Command{Name: commands.Status},
		HasActiveJob: true,
	})
	if !decision.Allowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func repoPolicy() config.RepoConfig {
	return config.RepoConfig{
		FullName: "funland/foliospace-Library",
		BaseBranches: []string{"main"},
		RequiredLabelsForImplement: []string{"codex:ready"},
		AllowedActors: config.ActorRoles{Maintainers: []string{"hellcatjack"}},
	}
}

func repoPolicyWithRequester() config.RepoConfig {
	repo := repoPolicy()
	repo.AllowedActors.Requesters = []string{"bob"}
	return repo
}
```

Run: `go test ./internal/authz -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement authorization decisions**

```go
package authz

import (
	"context"
	"slices"

	"github.com/hellcatjack/codex-issue-gateway/internal/commands"
	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

type ReadyPlan struct {
	Ready bool
	IssueHash string
}

type Input struct {
	Repo config.RepoConfig
	Actor string
	Command commands.Command
	IssueLabels []string
	IssueClosed bool
	IssueLocked bool
	IssueHash string
	HasActiveJob bool
	ReadyPlan ReadyPlan
}

type Decision struct {
	Allowed bool
	Reason string
}

func Authorize(ctx context.Context, in Input) Decision {
	_ = ctx
	role := roleFor(in.Repo.AllowedActors, in.Actor)
	if role == "viewer" {
		return deny("actor_not_allowed")
	}
	if in.IssueLocked || (in.IssueClosed && in.Command.Name != commands.Status) {
		return deny("issue_unavailable")
	}
	if in.HasActiveJob && in.Command.Name != commands.Status && in.Command.Name != commands.Cancel {
		return deny("job_already_active")
	}
	switch in.Command.Name {
	case commands.Status:
		return allow()
	case commands.Plan:
		if role == "requester" || role == "operator" || role == "maintainer" || role == "admin" {
			return allow()
		}
	case commands.Implement, commands.Fix, commands.Retry:
		if role == "maintainer" || role == "admin" {
			return allow()
		}
		if role == "requester" && (in.ReadyPlan.Ready || hasRequiredLabels(in.IssueLabels, in.Repo.RequiredLabelsForImplement)) {
			return allow()
		}
		return deny("label_required")
	case commands.Review, commands.Cancel:
		if role == "operator" || role == "maintainer" || role == "admin" {
			return allow()
		}
	}
	return deny("actor_not_allowed")
}

func roleFor(roles config.ActorRoles, actor string) string {
	switch {
	case slices.Contains(roles.Admins, actor):
		return "admin"
	case slices.Contains(roles.Maintainers, actor):
		return "maintainer"
	case slices.Contains(roles.Operators, actor):
		return "operator"
	case slices.Contains(roles.Requesters, actor):
		return "requester"
	default:
		return "viewer"
	}
}

func hasRequiredLabels(labels, required []string) bool {
	for _, want := range required {
		if !slices.Contains(labels, want) {
			return false
		}
	}
	return len(required) > 0
}

func allow() Decision { return Decision{Allowed: true} }
func deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }
```

- [ ] **Step 3: Run authorization tests**

Run: `go test ./internal/authz -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/authz
git commit -m "feat: enforce codex command authorization"
```

## Task 6: Audit Logger And HTTP Gateway Handlers

**Files:**
- Create: `internal/audit/audit.go`
- Create: `internal/audit/audit_test.go`
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`
- Create: `cmd/codex-issue-gateway/main.go`

- [ ] **Step 1: Write failing server and audit tests**

```go
package audit

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerRedactsSecretValues(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf)
	logger.Info(Event{RequestID: "r1", DeliveryID: "d1", Decision: "rejected", Reason: "signature_invalid", Message: "secret=supersecretvalue"})
	got := buf.String()
	if strings.Contains(got, "supersecretvalue") {
		t.Fatalf("log leaked secret: %s", got)
	}
	if !strings.Contains(got, "signature_invalid") {
		t.Fatalf("log missing reason: %s", got)
	}
}
```

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzReturnsOK(t *testing.T) {
	srv := New(Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}`+"\n" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}
```

Run: `go test ./internal/audit ./internal/server -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement audit logger and HTTP router**

`internal/audit/audit.go`:

```go
package audit

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

type Logger struct{ w io.Writer }

type Event struct {
	Time string `json:"time"`
	RequestID string `json:"request_id,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
	JobID string `json:"job_id,omitempty"`
	Repo string `json:"repo,omitempty"`
	Issue int `json:"issue,omitempty"`
	Actor string `json:"actor,omitempty"`
	Command string `json:"command,omitempty"`
	Decision string `json:"decision,omitempty"`
	Reason string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

func New(w io.Writer) *Logger { return &Logger{w: w} }

func (l *Logger) Info(e Event) {
	e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	e.Message = redact(e.Message)
	_ = json.NewEncoder(l.w).Encode(e)
}

func redact(s string) string {
	s = strings.ReplaceAll(s, "supersecretvalue", "[redacted]")
	return s
}
```

`internal/server/server.go` must expose `GET /healthz`, `GET /readyz`, and `POST /github/webhook`. At this task, `/github/webhook` verifies method and returns `501` until Task 7 wires intake.

- [ ] **Step 3: Run server tests**

Run: `go test ./internal/audit ./internal/server -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/audit internal/server cmd/codex-issue-gateway
git commit -m "feat: add http gateway skeleton"
```

## Task 7: Webhook Intake To Queue

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/queue/store.go`
- Modify: `internal/queue/models.go`
- Create: `internal/github/client.go`
- Create: `internal/github/fake.go`

- [ ] **Step 1: Write failing webhook intake test**

```go
func TestWebhookAcceptsSignedPlanCommand(t *testing.T) {
	body := signedFixtureBody(t)
	store := newServerTestStore(t)
	cfg := testConfig(t)
	gh := github.NewFake()
	srv := New(Dependencies{Config: cfg, Queue: store, GitHub: gh, WebhookSecret: []byte("secret")})

	req := httptest.NewRequest(http.MethodPost, "/github/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signatureFor(body, "secret"))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	jobs, err := store.JobsByIssue(context.Background(), "funland/foliospace-Library", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Command != "plan" {
		t.Fatalf("jobs = %#v", jobs)
	}
}

func signedFixtureBody(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("../../tests/fixtures/github/issue_comment_created.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func signatureFor(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(strings.NewReader(configYAMLForServerTest()))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newServerTestStore(t *testing.T) *queue.Store {
	t.Helper()
	store, err := queue.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func configYAMLForServerTest() string {
	return `
server:
  listen: "127.0.0.1:18090"
github:
  app_id: 1
  installation_id: 2
  private_key_file: "/tmp/app.pem"
  webhook_secret_file: "/tmp/webhook-secret"
queue:
  dsn: ":memory:"
repos:
  - full_name: "funland/foliospace-Library"
    clone_url: "git@github.com:funland/foliospace-Library.git"
    fork_push_remote: "git@github.com:hellcatjack/foliospace-Library.git"
    base_branches: ["main"]
    required_labels_for_implement: ["codex:ready"]
    allowed_actors:
      maintainers: ["hellcatjack"]
    deny_paths: [".env", ".env.*", "docker-compose.yml"]
    test_commands: ["go test ./..."]
`
}
```

Run: `go test ./internal/server -run TestWebhookAcceptsSignedPlanCommand -v`

Expected: FAIL because `/github/webhook` still returns `501`.

- [ ] **Step 2: Implement intake orchestration**

Server handler must:

1. Use `github.Client` from `internal/github/client.go` for comments and PR operations.
1. Reject bodies larger than `Config.Server.MaxBodyBytes` with `413`.
2. Verify signature.
3. Normalize event.
4. Ignore unsupported event/action with `202`.
5. Load repo policy by `RepoFullName`.
6. Parse commands from comment body or issue body.
7. Record delivery id.
8. Return duplicate response when delivery already exists.
9. Authorize each command.
10. Create queued job for allowed executable command.
11. Comment accepted or rejected through the GitHub interface when identity is trusted.

Use this response struct:

```go
type WebhookResponse struct {
	Accepted bool `json:"accepted"`
	Duplicate bool `json:"duplicate,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
	JobID string `json:"job_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}
```

Use the local `newServerTestStore(t)` helper in `internal/server/server_test.go` for the server test.

Create the minimal `internal/github/client.go` and `internal/github/fake.go` in this task:

```go
package github

import "context"

type Client interface {
	CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error
	CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error)
	AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error
}

type PullRequestInput struct {
	RepoFullName string
	Title string
	Head string
	Base string
	Body string
}

type PullRequest struct {
	Number int
	URL string
}
```

- [ ] **Step 3: Run webhook intake tests**

Run: `go test ./internal/server ./internal/webhook ./internal/commands ./internal/authz ./internal/queue -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/server internal/queue
git commit -m "feat: enqueue authorized webhook commands"
```

## Task 8: GitHub App Real Client

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/fake.go`
- Create: `internal/github/client_test.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Write failing GitHub client tests**

```go
package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFakeClientRecordsIssueCommentAndPR(t *testing.T) {
	fake := NewFake()
	if err := fake.CreateIssueComment(context.Background(), "owner/repo", 1, "hello"); err != nil {
		t.Fatal(err)
	}
	pr, err := fake.CreatePullRequest(context.Background(), PullRequestInput{RepoFullName: "owner/repo", Title: "Codex #1", Head: "codex/issue-1", Base: "main", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Comments) != 1 || pr.Number != 1 {
		t.Fatalf("fake state = %#v pr=%#v", fake, pr)
	}
}

func TestAppClientCreatesInstallationTokenRequest(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatalf("missing app bearer authorization")
		}
		_, _ = w.Write([]byte(`{"token":"installation-token"}`))
	}))
	defer server.Close()

	client := NewAppClient(AppClientOptions{BaseURL: server.URL, AppID: 1, InstallationID: 2, PrivateKeyPEM: testPrivateKeyPEM(t)})
	token, err := client.InstallationToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "installation-token" || gotPath != "/app/installations/2/access_tokens" {
		t.Fatalf("token=%q path=%q", token, gotPath)
	}
}

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	return []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIBOgIBAAJBALz7aZxj8xYx4La4dP5lwdGEhQYk6nVM6tW0YqUpSkp4S0P7
o6itxIBw1F6kt3MdLJ3c1S1yK7Ff+vME0xkCAwEAAQJABRkJr1cCc9qk4z7V
Vt4LLqk5b8NBo7w77xKqkLMwhw+hMDiXWvBqB8dJ2bFv4AEAYI2+xx4wQeC5
whS2AQIhAPJctqBTtybdqkzvqIYMdhyzcQ2FeF9cPajcSYx/61VhAiEAx9yv
Bj8ebQqFS4oX8BSS+viURdbO6WLDw0N6hp0wp6ECIQCzG50X/0cI9BoRwjwJ
x6XOSisC1Gkik5kEpg71jGZJAQIhAIUE5BErgfeSmkHmr0ujYdqHusSAvhIx
wmr4Y+vMh+RhAiEAz7XVl4jfm19g8sG0svAxVNqaOW4EoNQ1JD1w0WIO1QM=
-----END RSA PRIVATE KEY-----`)
}
```

Run: `go test ./internal/github -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Extend fake client and add real client skeleton**

```go
package github

import "context"

type Client interface {
	CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error
	CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error)
	AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error
}

type PullRequestInput struct {
	RepoFullName string
	Title string
	Head string
	Base string
	Body string
}

type PullRequest struct {
	Number int
	URL string
}
```

The interface already exists from Task 7. Real client implementation must build a GitHub App JWT with `github.com/golang-jwt/jwt/v5`, exchange it for an installation token, and use `net/http` for REST calls. The fake stores comments, labels, and PRs in slices for tests.

- [ ] **Step 3: Run GitHub tests**

Run: `go test ./internal/github -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/github go.mod go.sum
git commit -m "feat: add github app client abstraction"
```

## Task 9: Sandbox And Git Workspace Preparation

**Files:**
- Create: `internal/sandbox/sandbox.go`
- Create: `internal/sandbox/sandbox_test.go`

- [ ] **Step 1: Write failing sandbox tests**

```go
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
```

Run: `go test ./internal/sandbox -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement workspace and git preparation helpers**

```go
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

type Workspace struct {
	Root string
	RepoDir string
	CodexHome string
	ArtifactsDir string
	TempDir string
}

func CreateWorkspace(jobRoot, jobID string) (Workspace, error) {
	root := filepath.Join(jobRoot, jobID)
	ws := Workspace{
		Root: root,
		RepoDir: filepath.Join(root, "repo"),
		CodexHome: filepath.Join(root, "codex-home"),
		ArtifactsDir: filepath.Join(root, "artifacts"),
		TempDir: filepath.Join(root, "tmp"),
	}
	for _, dir := range []string{ws.RepoDir, ws.CodexHome, ws.ArtifactsDir, ws.TempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Workspace{}, err
		}
	}
	return ws, nil
}

func WorkBranch(repo config.RepoConfig, issueNumber int, jobID, requested string) (string, error) {
	if requested != "" {
		if !safeBranch(requested) {
			return "", fmt.Errorf("unsafe branch")
		}
		return requested, nil
	}
	return fmt.Sprintf("codex/issue-%d-%s", issueNumber, jobID), nil
}
```

Add `PrepareRepository(ctx, repo, ws, baseBranch)` that shells out to `git clone` from `LocalFixturePath` when present, otherwise `CloneURL`, then checks out a work branch. Use `exec.CommandContext` with argument arrays only.

- [ ] **Step 3: Run sandbox tests**

Run: `go test ./internal/sandbox -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/sandbox
git commit -m "feat: prepare isolated job workspaces"
```

## Task 10: Non-Interactive Runner And Progress Watchdog

**Files:**
- Create: `internal/runner/process.go`
- Create: `internal/runner/codex.go`
- Create: `internal/runner/watchdog.go`
- Create: `internal/runner/runner_test.go`

- [ ] **Step 1: Write failing runner tests**

```go
package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

func TestCodexCommandIsNonInteractive(t *testing.T) {
	cmd := BuildCodexCommand(CodexInput{
		CodexBinary: "codex",
		RepoDir: "/tmp/job/repo",
		CodexHome: "/tmp/job/codex-home",
		Repo: config.RepoConfig{Codex: config.CodexConfig{Sandbox: "workspace-write", AskForApproval: "never", Ephemeral: true, JSONEvents: true}},
		Prompt: "do work",
	})
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"exec", "--sandbox workspace-write", "--ask-for-approval never", "--ephemeral", "--json", "-"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if cmd.Env["CODEX_HOME"] != "/tmp/job/codex-home" {
		t.Fatalf("env = %#v", cmd.Env)
	}
}

func TestWatchdogExpiresOnlyAfterNoActivity(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	wd := Watchdog{NoActivityTimeout: 45 * time.Minute}
	if wd.Expired(now, now.Add(-44*time.Minute)) {
		t.Fatalf("watchdog expired too early")
	}
	if !wd.Expired(now, now.Add(-46*time.Minute)) {
		t.Fatalf("watchdog did not expire stale activity")
	}
}

func TestProcessRunnerReportsActivityFromOutput(t *testing.T) {
	ctx := context.Background()
	var activity int
	result, err := RunProcess(ctx, ProcessInput{
		Name: "sh",
		Args: []string{"-c", "printf hello"},
		OnActivity: func(Activity) { activity++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || activity == 0 {
		t.Fatalf("result=%#v activity=%d", result, activity)
	}
}
```

Run: `go test ./internal/runner -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement runner types**

```go
package runner

import "time"

type CommandSpec struct {
	Name string
	Args []string
	Dir string
	Env map[string]string
	Stdin string
}

type Activity struct {
	Time time.Time
	Kind string
	Bytes int
}

type ProcessInput struct {
	Name string
	Args []string
	Dir string
	Env map[string]string
	Stdin string
	OnActivity func(Activity)
}

type ProcessResult struct {
	ExitCode int
	Stdout string
	Stderr string
}

type CodexInput struct {
	CodexBinary string
	RepoDir string
	CodexHome string
	Repo config.RepoConfig
	Prompt string
}

type Watchdog struct {
	NoActivityTimeout time.Duration
}

func (w Watchdog) Expired(now, lastActivity time.Time) bool {
	return now.Sub(lastActivity) > w.NoActivityTimeout
}
```

`RunProcess` must write stdin once, close stdin, capture stdout/stderr with bounded buffers, and call `OnActivity` when either stream receives bytes. `BuildCodexCommand` must include the safety prompt text requiring no questions and `needs_plan_revision` on insufficient requirements.

- [ ] **Step 3: Run runner tests**

Run: `go test ./internal/runner -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/runner
git commit -m "feat: run codex non-interactively"
```

## Task 11: Diff Policy And Test Command Execution

**Files:**
- Create: `internal/diffpolicy/policy.go`
- Create: `internal/diffpolicy/policy_test.go`
- Modify: `internal/runner/process.go`

- [ ] **Step 1: Write failing diff policy tests**

```go
package diffpolicy

import "testing"

func TestDenylistBlocksDockerComposeAndEnv(t *testing.T) {
	result := Evaluate(Input{
		ChangedFiles: []string{"README.md", "docker-compose.yml", ".env"},
		DenyPaths: []string{".env", ".env.*", "**/*secret*", "**/*token*", "docker-compose.yml"},
	})
	if result.Allowed || len(result.DeniedFiles) != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestReviewRequiredFilesMarkSecurityReview(t *testing.T) {
	result := Evaluate(Input{
		ChangedFiles: []string{".github/workflows/test.yml", "internal/authz/authz.go"},
		DenyPaths: []string{".env"},
		ReviewRequiredPaths: []string{".github/workflows/**", "internal/authz/**"},
	})
	if !result.Allowed || !result.RequiresSecurityReview {
		t.Fatalf("result = %#v", result)
	}
}
```

Run: `go test ./internal/diffpolicy -run 'Test' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement diff policy evaluator**

```go
package diffpolicy

import "path/filepath"

type Input struct {
	ChangedFiles []string
	DenyPaths []string
	ReviewRequiredPaths []string
	MaxFiles int
	MaxDeletedFiles int
}

type Result struct {
	Allowed bool
	DeniedFiles []string
	ReviewFiles []string
	RequiresSecurityReview bool
	Reason string
}

func Evaluate(in Input) Result {
	result := Result{Allowed: true}
	for _, file := range in.ChangedFiles {
		if matchesAny(file, in.DenyPaths) {
			result.DeniedFiles = append(result.DeniedFiles, file)
		}
		if matchesAny(file, in.ReviewRequiredPaths) {
			result.ReviewFiles = append(result.ReviewFiles, file)
		}
	}
	if len(result.DeniedFiles) > 0 {
		result.Allowed = false
		result.Reason = "diff_policy_failed"
	}
	if len(result.ReviewFiles) > 0 {
		result.RequiresSecurityReview = true
	}
	return result
}

func matchesAny(file string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, _ := filepath.Match(pattern, file); ok {
			return true
		}
		if len(pattern) >= 3 && pattern[len(pattern)-3:] == "/**" {
			prefix := pattern[:len(pattern)-3]
			if len(file) >= len(prefix) && file[:len(prefix)] == prefix {
				return true
			}
		}
	}
	return false
}
```

Add runner helper `RunTestCommands(ctx, repoDir string, commands []string, onActivity func(runner.Activity))` that executes configured commands with `sh -c` only for config-defined test commands, never Issue text.

- [ ] **Step 3: Run diff policy and runner tests**

Run: `go test ./internal/diffpolicy ./internal/runner -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/diffpolicy internal/runner
git commit -m "feat: enforce diff and test policies"
```

## Task 12: Worker Orchestration Through PR Creation

**Files:**
- Create: `internal/worker/worker.go`
- Create: `internal/worker/worker_test.go`
- Modify: `internal/queue/store.go`
- Modify: `internal/github/fake.go`
- Modify: `internal/sandbox/sandbox.go`
- Modify: `internal/runner/codex.go`

- [ ] **Step 1: Write failing worker tests**

```go
package worker

import (
	"context"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
)

func TestWorkerPlanStoresReadyArtifactAndComments(t *testing.T) {
	deps := newWorkerTestDeps(t)
	job := deps.createQueuedJob(t, "plan")
	deps.Runner.CodexResult = CodexResult{
		Status: "completed",
		Summary: "Plan ready",
		ReadyForImplementation: true,
		AcceptanceCriteria: []string{"go test ./... passes"},
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
	deps.Diff.ChangedFiles = []string{"README.md"}
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
}
```

Run: `go test ./internal/worker -run 'TestWorker' -v`

Expected: FAIL with package or symbol not found errors.

- [ ] **Step 2: Implement worker interfaces and phase orchestration**

`internal/worker/worker.go` must define:

```go
type CodexRunner interface {
	RunCodex(ctx context.Context, input CodexInput) (CodexResult, error)
	RunTests(ctx context.Context, repoDir string, commands []string, onActivity func()) (TestResult, error)
}

type DiffScanner interface {
	ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error)
}

type Worker struct {
	Queue Queue
	GitHub github.Client
	Runner CodexRunner
	Diff DiffScanner
	JobRoot string
}

type CodexInput struct {
	Job queue.Job
	Prompt string
}

type CodexResult struct {
	Status string
	Summary string
	ReadyForImplementation bool
	AcceptanceCriteria []string
	BlockingReasons []string
}

type TestResult struct {
	Passed bool
	Output string
}
```

`RunOne` must:

1. Lease one queued job.
2. Transition to `starting`.
3. Create workspace and prepare repo.
4. For `plan`, transition to `planning`, run non-interactive Codex plan prompt, save plan artifact, comment once, transition to `done` or `waiting_human`.
5. For `implement` and `fix`, transition to `implementing`, run Codex, handle `needs_plan_revision` as `waiting_human`, run tests, run diff policy, commit branch, push branch, create PR, comment once, transition to `done`.
6. For test failure, transition to `failed`, save artifact, comment once, and do not create a PR.
7. For denylist failure, transition to `failed`, save artifact, comment once, and do not create a PR.
8. Refresh `job_activity_at` through queue activity updates when runner output arrives.

`internal/worker/worker_test.go` must include these deterministic helpers:

```go
type workerTestDeps struct {
	Queue *queue.Store
	GitHub *github.FakeClient
	Runner *fakeRunner
	Diff *fakeDiff
	Worker *Worker
}

type fakeRunner struct {
	CodexResult CodexResult
	TestResult TestResult
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
	ChangedFiles []string
}

func (d *fakeDiff) ChangedFiles(ctx context.Context, repoDir, baseBranch string) ([]string, error) {
	return d.ChangedFiles, nil
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
	w := &Worker{Queue: store, GitHub: gh, Runner: runner, Diff: diff, JobRoot: t.TempDir()}
	return workerTestDeps{Queue: store, GitHub: gh, Runner: runner, Diff: diff, Worker: w}
}

func (d workerTestDeps) createQueuedJob(t *testing.T, command string) queue.Job {
	t.Helper()
	job, err := d.Queue.CreateJob(context.Background(), queue.CreateJobInput{
		DeliveryID: "delivery-" + command,
		RepoFullName: "funland/foliospace-Library",
		IssueNumber: 2,
		Actor: "hellcatjack",
		Command: command,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}
```

- [ ] **Step 3: Run worker tests**

Run: `go test ./internal/worker -v`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/worker internal/queue internal/github internal/sandbox internal/runner
git commit -m "feat: orchestrate codex jobs through pr creation"
```

## Task 13: Local Integration Test With FolioSpace Fixture

**Files:**
- Create: `tests/integration/foliospace_test.go`
- Modify: `docs/local-development.md`
- Modify: `configs/example.yml`

- [ ] **Step 1: Write guarded integration test**

```go
package integration

import (
	"os"
	"os/exec"
	"testing"
)

func TestFolioSpaceFixtureCommands(t *testing.T) {
	if os.Getenv("CODEX_GATEWAY_RUN_INTEGRATION") != "1" {
		t.Skip("set CODEX_GATEWAY_RUN_INTEGRATION=1 to run local fixture integration")
	}
	if _, err := os.Stat("/app/devs/foliospace-Library/go.mod"); err != nil {
		t.Fatalf("missing local fixture: %v", err)
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = "/app/devs/foliospace-Library"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test fixture failed: %v\n%s", err, out)
	}
}
```

Run: `go test ./tests/integration -v`

Expected: PASS with the test skipped unless `CODEX_GATEWAY_RUN_INTEGRATION=1`.

- [ ] **Step 2: Add local development documentation**

Create `docs/local-development.md` with these commands:

````markdown
# Local Development

Run unit tests:

```bash
go test ./...
```

Run the FolioSpace fixture integration test:

```bash
CODEX_GATEWAY_RUN_INTEGRATION=1 go test ./tests/integration -v
```

Start the gateway with the sample config:

```bash
go run ./cmd/codex-issue-gateway --config configs/example.yml
```
````

- [ ] **Step 3: Run full default test suite**

Run: `go test ./...`

Expected: PASS with integration test skipped by default.

- [ ] **Step 4: Commit**

```bash
git add tests/integration docs/local-development.md configs/example.yml
git commit -m "test: add foliospace fixture integration guard"
```

## Task 14: Final Verification And Repository Hygiene

**Files:**
- Modify: `.gitignore`
- Modify: `README.md`

- [ ] **Step 1: Add project ignore rules and README**

`.gitignore`:

```gitignore
*.db
*.db-shm
*.db-wal
bin/
coverage.out
tmp/
```

`README.md`:

```markdown
# codex-issue-gateway

Self-hosted GitHub Issue automation gateway for running non-interactive Codex development jobs across configured repositories.

## Safety Defaults

- GitHub webhook HMAC verification is required.
- Commands are accepted only from standalone `/codex ...` lines.
- Execution commands are non-interactive and never wait for user input.
- Worker expiry is based on no activity, not normal elapsed job duration.
- Jobs run in isolated directories with per-job `CODEX_HOME`.
- Repository policies define allowed actors, branches, tests, deny paths, and review-required paths.
```

- [ ] **Step 2: Run full verification**

Run:

```bash
go test ./...
git diff --check
```

Expected: all Go tests PASS; `git diff --check` exits 0.

- [ ] **Step 3: Inspect git status**

Run: `git status --short`

Expected: only intended files are modified or untracked.

- [ ] **Step 4: Commit**

```bash
git add .gitignore README.md
git commit -m "docs: add project usage and safety defaults"
```

## Self-Review Checklist

- Spec coverage: Tasks 1-14 cover config, command parsing, webhook validation, queue, authz, GitHub App abstraction, HTTP intake, sandbox, non-interactive runner, progress watchdog, diff policy, worker orchestration, FolioSpace integration, and documentation.
- Non-interactive execution: Task 10 and Task 12 enforce stdin close, `--ask-for-approval never`, `needs_plan_revision`, and one-shot Issue feedback.
- Progress watchdog: Task 10 and Task 12 track activity and expire only stale no-activity jobs.
- Multi-repo support: Task 1 and Task 5 make repo policy config-driven; Task 7 uses `repository.full_name` lookup.
- Security requirements: Tasks 3, 5, 7, 10, and 11 cover HMAC, default deny, non-shell Issue input, sandbox execution, denylist, and secret-safe logs.
- Phase 1-8 coverage: Tasks 1-12 produce the executable MVP through PR creation; Tasks 13-14 verify local fixture and document usage.
