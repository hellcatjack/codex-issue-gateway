package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/authz"
	"github.com/hellcatjack/codex-issue-gateway/internal/commands"
	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
	"github.com/hellcatjack/codex-issue-gateway/internal/webhook"
)

type Dependencies struct {
	Config        *config.Config
	Queue         *queue.Store
	GitHub        github.Client
	WebhookSecret []byte
}

type Server struct {
	deps Dependencies
	mux  *http.ServeMux
}

const commandActor = "hellcatjack"

func New(deps Dependencies) *Server {
	s := &Server{deps: deps, mux: http.NewServeMux()}
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/readyz", s.handleReadyz)
	s.mux.HandleFunc("/artifacts/", s.handleArtifact)
	s.mux.HandleFunc("/github/webhook", s.handleWebhook)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queue": "ok", "github": "ok"})
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.deps.Config == nil || strings.TrimSpace(s.deps.Config.Worker.JobRoot) == "" {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/artifacts/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || !safeArtifactSegment(parts[0]) || !safeArtifactFile(parts[1]) {
		http.NotFound(w, r)
		return
	}
	filePath := filepath.Join(s.deps.Config.Worker.JobRoot, parts[0], "artifacts", "public", parts[1])
	info, err := os.Lstat(filePath)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	header := make([]byte, 512)
	n, _ := file.Read(header)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := http.DetectContentType(header[:n])
	if !strings.HasPrefix(contentType, "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, parts[1], info.ModTime(), file)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.deps.Config == nil || s.deps.Queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, WebhookResponse{Accepted: false, Reason: "not_ready"})
		return
	}
	body, err := readLimitedBody(r, s.deps.Config.Server.MaxBodyBytes)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, WebhookResponse{Accepted: false, Reason: "body_too_large"})
		return
	}
	if err := webhook.VerifySignature(body, r.Header.Get("X-Hub-Signature-256"), s.deps.WebhookSecret); err != nil {
		writeJSON(w, http.StatusUnauthorized, WebhookResponse{Accepted: false, Reason: "signature_invalid"})
		return
	}
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	eventType := r.Header.Get("X-GitHub-Event")
	if deliveryID == "" || eventType == "" {
		writeJSON(w, http.StatusBadRequest, WebhookResponse{Accepted: false, Reason: "missing_headers"})
		return
	}
	event, err := webhook.Normalize(eventType, deliveryID, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, WebhookResponse{Accepted: false, Reason: "json_invalid"})
		return
	}
	if event.EventType != "issue_comment" && event.EventType != "issues" {
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: true, DeliveryID: deliveryID})
		return
	}
	repo, ok := s.deps.Config.Repo(event.RepoFullName)
	if !ok {
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: false, DeliveryID: deliveryID, Reason: "repo_not_allowed"})
		return
	}
	delivery, err := s.deps.Queue.RecordDelivery(r.Context(), queue.Delivery{
		ID:           deliveryID,
		EventType:    event.EventType,
		RepoFullName: event.RepoFullName,
		IssueNumber:  event.IssueNumber,
		Actor:        event.Actor,
		BodySHA256:   sha256Hex(body),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, WebhookResponse{Accepted: false, Reason: "queue_failed"})
		return
	}
	if delivery.Duplicate {
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: true, Duplicate: true, DeliveryID: deliveryID})
		return
	}
	if !commandActionAllowed(event) {
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: false, DeliveryID: deliveryID, Reason: "command_action_ignored"})
		return
	}
	if event.Actor != commandActor {
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: false, DeliveryID: deliveryID, Reason: "command_actor_not_allowed"})
		return
	}
	bodyForCommands := event.CommentBody
	if bodyForCommands == "" {
		bodyForCommands = event.IssueBody
	}
	cmds, err := commands.ParseBody(bodyForCommands, commands.Options{AllowedBases: repo.BaseBranches, MaxNoActivityMinutes: s.deps.Config.Worker.NoActivityTimeoutMinutes})
	if err != nil || len(cmds) == 0 {
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: false, DeliveryID: deliveryID, Reason: "command_invalid"})
		return
	}
	cmd := cmds[0]
	decision := authz.Authorize(r.Context(), authz.Input{
		Repo:        repo,
		Actor:       event.Actor,
		Command:     cmd,
		IssueLabels: event.Labels,
		IssueClosed: event.Closed,
		IssueLocked: event.Locked,
		IssueHash:   sha256Hex([]byte(event.IssueTitle + "\n" + event.IssueBody)),
	})
	if !decision.Allowed {
		commentTrusted(r.Context(), s.deps.GitHub, event.RepoFullName, event.IssueNumber, "Codex Gateway 拒绝执行此请求。\n\n- 原因: "+decision.Reason)
		writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: false, DeliveryID: deliveryID, Reason: decision.Reason})
		return
	}
	base := cmd.Flags.Base
	if base == "" && len(repo.BaseBranches) > 0 {
		base = repo.BaseBranches[0]
	}
	job, err := s.deps.Queue.CreateJob(r.Context(), queue.CreateJobInput{
		DeliveryID:   deliveryID,
		RepoFullName: event.RepoFullName,
		IssueNumber:  event.IssueNumber,
		CommentID:    event.CommentID,
		Actor:        event.Actor,
		Command:      string(cmd.Name),
		BaseBranch:   base,
		WorkBranch:   cmd.Flags.Branch,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, WebhookResponse{Accepted: false, DeliveryID: deliveryID, Reason: "queue_failed"})
		return
	}
	commentTrusted(r.Context(), s.deps.GitHub, event.RepoFullName, event.IssueNumber, "Codex Gateway 已接收请求。\n\n- 命令: `/codex "+string(cmd.Name)+"`\n- 状态: `queued`")
	writeJSON(w, http.StatusAccepted, WebhookResponse{Accepted: true, DeliveryID: deliveryID, JobID: job.ID})
}

func safeArtifactSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for _, r := range segment {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func safeArtifactFile(file string) bool {
	if file == "" || file == "." || file == ".." || filepath.Base(file) != file || strings.ContainsRune(file, 0) {
		return false
	}
	if !safeArtifactSegment(strings.TrimSuffix(file, filepath.Ext(file))) {
		return false
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type WebhookResponse struct {
	Accepted   bool   `json:"accepted"`
	Duplicate  bool   `json:"duplicate,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func readLimitedBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	defer r.Body.Close()
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBytes))
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func commentTrusted(ctx context.Context, gh github.Client, repoFullName string, issueNumber int, body string) {
	if gh == nil {
		return
	}
	_ = gh.CreateIssueComment(ctx, repoFullName, issueNumber, body)
}

func commandActionAllowed(event webhook.NormalizedEvent) bool {
	switch event.EventType {
	case "issue_comment":
		return event.Action == "created" || event.Action == "edited"
	case "issues":
		return event.Action == "opened" || event.Action == "edited"
	default:
		return false
	}
}
