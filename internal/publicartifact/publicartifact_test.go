package publicartifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPreviewsPlanMarkdownWithUsefulSections(t *testing.T) {
	repoDir := t.TempDir()
	path := filepath.Join(repoDir, "docs", "superpowers", "plans")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"# PWA Icon Metadata Documentation Implementation Plan",
		"",
		"> **For agentic workers:** internal execution instructions.",
		"",
		"**Goal:** Document how EPUB Reader maintains PWA install icon metadata without changing runtime behavior.",
		"",
		"**Architecture:** Add a README note near the existing PWA deployment documentation.",
		"",
		"**Tech Stack:** Markdown documentation, Vite PWA manifest configuration",
		"",
		"### Task 1: Confirm Current Icon Metadata Context",
		"",
		"**Files:**",
		"- Read: `README.md`",
		"- Read: `vite.config.ts`",
		"- Modify: `README.md`",
		"- [ ] **Step 1: Keep internal execution detail out of the public preview**",
		"",
		"### Task 2: Verify the Documentation-Only Change",
		"",
		"- [ ] **Step 1: Run the configured unit test suite**",
		"",
		"Run: `npm test`",
		"",
		"Run:",
		"",
		"```bash",
		"npm run build",
		"```",
		"",
		"Expected: PASS.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(path, "example.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Build(repoDir, []string{"docs/superpowers/plans/example.md"}, nil)

	for _, want := range []string{
		"Artifact preview:",
		"`docs/superpowers/plans/example.md`",
		"**Goal:** Document how EPUB Reader maintains PWA install icon metadata",
		"**Architecture:** Add a README note",
		"### Task 1: Confirm Current Icon Metadata Context",
		"**Files:**",
		"- Read: `README.md`",
		"Run: `npm test`",
		"```bash\nnpm run build\n```",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "For agentic workers") {
		t.Fatalf("preview leaked internal execution note:\n%s", got)
	}
	if strings.Contains(got, "- [ ]") {
		t.Fatalf("preview leaked checkbox execution details:\n%s", got)
	}
}

func TestBuildPreviewsPlanMarkdownWithReadableMarkdownStructure(t *testing.T) {
	repoDir := t.TempDir()
	path := filepath.Join(repoDir, "docs", "superpowers", "plans")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"# TTS Translation Comparison Surface Implementation Plan",
		"",
		"> **For agentic workers:** REQUIRED SUB-SKILL: internal instructions.",
		"",
		"**Goal:** Keep the translated sentence near the spoken English sentence.",
		"",
		"**Architecture:** Prefer adjacent placement, then fall back to the side lane.",
		"",
		"**Tech Stack:** React, TypeScript, CSS, Vitest, Playwright",
		"",
		"---",
		"",
		"## File Map",
		"",
		"### Modified files",
		"",
		"- `src/features/reader/ReaderPage.tsx`",
		"  - Update `resolveTtsSentenceNotePlacement`.",
		"- `src/features/reader/reader.css`",
		"  - Restyle the note as a compact comparison surface.",
		"",
		"## Task List",
		"",
		"### Task 1: Define placement behavior with unit tests",
		"",
		"**Files:**",
		"- Modify: `src/features/reader/ReaderPage.test.tsx`",
		"- Modify: `src/features/reader/ReaderPage.tsx`",
		"",
		"- [ ] Add focused tests for placement.",
		"- [ ] Run:",
		"",
		"```bash",
		"npm test -- src/features/reader/ReaderPage.test.tsx",
		"```",
		"",
		"## Changed Behavior",
		"",
		"- Before: the translation note is pinned far away.",
		"- After: the translation note appears near the active sentence.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(path, "plan.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Build(repoDir, []string{"docs/superpowers/plans/plan.md"}, nil)

	for _, want := range []string{
		"# TTS Translation Comparison Surface Implementation Plan",
		"**Goal:** Keep the translated sentence near the spoken English sentence.",
		"## File Map",
		"### Modified files",
		"  - Update `resolveTtsSentenceNotePlacement`.",
		"### Task 1: Define placement behavior with unit tests",
		"- Add focused tests for placement.",
		"```bash\nnpm test -- src/features/reader/ReaderPage.test.tsx\n```",
		"## Changed Behavior",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("structured preview missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"For agentic workers",
		"- [ ]",
		"File Map\nNew files\nModified files",
		"Task List\nTask 1:",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("structured preview contains ugly/internal fragment %q:\n%s", unwanted, got)
		}
	}
}

