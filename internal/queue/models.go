package queue

import "time"

type State string

const (
	StateReceived     State = "received"
	StateValidating   State = "validating"
	StateRejected     State = "rejected"
	StateQueued       State = "queued"
	StateStarting     State = "starting"
	StatePlanning     State = "planning"
	StateImplementing State = "implementing"
	StateTesting      State = "testing"
	StateReviewing    State = "reviewing"
	StateCreatingPR   State = "creating_pr"
	StateWaitingHuman State = "waiting_human"
	StateDone         State = "done"
	StateFailed       State = "failed"
	StateCancelled    State = "cancelled"
	StateExpired      State = "expired"
)

type Delivery struct {
	ID           string
	EventType    string
	RepoFullName string
	IssueNumber  int
	Actor        string
	BodySHA256   string
	Duplicate    bool
}

type Job struct {
	ID                string
	DeliveryID        string
	RepoFullName      string
	IssueNumber       int
	CommentID         int64
	Actor             string
	Command           string
	FlagsJSON         string
	State             State
	BaseBranch        string
	WorkBranch        string
	PRNumber          int
	CreatedAt         time.Time
	WorkerHeartbeatAt time.Time
	JobActivityAt     time.Time
	LastError         string
}

type CreateJobInput struct {
	DeliveryID   string
	RepoFullName string
	IssueNumber  int
	CommentID    int64
	Actor        string
	Command      string
	FlagsJSON    string
	BaseBranch   string
	WorkBranch   string
}

type PlanArtifact struct {
	ID                     int64
	RepoFullName           string
	IssueNumber            int
	IssueHash              string
	BaseBranch             string
	Assumptions            []string
	AcceptanceCriteria     []string
	OpenQuestions          []string
	ReadyForImplementation bool
	CreatedAt              time.Time
}
