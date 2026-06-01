package commands

import "testing"

func TestParseFindsStandaloneCommandOnly(t *testing.T) {
	cmds, err := ParseBody("please run `/codex implement`\n/codex plan --base main\ntext", Options{AllowedBases: []string{"main"}, MaxNoActivityMinutes: 240})
	if err != nil {
		t.Fatalf("ParseBody error: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Name != Plan {
		t.Fatalf("commands = %#v", cmds)
	}
}

func TestParseRejectsMaxMinutesAndAcceptsNoActivity(t *testing.T) {
	_, err := ParseBody("/codex implement --max-minutes 5", Options{AllowedBases: []string{"main"}, MaxNoActivityMinutes: 240})
	if err == nil {
		t.Fatalf("expected --max-minutes to be rejected")
	}
	cmds, err := ParseBody("/codex implement --no-activity-minutes 60 --base main --branch codex/issue-1", Options{AllowedBases: []string{"main"}, MaxNoActivityMinutes: 240})
	if err != nil {
		t.Fatalf("ParseBody error: %v", err)
	}
	if cmds[0].Flags.NoActivityMinutes != 60 {
		t.Fatalf("no activity minutes = %d", cmds[0].Flags.NoActivityMinutes)
	}
}
