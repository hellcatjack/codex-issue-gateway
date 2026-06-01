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
