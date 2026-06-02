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
	"github.com/hellcatjack/codex-issue-gateway/internal/publiccomment"
)

type Client interface {
	CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error
	CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error)
	AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error
	FetchIssueContext(ctx context.Context, repoFullName string, issueNumber int) (IssueContext, error)
}

type IssueContext struct {
	RepoFullName      string
	Number            int
	Title             string
	Body              string
	Author            string
	AuthorAssociation string
	State             string
	Locked            bool
	Labels            []string
	Comments          []IssueContextComment
}

type IssueContextComment struct {
	ID                int64
	Author            string
	AuthorAssociation string
	Body              string
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
	for _, chunk := range publiccomment.SafeChunks(body) {
		var out any
		if err := c.installationRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repoFullName, issueNumber), map[string]string{"body": chunk}, &out); err != nil {
			return err
		}
	}
	return nil
}

func (c *AppClient) CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error) {
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	err := c.installationRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls", input.RepoFullName), map[string]string{
		"title": publiccomment.SafeTitle(input.Title),
		"head":  input.Head,
		"base":  input.Base,
		"body":  publiccomment.Safe(input.Body),
	}, &out)
	return PullRequest{Number: out.Number, URL: out.HTMLURL}, err
}

func (c *AppClient) AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error {
	var out any
	return c.installationRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", repoFullName, issueNumber), map[string][]string{"labels": labels}, &out)
}

func (c *AppClient) FetchIssueContext(ctx context.Context, repoFullName string, issueNumber int) (IssueContext, error) {
	var issue struct {
		Number            int    `json:"number"`
		Title             string `json:"title"`
		Body              string `json:"body"`
		State             string `json:"state"`
		Locked            bool   `json:"locked"`
		AuthorAssociation string `json:"author_association"`
		User              struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := c.installationRequest(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repoFullName, issueNumber), nil, &issue); err != nil {
		return IssueContext{}, err
	}
	commentsRaw, err := c.fetchIssueComments(ctx, repoFullName, issueNumber)
	if err != nil {
		return IssueContext{}, err
	}
	labels := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		labels = append(labels, label.Name)
	}
	comments := make([]IssueContextComment, 0, len(commentsRaw))
	for _, comment := range commentsRaw {
		comments = append(comments, IssueContextComment{
			ID:                comment.ID,
			Author:            comment.User.Login,
			AuthorAssociation: comment.AuthorAssociation,
			Body:              comment.Body,
		})
	}
	return IssueContext{
		RepoFullName:      repoFullName,
		Number:            issue.Number,
		Title:             issue.Title,
		Body:              issue.Body,
		Author:            issue.User.Login,
		AuthorAssociation: issue.AuthorAssociation,
		State:             issue.State,
		Locked:            issue.Locked,
		Labels:            labels,
		Comments:          comments,
	}, nil
}

func (c *AppClient) fetchIssueComments(ctx context.Context, repoFullName string, issueNumber int) ([]issueCommentResponse, error) {
	var all []issueCommentResponse
	for page := 1; ; page++ {
		var pageComments []issueCommentResponse
		path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100&page=%d", repoFullName, issueNumber, page)
		if err := c.installationRequest(ctx, http.MethodGet, path, nil, &pageComments); err != nil {
			return nil, err
		}
		all = append(all, pageComments...)
		if len(pageComments) < 100 {
			return all, nil
		}
	}
}

type issueCommentResponse struct {
	ID                int64  `json:"id"`
	Body              string `json:"body"`
	AuthorAssociation string `json:"author_association"`
	User              struct {
		Login string `json:"login"`
	} `json:"user"`
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
