package github

import "context"

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
