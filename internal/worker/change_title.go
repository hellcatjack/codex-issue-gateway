package worker

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hellcatjack/codex-issue-gateway/internal/publiccomment"
	"github.com/hellcatjack/codex-issue-gateway/internal/queue"
)

func implementationChangeTitle(job queue.Job, result CodexResult, files []string) string {
	fallback := fallbackChangeTitle(job)
	for _, candidate := range []string{
		firstSummaryBullet(result.PublicReport),
		result.Summary,
		changedFilesTitle(files),
	} {
		candidate = cleanChangeTitle(candidate)
		if candidate == "" {
			continue
		}
		prefix := "Codex: "
		suffix := " (#" + itoa(job.IssueNumber) + ")"
		candidate = trimTitleBytes(candidate, publiccomment.MaxPublicTitleBytes-len(prefix)-len(suffix))
		if candidate == "" {
			continue
		}
		title := prefix + candidate + suffix
		safe := publiccomment.SafeTitle(title)
		if safe != publiccomment.FallbackTitle {
			return safe
		}
	}
	return fallback
}

func fallbackChangeTitle(job queue.Job) string {
	return "Codex changes for issue #" + itoa(job.IssueNumber)
}

func firstSummaryBullet(report string) string {
	lines := strings.Split(report, "\n")
	inSummary := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if inSummary {
				return ""
			}
			continue
		}
		if summaryHeading(line) {
			inSummary = true
			continue
		}
		if !inSummary {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "*") {
			return ""
		}
		return line
	}
	return ""
}

func summaryHeading(line string) bool {
	line = strings.Trim(line, "*_# \t")
	line = strings.TrimSuffix(line, ":")
	return strings.EqualFold(strings.TrimSpace(line), "Summary")
}

func changedFilesTitle(files []string) string {
	if len(files) == 0 {
		return ""
	}
	first := strings.TrimSpace(filepath.ToSlash(files[0]))
	if first == "" {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(first))
	if dir == "." || dir == "" {
		return "Update " + filepath.Base(first)
	}
	return "Update " + dir
}

func cleanChangeTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimSpace(stripListMarker(title))
	title = strings.TrimSpace(title)
	title = strings.TrimSuffix(title, ".")
	return strings.TrimSpace(title)
}

func stripListMarker(title string) string {
	if title == "" {
		return ""
	}
	for _, marker := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(title, marker) {
			return title[len(marker):]
		}
	}
	digits := 0
	for _, r := range title {
		if r < '0' || r > '9' {
			break
		}
		digits++
	}
	if digits == 0 || digits >= len(title) {
		return title
	}
	if (title[digits] == '.' || title[digits] == ')') && digits+1 < len(title) && unicode.IsSpace(rune(title[digits+1])) {
		return title[digits+2:]
	}
	return title
}

func trimTitleBytes(title string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	title = strings.TrimSpace(title)
	for len([]byte(title)) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(title)
		if size <= 0 {
			return ""
		}
		title = strings.TrimSpace(title[:len(title)-size])
	}
	return title
}
