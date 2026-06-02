package config

import (
	"slices"
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
	if repo.Codex.AuthSourceDir != "/home/runner/.codex" {
		t.Fatalf("auth source dir = %q", repo.Codex.AuthSourceDir)
	}
	if cfg.Worker.NoActivityTimeoutMinutes != 45 {
		t.Fatalf("no activity timeout = %d", cfg.Worker.NoActivityTimeoutMinutes)
	}
	if cfg.Server.PublicBaseURL != "https://gateway.example.test" {
		t.Fatalf("public base url = %q", cfg.Server.PublicBaseURL)
	}
	if len(repo.AgentSetupCommands) != 1 || repo.AgentSetupCommands[0] != "test -d node_modules || cp -a /cache/node_modules ./node_modules" {
		t.Fatalf("agent setup commands = %#v", repo.AgentSetupCommands)
	}
}

func TestLoadParsesWorkerPlaywrightConfig(t *testing.T) {
	yml := strings.Replace(validConfigYAML(), "queue:\n  dsn: \"/tmp/gateway.db\"\n", `queue:
  dsn: "/tmp/gateway.db"
worker:
  job_root: "/tmp/gateway/jobs"
  playwright:
    enabled: true
    browsers_path: "/cache/ms-playwright"
    node_binary: "/opt/node/bin/node"
    browsers: ["chromium", "firefox"]
`, 1)
	cfg, err := Load(strings.NewReader(yml))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	pw := cfg.Worker.Playwright
	if !pw.Enabled {
		t.Fatalf("playwright should be enabled")
	}
	if pw.BrowsersPath != "/cache/ms-playwright" {
		t.Fatalf("browsers path = %q", pw.BrowsersPath)
	}
	if pw.NodeBinary != "/opt/node/bin/node" {
		t.Fatalf("node binary = %q", pw.NodeBinary)
	}
	if !slices.Equal(pw.Browsers, []string{"chromium", "firefox"}) {
		t.Fatalf("browsers = %#v", pw.Browsers)
	}
}

func TestLoadDefaultsWorkerPlaywrightConfig(t *testing.T) {
	yml := strings.Replace(validConfigYAML(), "queue:\n  dsn: \"/tmp/gateway.db\"\n", `queue:
  dsn: "/tmp/gateway.db"
worker:
  job_root: "/tmp/gateway/jobs"
  playwright:
    enabled: true
`, 1)
	cfg, err := Load(strings.NewReader(yml))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	pw := cfg.Worker.Playwright
	if pw.BrowsersPath != "/tmp/gateway/jobs/_playwright-browsers" {
		t.Fatalf("browsers path = %q", pw.BrowsersPath)
	}
	if pw.NodeBinary != "node" {
		t.Fatalf("node binary = %q", pw.NodeBinary)
	}
	if !slices.Equal(pw.Browsers, []string{"chromium"}) {
		t.Fatalf("browsers = %#v", pw.Browsers)
	}
}

func TestLoadRejectsEmptyPlaywrightBrowser(t *testing.T) {
	yml := strings.Replace(validConfigYAML(), "queue:\n  dsn: \"/tmp/gateway.db\"\n", `queue:
  dsn: "/tmp/gateway.db"
worker:
  playwright:
    enabled: true
    browsers: [""]
`, 1)
	_, err := Load(strings.NewReader(yml))
	if err == nil || !strings.Contains(err.Error(), "worker.playwright.browsers contains an empty browser name") {
		t.Fatalf("expected empty browser validation error, got %v", err)
	}
}

func validConfigYAML() string {
	return `
server:
  listen: "127.0.0.1:18090"
  public_base_url: "https://gateway.example.test"
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
    agent_setup_commands: ["test -d node_modules || cp -a /cache/node_modules ./node_modules"]
    test_commands: ["go test ./..."]
    codex:
      auth_source_dir: "/home/runner/.codex"
`
}
