package publicartifact

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hellcatjack/codex-issue-gateway/internal/diffpolicy"
	"github.com/hellcatjack/codex-issue-gateway/internal/publicreport"
)

const (
	previewUnavailable = "Preview withheld by public safety filter."
)

var previewExtensions = []string{".md", ".markdown", ".txt", ".rst", ".json", ".yaml", ".yml"}

func Build(repoDir string, files []string, denyPaths []string) string {
	repoDir = strings.TrimSpace(repoDir)
	if repoDir == "" || len(files) == 0 {
		return ""
	}
	var sections []string
	for _, file := range files {
		section := previewFile(repoDir, file, denyPaths)
		if section == "" {
			continue
		}
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		return ""
	}
	return "Artifact preview:\n" + strings.Join(sections, "\n\n")
}

func previewFile(repoDir, file string, denyPaths []string) string {
	rel, ok := safeRelativePath(file)
	if !ok || denied(rel, denyPaths) {
		return ""
	}
	fullPath := filepath.Join(repoDir, filepath.FromSlash(rel))
	info, err := os.Lstat(fullPath)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	if suspiciousPath(rel) {
		return ""
	}
	ext := strings.ToLower(path.Ext(rel))
	if !slices.Contains(previewExtensions, ext) {
		return fmt.Sprintf("- `%s` (%s, preview unavailable for this file type)", rel, formatBytes(info.Size()))
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ""
	}
	lines := extractLines(rel, string(data), ext)
	if len(lines) == 0 {
		lines = []string{previewUnavailable}
	}
	body := strings.Join(lines, "\n")
	return fmt.Sprintf("<details>\n<summary>`%s` (%s, %s)</summary>\n\n%s\n</details>", rel, fileKind(ext), formatBytes(info.Size()), body)
}