func TestBuildDropsUnsafePreviewLinesAndDenylistedFiles(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("OPENAI_API_KEY=sk-proj-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"**Goal:** Safe public goal.",
		"",
		"**Architecture:** Uses /app/devs/private for local testing.",
		"",
		"Run: `go test ./...`",
		"",
		"TOKEN=ghp_secretsecret",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(repoDir, "plan.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Build(repoDir, []string{".env", "plan.md", "/tmp/escape.md", "../escape.md"}, []string{".env"})

	for _, want := range []string{"Safe public goal", "Run: `go test ./...`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q:\n%s", want, got)
		}
	}
	for _, leaked := range []string{"OPENAI_API_KEY", "sk-proj-secret", "/app/devs", "TOKEN=", ".env", "/tmp/escape", "../escape"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("preview leaked %q:\n%s", leaked, got)
		}
	}
}

func TestBuildPreviewsHeadingParagraphPlanMarkdown(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "docs", "superpowers", "plans"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"# Reader Persistence Checklist Note Plan",
		"",
		"## Goal",
		"",
		"Add a concise maintainer note at `docs/reader-persistence-checklist.md`.",
		"",
		"## Architecture",
		"",
		"This is a documentation-only change.",
		"",
		"## Changed Files",
		"",
		"- `docs/reader-persistence-checklist.md`: new checklist.",
		"",
		"## Verification Commands",
		"",
		"```sh",
		"git diff -- docs/reader-persistence-checklist.md",
		"npm test",
		"npm run build",
		"```",
		"",
	}, "\n")
	rel := "docs/superpowers/plans/plan.md"
	if err := os.WriteFile(filepath.Join(repoDir, filepath.FromSlash(rel)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Build(repoDir, []string{rel}, nil)

	for _, want := range []string{
		"Reader Persistence Checklist Note Plan",
		"## Goal\n\nAdd a concise maintainer note",
		"## Architecture\n\nThis is a documentation-only change.",
		"## Changed Files",
		"- `docs/reader-persistence-checklist.md`: new checklist.",
		"```sh\ngit diff -- docs/reader-persistence-checklist.md\nnpm test\nnpm run build\n```",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q:\n%s", want, got)
		}
	}
}

func TestBuildPreviewsRegularMarkdownDocumentBody(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"# Reader Persistence Checklist",
		"",
		"Use this quick check before changing reader storage, import, or resume behavior.",
		"",
		"- Import a sample EPUB through the app.",
		"- Close the app tab or window, then reopen the app.",
		"- Confirm the imported book is still available in the library.",
		"- Open the book and confirm the last reading position or reader state is restored.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(repoDir, "docs", "reader-persistence-checklist.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Build(repoDir, []string{"docs/reader-persistence-checklist.md"}, nil)

	for _, want := range []string{
		"Reader Persistence Checklist",
		"Use this quick check before changing reader storage",
		"- Import a sample EPUB through the app.",
		"- Confirm the imported book is still available in the library.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q:\n%s", want, got)
		}
	}
}

func TestBuildPreviewsAllSafeMarkdownArtifactsWithoutLineLimit(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	var files []string
	for fileIndex := 0; fileIndex < 6; fileIndex++ {
		rel := fmt.Sprintf("docs/preview-%d.md", fileIndex)
		files = append(files, rel)
		lines := []string{
			fmt.Sprintf("# Long Preview %d", fileIndex),
			"",
			fmt.Sprintf("Safe overview for preview file %d.", fileIndex),
			"",
		}
		for lineIndex := 0; lineIndex < 50; lineIndex++ {
			lines = append(lines, fmt.Sprintf("- Item %02d for file %d", lineIndex, fileIndex))
		}
		if err := os.WriteFile(filepath.Join(repoDir, filepath.FromSlash(rel)), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got := Build(repoDir, files, nil)

	for _, want := range []string{
		"`docs/preview-0.md`",
		"`docs/preview-5.md`",
		"- Item 49 for file 0",
		"- Item 49 for file 5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[truncated]") {
		t.Fatalf("preview should not be marked truncated:\n%s", got)
	}
}
