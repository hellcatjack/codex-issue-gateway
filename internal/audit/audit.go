package audit

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

type Logger struct {
	w io.Writer
}

type Event struct {
	Time       string `json:"time"`
	RequestID  string `json:"request_id,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Repo       string `json:"repo,omitempty"`
	Issue      int    `json:"issue,omitempty"`
	Actor      string `json:"actor,omitempty"`
	Command    string `json:"command,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Message    string `json:"message,omitempty"`
}

func New(w io.Writer) *Logger {
	return &Logger{w: w}
}

func (l *Logger) Info(e Event) {
	e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	e.Message = redact(e.Message)
	_ = json.NewEncoder(l.w).Encode(e)
}

func redact(s string) string {
	s = strings.ReplaceAll(s, "supersecretvalue", "[redacted]")
	return s
}
