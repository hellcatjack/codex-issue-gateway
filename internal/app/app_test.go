package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/worker"
)

func TestLoadRuntimeBuildsServerDependenciesFromConfig(t *testing.T) {
	tmp := t.TempDir()
	secretPath := filepath.Join(tmp, "webhook-secret")
	dbPath := filepath.Join(tmp, "gateway.db")
	configPath := filepath.Join(tmp, "gateway.yml")
	if err := os.WriteFile(secretPath, []byte("local-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(runtimeConfigYAML(secretPath, dbPath)), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, cleanup, err := LoadRuntime(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	defer cleanup()

	if runtime.Config.Server.Listen != "127.0.0.1:18090" {
		t.Fatalf("listen = %q", runtime.Config.Server.Listen)
	}
	if string(runtime.ServerDependencies.WebhookSecret) != "local-secret" {
		t.Fatalf("webhook secret = %q", string(runtime.ServerDependencies.WebhookSecret))
	}
	if runtime.ServerDependencies.Queue == nil {
		t.Fatal("queue dependency is nil")
	}
	if _, ok := runtime.ServerDependencies.GitHub.(*github.FakeClient); !ok {
		t.Fatalf("github dependency = %T, want *github.FakeClient", runtime.ServerDependencies.GitHub)
	}
}

func TestBuildWorkerUsesConfiguredCodexBinary(t *testing.T) {
	tmp := t.TempDir()
	secretPath := filepath.Join(tmp, "webhook-secret")
	dbPath := filepath.Join(tmp, "gateway.db")
	configPath := filepath.Join(tmp, "gateway.yml")
	if err := os.WriteFile(secretPath, []byte("local-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yml := strings.ReplaceAll(runtimeConfigYAML(secretPath, dbPath), "worker:\n", "worker:\n  codex_binary: \"/opt/codex/bin/codex\"\n")
	if err := os.WriteFile(configPath, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, cleanup, err := LoadRuntime(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	defer cleanup()

	w := buildWorker(runtime)
	runner, ok := w.Runner.(worker.LocalRunner)
	if !ok {
		t.Fatalf("runner = %T", w.Runner)
	}
	if runner.CodexBinary != "/opt/codex/bin/codex" {
		t.Fatalf("codex binary = %q", runner.CodexBinary)
	}
}

func runtimeConfigYAML(secretPath, dbPath string) string {
	yml := `
server:
  listen: "127.0.0.1:18090"
github:
  app_id: 1
  installation_id: 2
  private_key_file: "/tmp/app.pem"
  webhook_secret_file: "__SECRET__"
queue:
  dsn: "__DB__"
worker:
  enabled: true
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
	yml = strings.ReplaceAll(yml, "__SECRET__", secretPath)
	return strings.ReplaceAll(yml, "__DB__", dbPath)
}
