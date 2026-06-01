package github

import (
	"context"
	"strconv"
)

type FakeClient struct {
	Comments     []IssueComment
	PullRequests []PullRequestInput
	Labels       []LabelRequest
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
	return &FakeClient{}
}

func (f *FakeClient) CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) error {
	_ = ctx
	f.Comments = append(f.Comments, IssueComment{RepoFullName: repoFullName, IssueNumber: issueNumber, Body: body})
	return nil
}

func (f *FakeClient) CreatePullRequest(ctx context.Context, input PullRequestInput) (PullRequest, error) {
	_ = ctx
	f.PullRequests = append(f.PullRequests, input)
	number := len(f.PullRequests)
	return PullRequest{Number: number, URL: input.RepoFullName + "/pull/" + strconv.Itoa(number)}, nil
}

func (f *FakeClient) AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels []string) error {
	_ = ctx
	f.Labels = append(f.Labels, LabelRequest{RepoFullName: repoFullName, IssueNumber: issueNumber, Labels: labels})
	return nil
}
