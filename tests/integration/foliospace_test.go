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
