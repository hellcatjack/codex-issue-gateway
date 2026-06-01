package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestVerifySignatureAcceptsGitHubHeader(t *testing.T) {
	body := []byte(`{"zen":"Keep it logically awesome."}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if err := VerifySignature(body, header, []byte("secret")); err != nil {
		t.Fatalf("VerifySignature error: %v", err)
	}
}

func TestVerifySignatureRejectsMissingOrInvalid(t *testing.T) {
	if err := VerifySignature([]byte("{}"), "", []byte("secret")); err == nil {
		t.Fatalf("expected missing signature error")
	}
	if err := VerifySignature([]byte("{}"), "sha256=bad", []byte("secret")); err == nil {
		t.Fatalf("expected invalid signature error")
	}
}

func TestNormalizeIssueCommentCreated(t *testing.T) {
	body, err := os.ReadFile("../../tests/fixtures/github/issue_comment_created.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := Normalize("issue_comment", "delivery-1", body)
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if event.RepoFullName != "funland/foliospace-Library" || event.Actor != "hellcatjack" || event.IssueNumber != 2 {
		t.Fatalf("event = %#v", event)
	}
}
