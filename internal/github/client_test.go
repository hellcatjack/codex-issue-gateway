package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFakeClientRecordsIssueCommentAndPR(t *testing.T) {
	fake := NewFake()
	if err := fake.CreateIssueComment(context.Background(), "owner/repo", 1, "hello OPENAI_API_KEY=sk-proj-secret /home/hellcat/.env"); err != nil {
		t.Fatal(err)
	}
	pr, err := fake.CreatePullRequest(context.Background(), PullRequestInput{
		RepoFullName: "owner/repo",
		Title:        "Codex /data/share/repo",
		Head:         "codex/issue-1",
		Base:         "main",
		Body:         "body token=ghp_secretvalue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Comments) != 1 || pr.Number != 1 {
		t.Fatalf("fake state = %#v pr=%#v", fake, pr)
	}
	if strings.Contains(fake.Comments[0].Body, "sk-proj-secret") || strings.Contains(fake.Comments[0].Body, "/home/hellcat") {
		t.Fatalf("fake public comment leaked sensitive content: %q", fake.Comments[0].Body)
	}
	if strings.Contains(fake.PullRequests[0].Title, "/data/share") || strings.Contains(fake.PullRequests[0].Body, "ghp_secretvalue") {
		t.Fatalf("fake pull request leaked sensitive content: %#v", fake.PullRequests[0])
	}
}

func TestFakeClientFetchesIssueContext(t *testing.T) {
	fake := NewFake()
	fake.Issues["owner/repo#7"] = IssueContext{
		RepoFullName:      "owner/repo",
		Number:            7,
		Title:             "title",
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "body",
		Comments: []IssueContextComment{
			{ID: 99, Author: "hellcatjack", AuthorAssociation: "OWNER", Body: "/codex plan"},
		},
	}

	got, err := fake.FetchIssueContext(context.Background(), "owner/repo", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Number != 7 || len(got.Comments) != 1 || got.Comments[0].ID != 99 {
		t.Fatalf("issue context = %#v", got)
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

func TestAppClientFetchesAllIssueCommentPages(t *testing.T) {
	var requestedPages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/2/access_tokens" {
			_, _ = w.Write([]byte(`{"token":"installation-token"}`))
			return
		}
		if r.URL.Path == "/repos/owner/repo/issues/7" {
			_, _ = w.Write([]byte(`{"number":7,"title":"title","body":"body","state":"open","author_association":"OWNER","user":{"login":"hellcatjack"},"labels":[]}`))
			return
		}
		if r.URL.Path != "/repos/owner/repo/issues/7/comments" {
			t.Fatalf("unexpected path %s", r.URL.String())
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		page := r.URL.Query().Get("page")
		count := 100
		start := 1
		if page == "2" {
			count = 1
			start = 101
		}
		var comments []map[string]any
		for i := 0; i < count; i++ {
			id := start + i
			comments = append(comments, map[string]any{
				"id":                 id,
				"body":               fmt.Sprintf("comment %03d", id),
				"author_association": "OWNER",
				"user":               map[string]string{"login": "hellcatjack"},
			})
		}
		if err := json.NewEncoder(w).Encode(comments); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()
	client := NewAppClient(AppClientOptions{BaseURL: server.URL, AppID: 1, InstallationID: 2, PrivateKeyPEM: testPrivateKeyPEM(t)})

	got, err := client.FetchIssueContext(context.Background(), "owner/repo", 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Comments) != 101 {
		t.Fatalf("comments=%d requestedPages=%v", len(got.Comments), requestedPages)
	}
	if got.Comments[100].Body != "comment 101" {
		t.Fatalf("last comment = %#v", got.Comments[100])
	}
	if strings.Join(requestedPages, ",") != "1,2" {
		t.Fatalf("requested pages = %v", requestedPages)
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
