package publicreport

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	Fallback = "Agent feedback was withheld because it did not pass the public safety filter."
)

var unsafeLinePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b[A-Z0-9_]*(SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE[_-]?KEY|API[_-]?KEY|ACCESS[_-]?KEY)[A-Z0-9_]*\s*[:=]`),
	regexp.MustCompile(`(?i)\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{8,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{16,}\b`),
	regexp.MustCompile(`\bsk-(proj-)?[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?m)(^|[\s` + "`" + `'"(\[{=])/(home|app|data|tmp|var|etc|root)/[^\s` + "`" + `'"<>)\]}]+`),
	regexp.MustCompile(`(?i)[A-Z]:\\Users\\[^\s` + "`" + `]+`),
	regexp.MustCompile(`https?://[^/\s]+:[^@\s]+@`),
}

func FromCodexOutput(stdout string) string {
	return Sanitize(lastAgentMessage(stdout))
}

func Sanitize(text string) string {
	text = stripControl(strings.TrimSpace(text))
	if text == "" {
		return Fallback
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if unsafeLine(line) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			if len(kept) == 0 || blank {
				continue
			}
			blank = true
			kept = append(kept, "")
			continue
		}
		blank = false
		kept = append(kept, line)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if out == "" {
		return Fallback
	}
	return out
}

func lastAgentMessage(stdout string) string {
	var last string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" && strings.TrimSpace(event.Item.Text) != "" {
			last = event.Item.Text
		}
	}
	if last != "" {
		return last
	}
	return stdout
}

func unsafeLine(line string) bool {
	for _, pattern := range unsafeLinePatterns {
		if pattern.MatchString(line) {
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
