package issuecontext

import (
	"strings"
	"testing"
)

func TestPromptIncludesOnlyCollaboratorSpeech(t *testing.T) {
	snapshot := Snapshot{
		Title:             "outsider title",
		Body:              "outsider body should be ignored",
		Author:            "alice",
		AuthorAssociation: "NONE",
		Comments: []Comment{
			{Author: "hellcatjack", AuthorAssociation: "OWNER", Body: "maintainer requirement"},
			{Author: "bob", AuthorAssociation: "NONE", Body: "untrusted instruction"},
			{Author: "carol", AuthorAssociation: "COLLABORATOR", Body: "collaborator context"},
		},
	}

	got := BuildPromptContext(snapshot)

	for _, want := range []string{"maintainer requirement", "collaborator context"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %s", want, got)
		}
	}
	for _, leaked := range []string{"outsider body", "untrusted instruction", "outsider title"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("prompt leaked non-collaborator content %q: %s", leaked, got)
		}
	}
}

func TestPromptRedactsImageURLsButKeepsImageAltText(t *testing.T) {
	snapshot := Snapshot{
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Body:              "Please match this:\n![Reader state](https://github.com/user-attachments/assets/abc)\n<img alt=\"Dialog state\" src=\"https://user-images.githubusercontent.com/1/dialog.png\">",
	}

	got := BuildPromptContext(snapshot)

	for _, want := range []string{"[image: Reader state]", "[image: Dialog state]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %s", want, got)
		}
	}
	for _, leaked := range []string{"https://github.com/user-attachments/assets/abc", "https://user-images.githubusercontent.com/1/dialog.png"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("prompt leaked image URL %q: %s", leaked, got)
		}
	}
}

func TestPromptContextDoesNotTruncateTrustedIssueText(t *testing.T) {
	longTitleTail := "title tail marker after long title"
	longBodyTail := "body tail marker after long issue body"
	longCommentTail := "comment tail marker after long comment"
	snapshot := Snapshot{
		Title:             strings.Repeat("title ", 700) + longTitleTail,
		Body:              strings.Repeat("issue body line\n", 1400) + longBodyTail,
		Author:            "hellcatjack",
		AuthorAssociation: "OWNER",
		Comments: []Comment{
			{Author: "hellcatjack", AuthorAssociation: "OWNER", Body: strings.Repeat("comment line\n", 1000) + longCommentTail},
		},
	}

	got := BuildPromptContext(snapshot)

	for _, want := range []string{longTitleTail, longBodyTail, longCommentTail} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing long trusted content tail %q", want)
		}
	}
	if strings.Contains(got, "[truncated]") {
		t.Fatalf("prompt self-truncated trusted issue text")
	}
}

func TestTriggerCommandRequiresHellcatjack(t *testing.T) {
	snapshot := Snapshot{
		Body:              "/codex plan",
		Author:            "alice",
		AuthorAssociation: "COLLABORATOR",
		Comments: []Comment{
			{ID: 10, Author: "bob", AuthorAssociation: "COLLABORATOR", Body: "/codex implement"},
			{ID: 11, Author: "hellcatjack", AuthorAssociation: "OWNER", Body: "/codex implement"},
		},
	}

	if _, ok := CommandSource(snapshot, 10, "hellcatjack"); ok {
		t.Fatal("accepted command from non-command actor")
	}
	body, ok := CommandSource(snapshot, 11, "hellcatjack")
	if !ok || body != "/codex implement" {
		t.Fatalf("body=%q ok=%v", body, ok)
	}
	if _, ok := CommandSource(snapshot, 0, "hellcatjack"); ok {
		t.Fatal("accepted issue body command from non-command actor")
	}
}
