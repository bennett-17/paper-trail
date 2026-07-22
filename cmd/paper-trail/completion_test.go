package main

import (
	"strings"
	"testing"
)

func TestOrderedCompletionCommandsMatchesCompletionCommandsOrder(t *testing.T) {
	got := orderedCompletionCommands()
	if len(got) != len(completionFlags) {
		t.Fatalf("got %d commands, want one per entry in completionFlags (%d)", len(got), len(completionFlags))
	}

	// Every returned command must actually have a flags entry, and the
	// order must match completionCommands, not map iteration order.
	seen := make(map[string]bool)
	lastIdx := -1
	for _, cmd := range got {
		if _, ok := completionFlags[cmd]; !ok {
			t.Errorf("%q has no entry in completionFlags", cmd)
		}
		if seen[cmd] {
			t.Errorf("%q appeared more than once", cmd)
		}
		seen[cmd] = true

		idx := strings.Index(completionCommands, cmd)
		if idx < lastIdx {
			t.Errorf("%q is out of order relative to completionCommands", cmd)
		}
		lastIdx = idx
	}
}

func TestBashFlagCasesCoversEveryCommandWithFlags(t *testing.T) {
	out := bashFlagCases()
	for cmd, flags := range completionFlags {
		want := cmd + ") flags=\"" + flags + "\""
		if !strings.Contains(out, want) {
			t.Errorf("bash flag cases missing entry for %q: got %q", cmd, out)
		}
	}
}

func TestZshFlagCasesCoversEveryCommandWithFlags(t *testing.T) {
	out := zshFlagCases()
	for cmd, flags := range completionFlags {
		want := cmd + ") flags=(" + flags + ")"
		if !strings.Contains(out, want) {
			t.Errorf("zsh flag cases missing entry for %q: got %q", cmd, out)
		}
	}
}
