package issuecontext

import (
	"fmt"
	"regexp"
	"strings"
)

type Snapshot struct {
	RepoFullName       string
	Number             int
	Title              string
	Body               string
	Author             string
	AuthorAssociation  string
	State              string
	Locked             bool
	Labels             []string
	Comments           []Comment
	FetchedDescription string
}

type Comment struct {
	ID                int64
	Author            string
	AuthorAssociation string
	Body              string
}

var (
	markdownImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+["'][^)]*["'])?\)`)
	htmlImagePattern     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	htmlAltPattern       = regexp.MustCompile(`(?is)\balt\s*=\s*["']([^"']*)["']`)
)

func BuildPromptContext(snapshot Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s\nIssue: #%d\n", snapshot.RepoFullName, snapshot.Number)
	if collaboratorAssociation(snapshot.AuthorAssociation) {
		if strings.TrimSpace(snapshot.Title) != "" {
			fmt.Fprintf(&b, "Title: %s\n", strings.TrimSpace(snapshot.Title))
		}
		if strings.TrimSpace(snapshot.Body) != "" {
			fmt.Fprintf(&b, "\nIssue body by %s:\n%s\n", snapshot.Author, strings.TrimSpace(redactImageURLs(snapshot.Body)))
		}
	}
	for _, comment := range snapshot.Comments {
		if !collaboratorAssociation(comment.AuthorAssociation) || strings.TrimSpace(comment.Body) == "" {
			continue
		}
		fmt.Fprintf(&b, "\nComment by %s:\n%s\n", comment.Author, strings.TrimSpace(redactImageURLs(comment.Body)))
	}
	return strings.TrimSpace(b.String())
}

func CommandSource(snapshot Snapshot, commentID int64, commandActor string) (string, bool) {
	if commentID == 0 {
		if snapshot.Author == commandActor {
			return snapshot.Body, true
		}
		return "", false
	}
	for _, comment := range snapshot.Comments {
		if comment.ID == commentID && comment.Author == commandActor {
			return comment.Body, true
		}
	}
	return "", false
}

func collaboratorAssociation(value string) bool {
	switch strings.ToUpper(value) {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	default:
		return false
	}
}

func redactImageURLs(value string) string {
	value = markdownImagePattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := markdownImagePattern.FindStringSubmatch(match)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return "[image]"
		}
		return "[image: " + safeImageAlt(parts[1]) + "]"
	})
	value = htmlImagePattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := htmlAltPattern.FindStringSubmatch(match)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return "[image]"
		}
		return "[image: " + safeImageAlt(parts[1]) + "]"
	})
	return value
}

func safeImageAlt(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "attached image"
	}
	return value
}
