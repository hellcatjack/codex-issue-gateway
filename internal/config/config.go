package config

import (
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	GitHub GitHubConfig `yaml:"github"`
	Queue  QueueConfig  `yaml:"queue"`
	Worker WorkerConfig `yaml:"worker"`
	Repos  []RepoConfig `yaml:"repos"`

	index map[string]RepoConfig
}

type ServerConfig struct {
	Listen        string `yaml:"listen"`
	PublicBaseURL string `yaml:"public_base_url"`
	MaxBodyBytes  int64  `yaml:"max_body_bytes"`
}

type GitHubConfig struct {
	AppID             int64  `yaml:"app_id"`
	InstallationID    int64  `yaml:"installation_id"`
	PrivateKeyFile    string `yaml:"private_key_file"`
	WebhookSecretFile string `yaml:"webhook_secret_file"`
}

type QueueConfig struct {
	DSN              string `yaml:"dsn"`
	MaxGlobalRunning int    `yaml:"max_global_running"`
}

type WorkerConfig struct {
	Enabled                      bool           `yaml:"enabled"`
	CodexBinary                  string         `yaml:"codex_binary"`
	JobRoot                      string         `yaml:"job_root"`
	StaleLeaseAfterMinutes       int            `yaml:"stale_lease_after_minutes"`
	NoActivityTimeoutMinutes     int            `yaml:"no_activity_timeout_minutes"`
	PhaseNoActivityTimeouts      map[string]int `yaml:"phase_no_activity_timeout_minutes"`
	AbsoluteJobTimeoutMinutes    int            `yaml:"absolute_job_timeout_minutes"`
	ImplementationRepairAttempts int            `yaml:"implementation_repair_attempts"`
}

type RepoConfig struct {
	FullName                   string            `yaml:"full_name"`
	CloneURL                   string            `yaml:"clone_url"`
	LocalFixturePath           string            `yaml:"local_fixture_path"`
	ForkPushRemote             string            `yaml:"fork_push_remote"`
	BaseBranches               []string          `yaml:"base_branches"`
	ProtectedBranches          []string          `yaml:"protected_branches"`
	RequiredLabelsForImplement []string          `yaml:"required_labels_for_implement"`
	AllowedActors              ActorRoles        `yaml:"allowed_actors"`
	CommitAuthor               CommitAuthor      `yaml:"commit_author"`
	AgentSetupCommands         []string          `yaml:"agent_setup_commands"`
	TestCommands               []string          `yaml:"test_commands"`
	DenyPaths                  []string          `yaml:"deny_paths"`
	ReviewRequiredPaths        []string          `yaml:"review_required_paths"`
	Codex                      CodexConfig       `yaml:"codex"`
	Concurrency                RepoConcurrency   `yaml:"concurrency"`
	Labels                     map[string]string `yaml:"labels"`
}

type ActorRoles struct {
	Admins      []string `yaml:"admins"`
	Maintainers []string `yaml:"maintainers"`
	Operators   []string `yaml:"operators"`
	Requesters  []string `yaml:"requesters"`
}

type CommitAuthor struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type CodexConfig struct {
	Sandbox        string `yaml:"sandbox"`
	AskForApproval string `yaml:"ask_for_approval"`
	Ephemeral      bool   `yaml:"ephemeral"`
	JSONEvents     bool   `yaml:"json_events"`
	AuthSourceDir  string `yaml:"auth_source_dir"`
}

type RepoConcurrency struct {
	MaxRunning int `yaml:"max_running"`
}

func Load(r io.Reader) (*Config, error) {
	var cfg Config
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.index = map[string]RepoConfig{}
	for _, repo := range cfg.Repos {
		cfg.index[repo.FullName] = repo
	}
	return &cfg, nil
}

func (c *Config) Repo(fullName string) (RepoConfig, bool) {
	repo, ok := c.index[fullName]
	return repo, ok
}

func (c *Config) applyDefaults() {
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 2 * 1024 * 1024
	}
	if c.Queue.MaxGlobalRunning == 0 {
		c.Queue.MaxGlobalRunning = 1
	}
	if c.Worker.JobRoot == "" {
		c.Worker.JobRoot = "/tmp/codex-issue-gateway/jobs"
	}
	if c.Worker.NoActivityTimeoutMinutes == 0 {
		c.Worker.NoActivityTimeoutMinutes = 45
	}
	if c.Worker.StaleLeaseAfterMinutes == 0 {
		c.Worker.StaleLeaseAfterMinutes = 15
	}
	if c.Worker.AbsoluteJobTimeoutMinutes == 0 {
		c.Worker.AbsoluteJobTimeoutMinutes = 720
	}
	if c.Worker.ImplementationRepairAttempts == 0 {
		c.Worker.ImplementationRepairAttempts = 8
	}
	if c.Worker.PhaseNoActivityTimeouts == nil {
		c.Worker.PhaseNoActivityTimeouts = map[string]int{
			"planning":     30,
			"implementing": 60,
			"testing":      45,
			"creating_pr":  20,
		}
	}
	for i := range c.Repos {
		if c.Repos[i].Codex.Sandbox == "" {
			c.Repos[i].Codex.Sandbox = "workspace-write"
		}
		if c.Repos[i].Codex.AskForApproval == "" {
			c.Repos[i].Codex.AskForApproval = "never"
		}
		if c.Repos[i].Concurrency.MaxRunning == 0 {
			c.Repos[i].Concurrency.MaxRunning = 1
		}
	}
}

func (c *Config) validate() error {
	if c.GitHub.WebhookSecretFile == "" {
		return fmt.Errorf("github.webhook_secret_file is required")
	}
	if c.Queue.DSN == "" {
		return fmt.Errorf("queue.dsn is required")
	}
	if len(c.Repos) == 0 {
		return fmt.Errorf("at least one repo is required")
	}
	seen := map[string]bool{}
	for _, repo := range c.Repos {
		if repo.FullName == "" || repo.CloneURL == "" || repo.ForkPushRemote == "" {
			return fmt.Errorf("repo full_name, clone_url, and fork_push_remote are required")
		}
		if seen[repo.FullName] {
			return fmt.Errorf("duplicate repo %q", repo.FullName)
		}
		seen[repo.FullName] = true
		if len(repo.BaseBranches) == 0 {
			return fmt.Errorf("repo %s requires base_branches", repo.FullName)
		}
		if len(repo.DenyPaths) == 0 {
			return fmt.Errorf("repo %s requires deny_paths", repo.FullName)
		}
		if len(repo.TestCommands) == 0 {
			return fmt.Errorf("repo %s requires test_commands", repo.FullName)
		}
		if repo.Codex.Sandbox == "danger-full-access" {
			return fmt.Errorf("repo %s cannot use danger-full-access", repo.FullName)
		}
		if repo.Codex.AskForApproval != "never" {
			return fmt.Errorf("repo %s must use ask_for_approval never", repo.FullName)
		}
		if slices.Contains(repo.ProtectedBranches, "") {
			return fmt.Errorf("repo %s has an empty protected branch pattern", repo.FullName)
		}
	}
	return nil
}
