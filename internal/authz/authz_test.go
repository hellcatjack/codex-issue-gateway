package authz

import (
	"context"
	"testing"

	"github.com/hellcatjack/codex-issue-gateway/internal/commands"
	"github.com/hellcatjack/codex-issue-gateway/internal/config"
)

func TestMaintainerCanImplementWithoutReadyLabel(t *testing.T) {
	decision := Authorize(context.Background(), Input{
		Repo:         repoPolicy(),
		Actor:        "hellcatjack",
		Command:      commands.Command{Name: commands.Implement},
		IssueLabels:  nil,
		IssueHash:    "h1",
		HasActiveJob: false,
		ReadyPlan:    ReadyPlan{Ready: false},
	})
	if !decision.Allowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRequesterImplementRequiresReadyPlanOrLabel(t *testing.T) {
	decision := Authorize(context.Background(), Input{
		Repo:         repoPolicyWithRequester(),
		Actor:        "bob",
		Command:      commands.Command{Name: commands.Implement},
		IssueLabels:  nil,
		IssueHash:    "h1",
		HasActiveJob: false,
		ReadyPlan:    ReadyPlan{Ready: false},
	})
	if decision.Allowed || decision.Reason != "label_required" {
		t.Fatalf("decision = %#v", decision)
	}
	decision = Authorize(context.Background(), Input{
		Repo:         repoPolicyWithRequester(),
		Actor:        "bob",
		Command:      commands.Command{Name: commands.Implement},
		IssueLabels:  []string{"codex:ready"},
		IssueHash:    "h1",
		HasActiveJob: false,
		ReadyPlan:    ReadyPlan{Ready: false},
	})
	if !decision.Allowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestStatusDoesNotRequireReadyPlan(t *testing.T) {
	decision := Authorize(context.Background(), Input{
		Repo:         repoPolicyWithRequester(),
		Actor:        "bob",
		Command:      commands.Command{Name: commands.Status},
		HasActiveJob: true,
	})
	if !decision.Allowed {
		t.Fatalf("decision = %#v", decision)
	}
}

func repoPolicy() config.RepoConfig {
	return config.RepoConfig{
		FullName:                   "funland/foliospace-Library",
		BaseBranches:               []string{"main"},
		RequiredLabelsForImplement: []string{"codex:ready"},
		AllowedActors:              config.ActorRoles{Maintainers: []string{"hellcatjack"}},
	}
}

func repoPolicyWithRequester() config.RepoConfig {
	repo := repoPolicy()
	repo.AllowedActors.Requesters = []string{"bob"}
	return repo
}
