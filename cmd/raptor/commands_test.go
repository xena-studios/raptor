package main

import (
	"errors"
	"flag"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The docs site's reference for the raptor command.
const referencePage = "../../docs-site/src/content/docs/cli/reference.md"

func commandNames() []string {
	names := make([]string, len(cliCommands))
	for i, c := range cliCommands {
		names[i] = c.name
	}
	return names
}

// Every command in the list reaches its own flags, so the list can't name
// a command run doesn't have.
func TestEveryCommandIsRouted(t *testing.T) {
	stderr := os.Stderr
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = devnull // the flag sets print their help there
	t.Cleanup(func() { os.Stderr = stderr; _ = devnull.Close() })

	for _, c := range cliCommands {
		if c.name == "version" {
			continue // has no flags
		}
		args := append(strings.Fields(c.name), "-h")
		if err := run(args); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("raptor %s -h: %v, want its flags' help", c.name, err)
		}
	}
	if err := run([]string{"no-such-command"}); !errors.Is(err, errUsage) {
		t.Errorf("an unknown command: %v", err)
	}
}

// The docs site names every command people run, and nothing else.
func TestReferencePageMatchesCommands(t *testing.T) {
	data, err := os.ReadFile(referencePage)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, c := range cliCommands {
		if c.internal {
			continue
		}
		if !regexp.MustCompile(`raptor ` + regexp.QuoteMeta(c.name) + "([ `]|$)").MatchString(page) {
			t.Errorf("%s doesn't mention raptor %s", referencePage, c.name)
		}
	}
	names := commandNames()
	mention := regexp.MustCompile("`(?:sudo )?raptor ([a-z-]+)(?: ([a-z-]+))?")
	for _, m := range mention.FindAllStringSubmatch(page, -1) {
		if two := m[1] + " " + m[2]; m[2] != "" && slices.Contains(names, two) {
			continue
		}
		if !slices.Contains(names, m[1]) {
			t.Errorf("%s mentions %q, which isn't a command (or is missing from cliCommands)", referencePage, strings.Trim(m[0], "`"))
		}
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	for _, c := range cliCommands {
		if !strings.Contains(usage, "\n  "+c.name) {
			t.Errorf("usage doesn't list %s", c.name)
		}
	}
	for line := range strings.Lines(usage) {
		if len(strings.TrimRight(line, "\n")) > 80 {
			t.Errorf("usage line over 80 characters: %q", line)
		}
	}
}
