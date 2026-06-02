package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/config"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
	"github.com/hellcatjack/codex-issue-gateway/internal/sandbox"
)

const playwrightBrowsersPathEnv = "PLAYWRIGHT_BROWSERS_PATH"

func EnvironmentForConfig(cfg *config.Config) map[string]string {
	if cfg == nil || !cfg.Worker.Playwright.Enabled {
		return nil
	}
	playwright := normalizePlaywrightConfig(cfg.Worker.Playwright, cfg.Worker.JobRoot)
	if strings.TrimSpace(playwright.BrowsersPath) == "" {
		return nil
	}
	return map[string]string{playwrightBrowsersPathEnv: playwright.BrowsersPath}
}

func (w *Worker) preparePlaywrightWorkspace(ctx context.Context, repo config.RepoConfig, job queue.Job, ws sandbox.Workspace) error {
	playwright, ok := w.playwrightConfig()
	if !ok {
		return nil
	}
	cli, ok := findPlaywrightCLI(ws.RepoDir)
	if !ok {
		return nil
	}
	result, err := w.Runner.RunCommands(ctx, ws.RepoDir, []string{playwrightInstallCommand(playwright, cli)}, func() {
		_ = w.Queue.TouchActivity(ctx, job.ID)
	})
	if err != nil || !result.Passed {
		report := result.Output
		if err != nil {
			return w.codexFailure(ctx, repo, job, ws, "setup", report, err)
		}
		return w.codexFailure(ctx, repo, job, ws, "setup", report, errors.New("playwright_setup_failed"))
	}
	return nil
}

func (w *Worker) playwrightConfig() (config.PlaywrightConfig, bool) {
	if w.Config == nil || !w.Config.Worker.Playwright.Enabled {
		return config.PlaywrightConfig{}, false
	}
	return normalizePlaywrightConfig(w.Config.Worker.Playwright, w.JobRoot), true
}

func normalizePlaywrightConfig(playwright config.PlaywrightConfig, jobRoot string) config.PlaywrightConfig {
	if strings.TrimSpace(playwright.BrowsersPath) == "" && strings.TrimSpace(jobRoot) != "" {
		playwright.BrowsersPath = filepath.Join(jobRoot, "_playwright-browsers")
	}
	if strings.TrimSpace(playwright.NodeBinary) == "" {
		playwright.NodeBinary = "node"
	}
	if len(playwright.Browsers) == 0 {
		playwright.Browsers = []string{"chromium"}
	}
	return playwright
}

func findPlaywrightCLI(repoDir string) (string, bool) {
	for _, candidate := range []string{
		"node_modules/@playwright/test/cli.js",
		"node_modules/playwright/cli.js",
	} {
		info, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(candidate)))
		if err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func playwrightInstallCommand(playwright config.PlaywrightConfig, cli string) string {
	parts := []string{
		playwrightBrowsersPathEnv + "=" + shellQuote(playwright.BrowsersPath),
		shellQuote(playwright.NodeBinary),
		shellQuote(cli),
		"install",
	}
	for _, browser := range playwright.Browsers {
		browser = strings.TrimSpace(browser)
		if browser == "" {
			continue
		}
		parts = append(parts, shellQuote(browser))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
