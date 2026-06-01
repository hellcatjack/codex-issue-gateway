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
