package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
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
