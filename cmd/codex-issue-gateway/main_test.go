package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStartsServerWithConfiguredListenAddress(t *testing.T) {
	tmp := t.TempDir()
	secretPath := filepath.Join(tmp, "webhook-secret")
	dbPath := filepath.Join(tmp, "gateway.db")
	configPath := filepath.Join(tmp, "gateway.yml")
	if err := os.WriteFile(secretPath, []byte("local-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(mainConfigYAML(secretPath, dbPath)), 0o600); err != nil {
		t.Fatal(err)
	}

	called := false
	code := run(context.Background(), []string{"--config", configPath}, io.Discard, func(addr string, handler http.Handler) error {
		called = true
		if addr != "127.0.0.1:19090" {
			t.Fatalf("listen addr = %q", addr)
		}
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz code = %d body=%s", rec.Code, rec.Body.String())
		}
		return http.ErrServerClosed
	})

	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !called {
		t.Fatal("server function was not called")
	}
}

func TestRunRejectsMissingConfigFlag(t *testing.T) {
	var stderr strings.Builder
	code := run(context.Background(), nil, &stderr, nil)
	if code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "missing --config") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func mainConfigYAML(secretPath, dbPath string) string {
	yml := `
server:
  listen: "127.0.0.1:19090"
github:
  app_id: 1
  installation_id: 2
  private_key_file: "/tmp/app.pem"
  webhook_secret_file: "__SECRET__"
queue:
  dsn: "__DB__"
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
