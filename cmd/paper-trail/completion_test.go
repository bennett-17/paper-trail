package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// mainDispatchSubcommands reads main.go's own dispatch switch and
// returns every literal case value it handles. Parsing the source
// rather than hand-listing them is the point: this is the ONE source
// of truth for what subcommands actually exist, and the whole reason
// this test exists (see TestCompletionCommandsCoversEveryDispatched
// below) is that the two lists silently drifted apart once already --
// a subcommand was added to main() and shipped for several releases
// with no shell completion, which no self-consistency check between
// completionCommands and completionFlags could ever have caught.
func mainDispatchSubcommands(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	var cases []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			return true
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				cases = append(cases, strings.Trim(lit.Value, `"`))
			}
			return true
		})
		return false
	})
	if len(cases) == 0 {
		t.Fatal("found no dispatch cases in main() -- has the switch been restructured? This test needs updating alongside it")
	}
	return cases
}

func TestCompletionCommandsCoversEveryDispatchedSubcommand(t *testing.T) {
	// Flag-style aliases (-v, --version, -h, --help) are dispatched but
	// aren't subcommands a user would tab-complete as a bare word --
	// completionCommands carries their long forms ("version", "help")
	// instead, which is what these scripts should offer.
	skip := map[string]bool{"-v": true, "--version": true, "-h": true, "--help": true}

	listed := map[string]bool{}
	for _, c := range strings.Fields(completionCommands) {
		listed[c] = true
	}

	for _, cmd := range mainDispatchSubcommands(t) {
		if skip[cmd] || listed[cmd] {
			continue
		}
		t.Errorf("main() dispatches %q but completionCommands doesn't list it -- shell completion would silently omit it (add it to completionCommands AND completionFlags)", cmd)
	}
}

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
