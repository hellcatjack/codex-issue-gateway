package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/github"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
	"github.com/hellcatjack/codex-issue-gateway/internal/server"
	"github.com/hellcatjack/codex-issue-gateway/internal/worker"
)

type Runtime struct {
	Config             *config.Config
	Queue              *queue.Store
	GitHub             github.Client
	ServerDependencies server.Dependencies
}

func LoadRuntime(ctx context.Context, configPath string) (*Runtime, func() error, error) {
	cfgFile, err := os.Open(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open config: %w", err)
	}
	defer cfgFile.Close()

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	secret, err := os.ReadFile(cfg.GitHub.WebhookSecretFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read github webhook secret: %w", err)
	}
	store, err := queue.Open(ctx, cfg.Queue.DSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open queue: %w", err)
	}

	gh := githubClientForConfig(cfg)
	runtime := &Runtime{
		Config: cfg,
		Queue:  store,
		GitHub: gh,
		ServerDependencies: server.Dependencies{
			Config:        cfg,
			Queue:         store,
			GitHub:        gh,
			WebhookSecret: bytes.TrimSpace(secret),
		},
	}
	return runtime, store.Close, nil
}

func StartWorkerLoop(ctx context.Context, runtime *Runtime) func() {
	if runtime == nil || runtime.Config == nil || !runtime.Config.Worker.Enabled {
		return func() {}
	}
	workerCtx, cancel := context.WithCancel(ctx)
	w := buildWorker(runtime)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			_ = w.RunOne(workerCtx)
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return cancel
}

func buildWorker(runtime *Runtime) *worker.Worker {
	return &worker.Worker{
		Queue:   runtime.Queue,
		GitHub:  runtime.GitHub,
		Runner:  worker.LocalRunner{CodexBinary: runtime.Config.Worker.CodexBinary},
		Diff:    worker.GitDiffScanner{},
		JobRoot: runtime.Config.Worker.JobRoot,
		Config:  runtime.Config,
	}
}

func githubClientForConfig(cfg *config.Config) github.Client {
	if cfg.GitHub.PrivateKeyFile == "" || cfg.GitHub.AppID == 0 || cfg.GitHub.InstallationID == 0 {
		return github.NewFake()
	}
	privateKey, err := os.ReadFile(cfg.GitHub.PrivateKeyFile)
	if err != nil {
		return github.NewFake()
	}
	return github.NewAppClient(github.AppClientOptions{
		AppID:          cfg.GitHub.AppID,
		InstallationID: cfg.GitHub.InstallationID,
		PrivateKeyPEM:  privateKey,
	})
}
