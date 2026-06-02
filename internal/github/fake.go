package github

import (
	"context"
	"strconv"

	"github.com/hellcatjack/codex-issue-gateway/internal/publiccomment"
)

type FakeClient struct {
	Comments     []IssueComment
	PullRequests []PullRequestInput
	Labels       []LabelRequest
	Issues       map[string]IssueContext
}

type IssueComment struct {
	RepoFullName string
	IssueNumber  int
	Body         string
}

type LabelRequest struct {
	RepoFullName string
	IssueNumber  int
	Labels       []string
}

func NewFake() *FakeClient {
	return &FakeClient{Issues: map[string]IssueContext{}}
}

func (f *FakeClient) CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error {
	_ = ctx
	for _, chunk := range publiccomment.SafeChunks(body) {
		f.Comments = append(f.Comments, IssueComment{RepoFullName: repoFullName, IssueNumber: issueNumber, Body: chunk})
	}
	return nil
}

func (f *FakeClient) CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error) {
	_ = ctx
	input.Title = publiccomment.SafeTitle(input.Title)
	input.Body = publiccomment.Safe(input.Body)
	f.PullRequests = append(f.PullRequests, input)
	number := len(f.PullRequests)
	return PullRequest{Number: number, URL: input.RepoFullName + "/pull/" + strconv.Itoa(number)}, nil
}

func (f *FakeClient) AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error {
	_ = ctx
	f.Labels = append(f.Labels, LabelRequest{RepoFullName: repoFullName, IssueNumber: issueNumber, Labels: labels})
	return nil
}

func (f *FakeClient) FetchIssueContext(ctx context.Context, repoFullName string, issueNumber int) (IssueContext, error) {
	_ = ctx
	issue, ok := f.Issues[issueKey(repoFullName, issueNumber)]
	if !ok {
		return IssueContext{}, nil
	}
	return issue, nil
}

func issueKey(repoFullName string, issueNumber int) string {
	return repoFullName + "#" + strconv.Itoa(issueNumber)
}