func safeRelativePath(file string) (string, bool) {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if file == "" || strings.HasPrefix(file, "/") || strings.ContainsRune(file, 0) {
		return "", false
	}
	clean := path.Clean(file)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func denied(file string, denyPaths []string) bool {
	result := diffpolicy.Evaluate(diffpolicy.Input{ChangedFiles: []string{file}, DenyPaths: denyPaths})
	return !result.Allowed
}

func suspiciousPath(file string) bool {
	lower := strings.ToLower(path.Base(file))
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}
	for _, marker := range []string{"secret", "token", "password", "passwd", "private-key", "private_key", "api-key", "api_key", "access-key", "access_key"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func extractLines(rel, body, ext string) []string {
	if ext == ".json" || ext == ".yaml" || ext == ".yml" {
		return extractGenericLines(body)
	}
	if (ext == ".md" || ext == ".markdown") && strings.Contains(rel, "docs/superpowers/plans/") {
		return extractPlanMarkdownLines(body)
	}
	if (ext == ".md" || ext == ".markdown") && !strings.Contains(rel, "docs/superpowers/plans/") {
		return extractMarkdownDocumentLines(body)
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var kept []string
	inFiles := false
	awaitingRunCommand := false
	inRunFence := false
	section := ""
	inSectionFence := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if section == "Verification Commands" {
			if strings.HasPrefix(line, "```") {
				inSectionFence = !inSectionFence
				continue
			}
			if inSectionFence {
				addLine(&kept, "Run: `"+strings.Trim(line, "`")+"`")
				continue
			}
		}
		if awaitingRunCommand {
			if strings.HasPrefix(line, "```") {
				inRunFence = !inRunFence
				continue
			}
			if strings.HasPrefix(normalizeMarkdownLine(line), "Expected:") {
				awaitingRunCommand = false
				inRunFence = false
			} else if inRunFence {
				addLine(&kept, "Run: `"+strings.Trim(line, "`")+"`")
				continue
			}
		}
		if strings.HasPrefix(line, ">") || strings.Contains(line, "For agentic workers") {
			continue
		}
		normalized := normalizeMarkdownLine(line)
		if section == "Goal" || section == "Architecture" || section == "Tech Stack" {
			addLine(&kept, section+": "+normalized)
			section = ""
			continue
		}
		if section == "Changed Files" && strings.HasPrefix(normalized, "- ") {
			addLine(&kept, normalized)
			continue
		}
		switch {
		case strings.HasPrefix(line, "#"):
			inFiles = false
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if previewSection(heading) {
				section = heading
				if heading == "Changed Files" {
					addLine(&kept, "Changed Files:")
				}
				continue
			}
			section = ""
			addLine(&kept, heading)
		case normalized == "Files:":
			inFiles = true
			addLine(&kept, "Files:")
		case inFiles && fileOperationBullet(normalized):
			addLine(&kept, normalized)
		case strings.HasPrefix(normalized, "Goal:"):
			inFiles = false
			addLine(&kept, normalized)
		case strings.HasPrefix(normalized, "Architecture:"):
			inFiles = false
			addLine(&kept, normalized)
		case strings.HasPrefix(normalized, "Tech Stack:"):
			inFiles = false
			addLine(&kept, normalized)
		case strings.HasPrefix(normalized, "Task "):
			inFiles = false
			section = ""
			addLine(&kept, normalized)
		case normalized == "Run:":
			inFiles = false
			section = ""
			awaitingRunCommand = true
			inRunFence = false
		case strings.HasPrefix(normalized, "Run:"):
			inFiles = false
			section = ""
			awaitingRunCommand = false
			inRunFence = false
			addLine(&kept, normalized)
		}
	}
	return kept
}

func extractPlanMarkdownLines(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var kept []string
	blank := false
	inFence := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if addPlanLine(&kept, trimmed) {
				inFence = !inFence
				blank = false
			}
			continue
		}
		if inFence {
			if addPlanLine(&kept, line) {
				blank = false
			}
			continue
		}
		if trimmed == "" {
			if len(kept) == 0 || blank {
				continue
			}
			kept = append(kept, "")
			blank = true
			continue
		}
		if strings.HasPrefix(trimmed, ">") || strings.Contains(trimmed, "For agentic workers") {
			continue
		}
		line = strings.Replace(line, "- [ ] ", "- ", 1)
		if addPlanLine(&kept, line) {
			blank = false
		}
	}
	return trimBlankLines(kept)
}

func extractMarkdownDocumentLines(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var kept []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ">") || strings.Contains(line, "For agentic workers") {
			continue
		}
		normalized := normalizeMarkdownLine(line)
		if strings.HasPrefix(normalized, "- [ ]") {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(normalized, "- ") || len(kept) < 8 {
			addLine(&kept, normalized)
		}
	}
	return kept
}

func addPlanLine(lines *[]string, line string) bool {
	if line == "" {
		return false
	}
	if publicreport.Sanitize(strings.TrimSpace(line)) != strings.TrimSpace(line) {
		return false
	}
	*lines = append(*lines, line)
	return true
}

func trimBlankLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func previewSection(heading string) bool {
	switch heading {
	case "Goal", "Architecture", "Tech Stack", "Changed Files", "Verification Commands":
		return true
	default:
		return false
	}
}

func fileOperationBullet(line string) bool {
	for _, prefix := range []string{"- Add:", "- Create:", "- Modify:", "- Read:", "- Verify:", "- Test:"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func extractGenericLines(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var kept []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		addLine(&kept, line)
	}
	return kept
}

func normalizeMarkdownLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "#")
	line = strings.TrimSpace(line)
	line = strings.ReplaceAll(line, "**", "")
	line = strings.ReplaceAll(line, "__", "")
	return strings.TrimSpace(line)
}

func addLine(lines *[]string, line string) {
	line = strings.TrimRight(line, " \t")
	if line == "" {
		return
	}
	if publicreport.Sanitize(line) != line {
		return
	}
	*lines = append(*lines, line)
}

func fileKind(ext string) string {
	switch ext {
	case ".md", ".markdown":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	default:
		return "text"
	}
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f KB", float64(size)/1024)
}
