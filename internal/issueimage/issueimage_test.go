package issueimage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/issuecontext"
)

func TestCollectorDownloadsAllowedCollaboratorImagesAndBuildsSafePrompt(t *testing.T) {
	pngData := testPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer server.Close()
	host := mustURLHost(t, server.URL)
	outDir := t.TempDir()
	snapshot := issuecontext.Snapshot{
		RepoFullName:      "owner/repo",
		Number:            1,
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "Please match this UI:\n![Dashboard screenshot](" + server.URL + "/dashboard.png)\n![External](https://example.com/evil.png)",
		Comments: []issuecontext.Comment{
			{Author: "alice", AuthorAssociation: "NONE", Body: "![ignored](" + server.URL + "/ignored.png)"},
			{Author: "hellcatjack", AuthorAssociation: "OWNER", Body: `<img alt="Dialog state" src="` + server.URL + `/dialog.png">`},
		},
	}
	collector := Collector{
		Client:       server.Client(),
		Allowed:      []URLRule{{Host: host, AllowHTTP: true}},
		MaxImages:    5,
		MaxBytes:     1024 * 1024,
		OutputDir:    outDir,
		RepoFullName: snapshot.RepoFullName,
	}

	got, err := collector.Collect(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Images) != 2 {
		t.Fatalf("images=%#v skipped=%#v", got.Images, got.Skipped)
	}
	for _, img := range got.Images {
		if img.Path == "" {
			t.Fatalf("image missing path: %#v", img)
		}
		if !strings.HasPrefix(img.Path, outDir+string(os.PathSeparator)) {
			t.Fatalf("image path %q is outside output dir %q", img.Path, outDir)
		}
		if _, err := os.Stat(img.Path); err != nil {
			t.Fatalf("saved image missing: %v", err)
		}
		if img.Width != 2 || img.Height != 1 {
			t.Fatalf("dimensions=%dx%d", img.Width, img.Height)
		}
	}
	section := got.PromptSection()
	for _, want := range []string{
		"Issue image inputs:",
		`alt "Dashboard screenshot"`,
		`alt "Dialog state"`,
		"content-type image/png",
		"dimensions 2x1",
		"Skipped 1 issue image",
		"unsupported_host",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("prompt section missing %q:\n%s", want, section)
		}
	}
	for _, leaked := range []string{outDir, server.URL, "example.com/evil.png", "ignored"} {
		if strings.Contains(section, leaked) {
			t.Fatalf("prompt section leaked %q:\n%s", leaked, section)
		}
	}
	if got.ImageFiles()[0] != got.Images[0].Path || got.ImageFiles()[1] != got.Images[1].Path {
		t.Fatalf("image files = %#v images=%#v", got.ImageFiles(), got.Images)
	}
}

func TestCollectorRejectsNonImageResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not an image"))
	}))
	defer server.Close()
	snapshot := issuecontext.Snapshot{
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "![not image](" + server.URL + "/file.txt)",
	}
	collector := Collector{
		Client:    server.Client(),
		Allowed:   []URLRule{{Host: mustURLHost(t, server.URL), AllowHTTP: true}},
		MaxImages: 3,
		MaxBytes:  1024 * 1024,
		OutputDir: t.TempDir(),
	}

	got, err := collector.Collect(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Images) != 0 {
		t.Fatalf("images=%#v", got.Images)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Reason != "non_image_response" {
		t.Fatalf("skipped=%#v", got.Skipped)
	}
	if !strings.Contains(got.PromptSection(), "non_image_response") {
		t.Fatalf("prompt missing skip reason: %s", got.PromptSection())
	}
}

func TestCollectorSendsAuthorizationHeaderWhenTokenIsConfigured(t *testing.T) {
	pngData := testPNG(t)
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer server.Close()
	collector := Collector{
		Client:      server.Client(),
		Allowed:     []URLRule{{Host: mustURLHost(t, server.URL), AllowHTTP: true}},
		MaxImages:   1,
		MaxBytes:    1024 * 1024,
		OutputDir:   t.TempDir(),
		BearerToken: "installation-token",
	}

	if _, err := collector.Collect(context.Background(), issuecontext.Snapshot{
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "![image](" + server.URL + "/image.png)",
	}); err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer installation-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustURLHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestDefaultRulesAllowOnlyGitHubIssueImageHosts(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/user-attachments/assets/abc",
		"https://user-images.githubusercontent.com/1/file.png",
		"https://private-user-images.githubusercontent.com/1/file.png",
		"https://camo.githubusercontent.com/abc",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !DefaultRules().Allowed(u) {
			t.Fatalf("default rules rejected %s", raw)
		}
	}
	for _, raw := range []string{
		"http://github.com/user-attachments/assets/abc",
		"https://github.com/owner/repo/raw/main/screenshot.png",
		"https://example.com/screenshot.png",
		"https://objects.githubusercontent.com/github-production-release-asset/file.png",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if DefaultRules().Allowed(u) {
			t.Fatalf("default rules accepted %s", raw)
		}
	}
}

func TestDefaultRulesAllowSignedGitHubUserAssetRedirectsOnlyAfterAllowedAttachmentURL(t *testing.T) {
	from := mustParseURL(t, "https://github.com/user-attachments/assets/75d49877")
	to := mustParseURL(t, "https://github-production-user-asset-6210df.s3.amazonaws.com/6687484/601188752-75d49877.png?response-content-type=image%2Fpng")

	if !DefaultRules().AllowedRedirect(from, to) {
		t.Fatalf("default rules rejected GitHub user asset redirect")
	}
	if DefaultRules().Allowed(to) {
		t.Fatalf("default rules should not allow direct S3 user asset URLs")
	}
	for _, pair := range [][2]string{
		{"https://example.com/image.png", to.String()},
		{from.String(), "https://evil.s3.amazonaws.com/image.png"},
		{from.String(), "http://github-production-user-asset-6210df.s3.amazonaws.com/image.png"},
	} {
		if DefaultRules().AllowedRedirect(mustParseURL(t, pair[0]), mustParseURL(t, pair[1])) {
			t.Fatalf("default rules accepted redirect from %s to %s", pair[0], pair[1])
		}
	}
}

func TestCollectorWritesFilesWithImageExtensions(t *testing.T) {
	pngData := testPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer server.Close()
	outDir := t.TempDir()
	collector := Collector{
		Client:    server.Client(),
		Allowed:   []URLRule{{Host: mustURLHost(t, server.URL), AllowHTTP: true}},
		MaxImages: 1,
		MaxBytes:  1024 * 1024,
		OutputDir: outDir,
	}
	got, err := collector.Collect(context.Background(), issuecontext.Snapshot{
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "![image](" + server.URL + "/asset)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 {
		t.Fatalf("images=%#v", got.Images)
	}
	if filepath.Ext(got.Images[0].Path) != ".png" {
		t.Fatalf("path = %q", got.Images[0].Path)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
