package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
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

func TestWebhookIgnoresCodexCommandFromNonCommandActor(t *testing.T) {
	body := []byte(strings.ReplaceAll(string(signedFixtureBody(t)), `"login": "hellcatjack"`, `"login": "alice"`))
	store := newServerTestStore(t)
	cfg := testConfig(t)
	srv := New(Dependencies{Config: cfg, Queue: store, GitHub: github.NewFake(), WebhookSecret: []byte("secret")})

	req := httptest.NewRequest(http.MethodPost, "/github/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", "delivery-non-command-actor")
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
	if len(jobs) != 0 {
		t.Fatalf("jobs = %#v, want none", jobs)
	}
}

func TestWebhookDoesNotParseCommandsFromLabeledIssueEvents(t *testing.T) {
	body := []byte(strings.ReplaceAll(string(signedFixtureBody(t)), `"action": "created"`, `"action": "labeled"`))
	store := newServerTestStore(t)
	cfg := testConfig(t)
	srv := New(Dependencies{Config: cfg, Queue: store, GitHub: github.NewFake(), WebhookSecret: []byte("secret")})

	req := httptest.NewRequest(http.MethodPost, "/github/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "delivery-labeled")
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
	if len(jobs) != 0 {
		t.Fatalf("jobs = %#v, want none", jobs)
	}
}

func TestArtifactsServesPublishedScreenshot(t *testing.T) {
	cfg := testConfig(t)
	cfg.Worker.JobRoot = t.TempDir()
	publicDir := filepath.Join(cfg.Worker.JobRoot, "job_123", "artifacts", "public")
	if err := os.MkdirAll(publicDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const png = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	if err := os.WriteFile(filepath.Join(publicDir, "screen.png"), []byte(png), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(Dependencies{Config: cfg})

	req := httptest.NewRequest(http.MethodGet, "/artifacts/job_123/screen.png", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != png {
		t.Fatalf("body = %q", string(body))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestArtifactsRejectsUnsafePath(t *testing.T) {
	cfg := testConfig(t)
	cfg.Worker.JobRoot = t.TempDir()
	srv := New(Dependencies{Config: cfg})

	req := httptest.NewRequest(http.MethodGet, "/artifacts/job_123/nested/secret.png", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestArtifactsRejectsNonImageContent(t *testing.T) {
	cfg := testConfig(t)
	cfg.Worker.JobRoot = t.TempDir()
	publicDir := filepath.Join(cfg.Worker.JobRoot, "job_123", "artifacts", "public")
	if err := os.MkdirAll(publicDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "screen.png"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(Dependencies{Config: cfg})

	req := httptest.NewRequest(http.MethodGet, "/artifacts/job_123/screen.png", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
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
