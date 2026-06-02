package publiccomment

import (
	"strings"
	"testing"
)

func TestSafeReplacesSensitiveContentWithFallback(t *testing.T) {
	input := "failed with OPENAI_API_KEY=sk-proj-secret and /home/hellcat/project/.env"

	got := Safe(input)

	if strings.Contains(got, "sk-proj-secret") || strings.Contains(got, "/home/hellcat") {
		t.Fatalf("public comment leaked sensitive input: %q", got)
	}
	if got != Fallback {
		t.Fatalf("got %q, want fallback", got)
	}
}

func TestSafeAllowsSimpleStatusWithoutInternalIdentifiers(t *testing.T) {
	input := "Codex Gateway 已接收请求。\n\n- 命令: `/codex plan`\n- 状态: `queued`"

	got := Safe(input)

	if got != input {
		t.Fatalf("got %q, want unchanged status", got)
	}
}

func TestSafeDropsControlCharactersWithoutTruncatingLongBodies(t *testing.T) {
	tail := "tail marker after a long public comment"
	input := "status ok\x00\x1b[31m" + strings.Repeat("x", 20_000) + tail

	got := Safe(input)

	if strings.ContainsAny(got, "\x00\x1b") {
		t.Fatalf("public comment contains control characters: %q", got)
	}
	if strings.Contains(got, "[truncated]") {
		t.Fatalf("public comment was truncated")
	}
	if !strings.Contains(got, tail) {
		t.Fatalf("public comment dropped long tail: %d bytes", len([]byte(got)))
	}
}

func TestSafeChunksSplitsLongBodiesWithoutDroppingContent(t *testing.T) {
	tail := "tail marker after split public comment"
	input := "start\n" + strings.Repeat("safe public detail line\n", 6_000) + tail

	chunks := SafeChunks(input)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	var combined strings.Builder
	for _, chunk := range chunks {
		if len(chunk) > MaxIssueCommentChunkBytes {
			t.Fatalf("chunk length = %d, want <= %d", len(chunk), MaxIssueCommentChunkBytes)
		}
		if strings.Contains(chunk, "[truncated]") {
			t.Fatalf("chunk was truncated: %q", chunk)
		}
		combined.WriteString(chunk)
	}
	if !strings.Contains(combined.String(), "start") || !strings.Contains(combined.String(), tail) {
		t.Fatalf("chunks dropped content: %d bytes", combined.Len())
	}
}

func TestSafeTitleUsesFallbackForSensitiveInput(t *testing.T) {
	got := SafeTitle("Fix /data/share/repo using token=ghp_secretvalue")

	if got != FallbackTitle {
		t.Fatalf("got %q, want fallback title", got)
	}
}
