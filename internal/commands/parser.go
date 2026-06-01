package commands

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type Name string

const (
	Plan      Name = "plan"
	Implement Name = "implement"
	Fix       Name = "fix"
	Review    Name = "review"
	Retry     Name = "retry"
	Cancel    Name = "cancel"
	Status    Name = "status"
)

type Options struct {
	AllowedBases         []string
	MaxNoActivityMinutes int
}

type Command struct {
	Name  Name
	Flags Flags
	Raw   string
}

type Flags struct {
	Branch            string
	Base              string
	DryRun            bool
	NoActivityMinutes int
}

func ParseBody(body string, opts Options) ([]Command, error) {
	var out []Command
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "/codex ") {
			continue
		}
		cmd, err := parseLine(trimmed, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, cmd)
	}
	return out, nil
}

func parseLine(line string, opts Options) (Command, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 || parts[0] != "/codex" {
		return Command{}, fmt.Errorf("invalid command line")
	}
	name := Name(parts[1])
	if !slices.Contains([]Name{Plan, Implement, Fix, Review, Retry, Cancel, Status}, name) {
		return Command{}, fmt.Errorf("unknown command %q", name)
	}
	cmd := Command{Name: name, Raw: line}
	for i := 2; i < len(parts); i++ {
		switch parts[i] {
		case "--dry-run":
			cmd.Flags.DryRun = true
		case "--branch":
			i++
			if i >= len(parts) || !safeBranch(parts[i]) {
				return Command{}, fmt.Errorf("invalid --branch")
			}
			cmd.Flags.Branch = parts[i]
		case "--base":
			i++
			if i >= len(parts) || !slices.Contains(opts.AllowedBases, parts[i]) {
				return Command{}, fmt.Errorf("invalid --base")
			}
			cmd.Flags.Base = parts[i]
		case "--no-activity-minutes":
			i++
			if i >= len(parts) {
				return Command{}, fmt.Errorf("missing --no-activity-minutes value")
			}
			n, err := strconv.Atoi(parts[i])
			if err != nil || n < 30 || n > opts.MaxNoActivityMinutes {
				return Command{}, fmt.Errorf("invalid --no-activity-minutes")
			}
			cmd.Flags.NoActivityMinutes = n
		default:
			return Command{}, fmt.Errorf("unknown flag %q", parts[i])
		}
	}
	return cmd, nil
}

func safeBranch(s string) bool {
	if len(s) == 0 || len(s) > 80 || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '/' {
			continue
		}
		return false
	}
	return true
}
