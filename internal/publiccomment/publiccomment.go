package publiccomment

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	Fallback                  = "Codex Gateway 状态已更新。\n\n详情已保存在内部审计日志中。"
	MaxIssueCommentChunkBytes = 60 * 1024
	MaxPublicTitleBytes       = 120
	FallbackTitle             = "Codex Gateway update"
)

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b[A-Z0-9_]*(SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE[_-]?KEY|API[_-]?KEY|ACCESS[_-]?KEY)[A-Z0-9_]*\s*[:=]`),
	regexp.MustCompile(`(?i)\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{8,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{16,}\b`),
	regexp.MustCompile(`\bsk-(proj-)?[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?m)(^|\s)/(home|app|data|tmp|var|etc|root)/[^\s` + "`" + `]+`),
	regexp.MustCompile(`(?i)[A-Z]:\\Users\\[^\s` + "`" + `]+`),
	regexp.MustCompile(`https?://[^/\s]+:[^@\s]+@`),
}

func Safe(body string) string {
	body = strings.TrimSpace(stripControl(body))
	if body == "" {
		return Fallback
	}
	if containsSensitive(body) {
		return Fallback
	}
	return body
}

func SafeChunks(body string) []string {
	body = Safe(body)
	if len(body) <= MaxIssueCommentChunkBytes {
		return []string{body}
	}
	var chunks []string
	for len(body) > MaxIssueCommentChunkBytes {
		cut := commentChunkCut(body, MaxIssueCommentChunkBytes)
		chunks = append(chunks, body[:cut])
		body = body[cut:]
	}
	if body != "" {
		chunks = append(chunks, body)
	}
	return chunks
}

func SafeTitle(title string) string {
	title = strings.TrimSpace(stripControl(title))
	if title == "" || containsSensitive(title) {
		return FallbackTitle
	}
	if len([]byte(title)) <= MaxPublicTitleBytes {
		return title
	}
	return trimUTF8Bytes(title, MaxPublicTitleBytes)
}

func containsSensitive(body string) bool {
	for _, pattern := range sensitivePatterns {
		if pattern.MatchString(body) {
			return true
		}
	}
	return false
}

func stripControl(body string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, body)
}

func commentChunkCut(body string, maxBytes int) int {
	cut := utf8SafeCut(body, maxBytes)
	if newline := strings.LastIndex(body[:cut], "\n"); newline > 0 && newline+1 >= maxBytes/2 {
		return newline + 1
	}
	return cut
}

func utf8SafeCut(body string, maxBytes int) int {
	if maxBytes >= len(body) {
		return len(body)
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	if cut <= 0 {
		_, size := utf8.DecodeRuneInString(body)
		return size
	}
	return cut
}

func trimUTF8Bytes(body string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	for len([]byte(body)) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(body)
		if size <= 0 {
			return ""
		}
		body = body[:len(body)-size]
	}
	return body
}
