package issueimage

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hellcatjack/codex-issue-gateway/internal/issuecontext"
	"github.com/hellcatjack/codex-issue-gateway/internal/publicreport"
)

const (
	DefaultMaxImages = 5
	DefaultMaxBytes  = 10 * 1024 * 1024
)

var (
	markdownImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+["'][^)]*["'])?\)`)
	htmlImagePattern     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	htmlSrcPattern       = regexp.MustCompile(`(?is)\bsrc\s*=\s*["']([^"']+)["']`)
	htmlAltPattern       = regexp.MustCompile(`(?is)\balt\s*=\s*["']([^"']*)["']`)
	githubUserAssetS3    = regexp.MustCompile(`^github-production-user-asset-[a-z0-9]+\.s3\.amazonaws\.com$`)
)

type URLRule struct {
	Host         string
	PathPrefixes []string
	AllowHTTP    bool
}

type URLRules []URLRule

type Collector struct {
	Client       *http.Client
	Allowed      URLRules
	MaxImages    int
	MaxBytes     int64
	OutputDir    string
	RepoFullName string
	BearerToken  string
}

type Context struct {
	Images  []Image
	Skipped []Skipped
}

type Image struct {
	Alt         string
	SourceHost  string
	ContentType string
	Bytes       int64
	Width       int
	Height      int
	Path        string
}

type Skipped struct {
	Reason string
	Host   string
}

type reference struct {
	Alt string
	URL string
}

func DefaultRules() URLRules {
	return URLRules{
		{Host: "github.com", PathPrefixes: []string{"/user-attachments/assets/"}},
		{Host: "user-images.githubusercontent.com"},
		{Host: "private-user-images.githubusercontent.com"},
		{Host: "camo.githubusercontent.com"},
	}
}

func DefaultCollector(outputDir string) Collector {
	return Collector{
		Allowed:   DefaultRules(),
		MaxImages: DefaultMaxImages,
		MaxBytes:  DefaultMaxBytes,
		OutputDir: outputDir,
	}
}

func (rules URLRules) Allowed(u *url.URL) bool {
	if u == nil {
		return false
	}
	for _, rule := range rules {
		if !rule.AllowHTTP && u.Scheme != "https" {
			continue
		}
		if rule.AllowHTTP && u.Scheme != "https" && u.Scheme != "http" {
			continue
		}
		if !sameHost(rule.Host, u) {
			continue
		}
		if len(rule.PathPrefixes) == 0 {
			return true
		}
		for _, prefix := range rule.PathPrefixes {
			if strings.HasPrefix(u.EscapedPath(), prefix) || strings.HasPrefix(u.Path, prefix) {
				return true
			}
		}
	}
	return false
}

func (rules URLRules) AllowedRedirect(from, to *url.URL) bool {
	if rules.Allowed(to) {
		return true
	}
	if !rules.Allowed(from) || to == nil || to.Scheme != "https" {
		return false
	}
	return strings.EqualFold(from.Hostname(), "github.com") &&
		(strings.HasPrefix(from.EscapedPath(), "/user-attachments/assets/") || strings.HasPrefix(from.Path, "/user-attachments/assets/")) &&
		githubUserAssetS3.MatchString(strings.ToLower(to.Hostname()))
}

func (c Collector) Collect(ctx context.Context, snapshot issuecontext.Snapshot) (Context, error) {
	out := Context{}
	refs := extractCollaboratorReferences(snapshot)
	if len(refs) == 0 {
		return out, nil
	}
	outputDir := strings.TrimSpace(c.OutputDir)
	if outputDir == "" {
		return out, fmt.Errorf("issue image output dir is required")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return out, err
	}
	allowed := c.Allowed
	if len(allowed) == 0 {
		allowed = DefaultRules()
	}
	maxImages := c.MaxImages
	if maxImages <= 0 {
		maxImages = DefaultMaxImages
	}
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	client := c.httpClient(allowed)
	seen := map[string]bool{}
	for _, ref := range refs {
		if len(out.Images) >= maxImages {
			out.Skipped = append(out.Skipped, Skipped{Reason: "max_images_reached"})
			break
		}
		if seen[ref.URL] {
			continue
		}
		seen[ref.URL] = true
		u, err := url.Parse(strings.TrimSpace(ref.URL))
		if err != nil || !u.IsAbs() {
			out.Skipped = append(out.Skipped, Skipped{Reason: "invalid_url"})
			continue
		}
		if !allowed.Allowed(u) {
			out.Skipped = append(out.Skipped, Skipped{Reason: "unsupported_host", Host: safeHost(u)})
			continue
		}
		image, reason, err := c.download(ctx, client, u, ref.Alt, outputDir, len(out.Images)+1, maxBytes)
		if err != nil {
			return out, err
		}
		if reason != "" {
			out.Skipped = append(out.Skipped, Skipped{Reason: reason, Host: safeHost(u)})
			continue
		}
		out.Images = append(out.Images, image)
	}
	return out, nil
}

func (c Collector) httpClient(allowed URLRules) *http.Client {
	base := c.Client
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	if client.Timeout == 0 {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		if len(via) == 0 {
			if !allowed.Allowed(req.URL) {
				return http.ErrUseLastResponse
			}
			return nil
		}
		if !allowed.AllowedRedirect(via[len(via)-1].URL, req.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &client
}

func (c Collector) download(ctx context.Context, client *http.Client, u *url.URL, alt, outputDir string, index int, maxBytes int64) (Image, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Image{}, "", err
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/*;q=0.8")
	if strings.TrimSpace(c.BearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.BearerToken))
	}
	resp, err := client.Do(req)
	if err != nil {
		return Image{}, "download_failed", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Image{}, "download_failed", nil
	}
	contentType := normalizedContentType(resp.Header.Get("Content-Type"))
	if !allowedContentType(contentType) {
		return Image{}, "non_image_response", nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return Image{}, "", err
	}
	if int64(len(data)) > maxBytes {
		return Image{}, "image_too_large", nil
	}
	ext := extensionFor(contentType, u)
	filename := fmt.Sprintf("issue-image-%d%s", index, ext)
	path := filepath.Join(outputDir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return Image{}, "", err
	}
	width, height := imageDimensions(data)
	return Image{
		Alt:         safeText(alt, 120),
		SourceHost:  safeHost(u),
		ContentType: contentType,
		Bytes:       int64(len(data)),
		Width:       width,
		Height:      height,
		Path:        path,
	}, "", nil
}

func (c Context) ImageFiles() []string {
	files := make([]string, 0, len(c.Images))
	for _, image := range c.Images {
		if image.Path != "" {
			files = append(files, image.Path)
		}
	}
	return files
}

func (c Context) PromptSection() string {
	if len(c.Images) == 0 && len(c.Skipped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Issue image inputs:\n")
	for i, image := range c.Images {
		fmt.Fprintf(&b, "- Image %d: ", i+1)
		if image.Alt != "" {
			fmt.Fprintf(&b, "alt %q; ", image.Alt)
		}
		fmt.Fprintf(&b, "source host %s; content-type %s; size %d bytes", image.SourceHost, image.ContentType, image.Bytes)
		if image.Width > 0 && image.Height > 0 {
			fmt.Fprintf(&b, "; dimensions %dx%d", image.Width, image.Height)
		}
		b.WriteString("; attached to Codex as image input.\n")
	}
	if len(c.Skipped) > 0 {
		fmt.Fprintf(&b, "- Skipped %d issue image link(s):", len(c.Skipped))
		counts := map[string]int{}
		for _, skipped := range c.Skipped {
			counts[skipped.Reason]++
		}
		first := true
		for reason, count := range counts {
			if !first {
				b.WriteString(",")
			}
			first = false
			fmt.Fprintf(&b, " %s=%d", reason, count)
		}
		b.WriteString(".\n")
	}
	b.WriteString("Treat attached images as read-only visual references. Do not include cached issue image paths or source image URLs in public feedback.")
	return strings.TrimSpace(b.String())
}

func extractCollaboratorReferences(snapshot issuecontext.Snapshot) []reference {
	var refs []reference
	if collaboratorAssociation(snapshot.AuthorAssociation) {
		refs = append(refs, extractReferences(snapshot.Body)...)
	}
	for _, comment := range snapshot.Comments {
		if collaboratorAssociation(comment.AuthorAssociation) {
			refs = append(refs, extractReferences(comment.Body)...)
		}
	}
	return refs
}

func extractReferences(body string) []reference {
	var refs []reference
	for _, match := range markdownImagePattern.FindAllStringSubmatch(body, -1) {
		refs = append(refs, reference{Alt: strings.TrimSpace(match[1]), URL: strings.TrimSpace(match[2])})
	}
	for _, tag := range htmlImagePattern.FindAllString(body, -1) {
		src := submatch(htmlSrcPattern, tag)
		if src == "" {
			continue
		}
		refs = append(refs, reference{Alt: strings.TrimSpace(submatch(htmlAltPattern, tag)), URL: strings.TrimSpace(src)})
	}
	return refs
}

func collaboratorAssociation(value string) bool {
	switch strings.ToUpper(value) {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	default:
		return false
	}
}

func sameHost(ruleHost string, u *url.URL) bool {
	ruleHost = strings.ToLower(strings.TrimSpace(ruleHost))
	if ruleHost == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	hostname := strings.ToLower(u.Hostname())
	return host == ruleHost || hostname == ruleHost
}

func safeHost(u *url.URL) string {
	if u == nil {
		return ""
	}
	return safeText(u.Hostname(), 120)
}

func safeText(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || publicreport.Sanitize(value) != value {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

func normalizedContentType(value string) string {
	contentType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return strings.ToLower(contentType)
}

func allowedContentType(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml"
}

func extensionFor(contentType string, u *url.URL) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		return ext
	default:
		return ".img"
	}
}

func imageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func submatch(pattern *regexp.Regexp, value string) string {
	match := pattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
