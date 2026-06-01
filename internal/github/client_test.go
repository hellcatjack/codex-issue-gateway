package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFakeClientRecordsIssueCommentAndPR(t *testing.T) {
	fake := NewFake()
	if err := fake.CreateIssueComment(context.Background(), "owner/repo", 1, "hello"); err != nil {
		t.Fatal(err)
	}
	pr, err := fake.CreatePullRequest(context.Background(), PullRequestInput{RepoFullName: "owner/repo", Title: "Codex #1", Head: "codex/issue-1", Base: "main", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Comments) != 1 || pr.Number != 1 {
		t.Fatalf("fake state = %#v pr=%#v", fake, pr)
	}
}

func TestAppClientCreatesInstallationTokenRequest(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatalf("missing app bearer authorization")
		}
		_, _ = w.Write([]byte(`{"token":"installation-token"}`))
	}))
	defer server.Close()

	client := NewAppClient(AppClientOptions{BaseURL: server.URL, AppID: 1, InstallationID: 2, PrivateKeyPEM: testPrivateKeyPEM(t)})
	token, err := client.InstallationToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "installation-token" || gotPath != "/app/installations/2/access_tokens" {
		t.Fatalf("token=%q path=%q", token, gotPath)
	}
}

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return pem.EncodeToMemory(block)
}
