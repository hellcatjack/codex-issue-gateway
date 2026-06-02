package publicreport

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestFromCodexOutputKeepsSafeAgentFeedbackAndDropsUnsafeLines(t *testing.T) {
	message := "status: completed\n\nSummary:\n- Added manifest metadata coverage.\n- Wrote files under /app/devs/epubReader.\n\nVerification:\n- `npm run test`: passed\n- OPENAI_API_KEY=sk-proj-secret"
	event, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": message},
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		string(event),
	}, "\n")

	got := FromCodexOutput(stdout)

	for _, want := range []string{"status: completed", "Added manifest metadata coverage", "`npm run test`: passed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("report missing %q: %s", want, got)
		}
	}
	for _, leaked := range []string{"/app/devs", "OPENAI_API_KEY", "sk-proj-secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("report leaked %q: %s", leaked, got)
		}
	}
}

func TestFromCodexOutputFallsBackWhenNothingSafeRemains(t *testing.T) {
	event, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": "OPENAI_API_KEY=sk-proj-secret\n/home/hellcat/.env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout := string(event)

	got := FromCodexOutput(stdout)

	if got != Fallback {
		t.Fatalf("report = %q", got)
	}
}

func TestSanitizeDropsAbsolutePathsInsideInlineFormatting(t *testing.T) {
	input := strings.Join([]string{
		"Verification:",
		"- `git diff --check`: passed",
		"- `/home/hellcat/.nvm/bin/npm run test`: failed",
		"- path '/app/codex-issue-gateway/tmp/job' was inspected",
		"- copied from \"/data/share/project/node_modules\"",
		"- plain /tmp/job/output.log",
	}, "\n")

	got := Sanitize(input)

	if !strings.Contains(got, "`git diff --check`: passed") {
		t.Fatalf("safe verification line missing: %s", got)
	}
	for _, leaked := range []string{"/home/hellcat", "/app/codex-issue-gateway", "/data/share", "/tmp/job"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitized report leaked %q: %s", leaked, got)
		}
	}
}

func TestSanitizeKeepsLongArtifactPreviewWithoutTruncating(t *testing.T) {
	var body strings.Builder
	body.WriteString("Summary:\n- Safe summary.\n\nArtifact preview:\n")
	for i := 0; i < 320; i++ {
		body.WriteString(fmt.Sprintf("- Safe preview line %03d for a changed Markdown artifact.\n", i))
	}

	got := Sanitize(body.String())

	if strings.Contains(got, "[truncated]") {
		t.Fatalf("artifact preview was truncated: %d bytes", len([]byte(got)))
	}
	if !strings.Contains(got, "Safe preview line 000 for a changed Markdown artifact") {
		t.Fatalf("preview content missing: %s", got)
	}
	if !strings.Contains(got, "Safe preview line 180 for a changed Markdown artifact") {
		t.Fatalf("later preview lines were dropped: %s", got)
	}
	if !strings.Contains(got, "Safe preview line 260 for a changed Markdown artifact") {
		t.Fatalf("longer preview lines were dropped: %s", got)
	}
	if !strings.Contains(got, "Safe preview line 319 for a changed Markdown artifact") {
		t.Fatalf("full preview was dropped near the end: %s", got)
	}
}
