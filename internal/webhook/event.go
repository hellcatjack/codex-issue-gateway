package webhook

import "encoding/json"

type NormalizedEvent struct {
	DeliveryID   string
	EventType    string
	Action       string
	RepoFullName string
	IssueNumber  int
	CommentID    int64
	Actor        string
	IssueTitle   string
	IssueBody    string
	CommentBody  string
	Labels       []string
	Closed       bool
	Locked       bool
}

func Normalize(eventType, deliveryID string, body []byte) (NormalizedEvent, error) {
	var raw struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
		Issue struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			State  string `json:"state"`
			Locked bool   `json:"locked"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"issue"`
		Comment struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return NormalizedEvent{}, err
	}
	labels := make([]string, 0, len(raw.Issue.Labels))
	for _, label := range raw.Issue.Labels {
		labels = append(labels, label.Name)
	}
	return NormalizedEvent{
		DeliveryID:   deliveryID,
		EventType:    eventType,
		Action:       raw.Action,
		RepoFullName: raw.Repository.FullName,
		IssueNumber:  raw.Issue.Number,
		CommentID:    raw.Comment.ID,
		Actor:        raw.Sender.Login,
		IssueTitle:   raw.Issue.Title,
		IssueBody:    raw.Issue.Body,
		CommentBody:  raw.Comment.Body,
		Labels:       labels,
		Closed:       raw.Issue.State == "closed",
		Locked:       raw.Issue.Locked,
	}, nil
}
