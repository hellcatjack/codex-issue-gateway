package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Client interface {
	CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error
	CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error)
	AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error
}

type PullRequestInput struct {
	RepoFullName string
	Title        string
	Head         string
	Base         string
	Body         string
}

type PullRequest struct {
	Number int
	URL    string
}

type AppClientOptions struct {
	BaseURL        string
	AppID          int64
	InstallationID int64
	PrivateKeyPEM  []byte
	HTTPClient     *http.Client
}

type AppClient struct {
	baseURL        string
	appID          int64
	installationID int64
	privateKeyPEM  []byte
	httpClient     *http.Client
}

func NewAppClient(opts AppClientOptions) *AppClient {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &AppClient{
		baseURL:        strings.TrimRight(baseURL, "/"),
		appID:          opts.AppID,
		installationID: opts.InstallationID,
		privateKeyPEM:  opts.PrivateKeyPEM,
		httpClient:     client,
	}
}

func (c *AppClient) InstallationToken(ctx context.Context) (string, error) {
	appJWT, err := c.appJWT()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, c.installationID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	var out struct {
		Token string `json:"token"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("github installation token response missing token")
	}
	return out.Token, nil
}

func (c *AppClient) CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error {
	var out any
	return c.installationRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repoFullName, issueNumber), map[string]string{"body": body}, &out)
}

func (c *AppClient) CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error) {
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	err := c.installationRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls", input.RepoFullName), map[string]string{
		"title": input.Title,
		"head":  input.Head,
		"base":  input.Base,
		"body":  input.Body,
	}, &out)
	return PullRequest{Number: out.Number, URL: out.HTMLURL}, err
}

func (c *AppClient) AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error {
	var out any
	return c.installationRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", repoFullName, issueNumber), map[string][]string{"labels": labels}, &out)
}

func (c *AppClient) installationRequest(ctx context.Context, method, path string, payload any, out any) error {
	token, err := c.InstallationToken(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, out)
}

func (c *AppClient) doJSON(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github api failed: %s: %s", resp.Status, string(data))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *AppClient) appJWT() (string, error) {
	block, _ := pem.Decode(c.privateKeyPEM)
	if block == nil {
		return "", fmt.Errorf("invalid github app private key pem")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return "", err
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("github app private key is not RSA")
		}
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": c.appID,
	})
	return token.SignedString(key)
}
