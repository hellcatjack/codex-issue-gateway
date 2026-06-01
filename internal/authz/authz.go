package authz

import (
	"context"
	"slices"

	"github.com/hellcatjack/codex-issue-gateway/internal/commands"
	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

type ReadyPlan struct {
	Ready     bool
	IssueHash string
}

type Input struct {
	Repo         config.RepoConfig
	Actor        string
	Command      commands.Command
	IssueLabels  []string
	IssueClosed  bool
	IssueLocked  bool
	IssueHash    string
	HasActiveJob bool
	ReadyPlan    ReadyPlan
}

type Decision struct {
	Allowed bool
	Reason  string
}

func Authorize(ctx context.Context, in Input) Decision {
	_ = ctx
	role := roleFor(in.Repo.AllowedActors, in.Actor)
	if role == "viewer" {
		return deny("actor_not_allowed")
	}
	if in.IssueLocked || (in.IssueClosed && in.Command.Name != commands.Status) {
		return deny("issue_unavailable")
	}
	if in.HasActiveJob && in.Command.Name != commands.Status && in.Command.Name != commands.Cancel {
		return deny("job_already_active")
	}
	switch in.Command.Name {
	case commands.Status:
		return allow()
	case commands.Plan:
		if role == "requester" || role == "operator" || role == "maintainer" || role == "admin" {
			return allow()
		}
	case commands.Implement, commands.Fix, commands.Retry:
		if role == "maintainer" || role == "admin" {
			return allow()
		}
		if role == "requester" && (in.ReadyPlan.Ready || hasRequiredLabels(in.IssueLabels, in.Repo.RequiredLabelsForImplement)) {
			return allow()
		}
		return deny("label_required")
	case commands.Review, commands.Cancel:
		if role == "operator" || role == "maintainer" || role == "admin" {
			return allow()
		}
	}
	return deny("actor_not_allowed")
}

func roleFor(roles config.ActorRoles, actor string) string {
	switch {
	case slices.Contains(roles.Admins, actor):
		return "admin"
	case slices.Contains(roles.Maintainers, actor):
		return "maintainer"
	case slices.Contains(roles.Operators, actor):
		return "operator"
	case slices.Contains(roles.Requesters, actor):
		return "requester"
	default:
		return "viewer"
	}
}

func hasRequiredLabels(labels, required []string) bool {
	for _, want := range required {
		if !slices.Contains(labels, want) {
			return false
		}
	}
	return len(required) > 0
}

func allow() Decision { return Decision{Allowed: true} }
func deny(reason string) Decision {
	return Decision{Allowed: false, Reason: reason}
}
