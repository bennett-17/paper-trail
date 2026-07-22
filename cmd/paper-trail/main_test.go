package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("limit", "", "")
	fs.Bool("json", false, "")
	return fs
}

func TestSplitPositionalSeparatesFlagsFromPositionalArgs(t *testing.T) {
	fs := newTestFlagSet()
	flagArgs, positional := splitPositional(fs, []string{"Example Corp", "--limit", "5", "--json"})
	if !reflect.DeepEqual(positional, []string{"Example Corp"}) {
		t.Errorf("positional = %v, want [Example Corp]", positional)
	}
	if !reflect.DeepEqual(flagArgs, []string{"--limit", "5", "--json"}) {
		t.Errorf("flagArgs = %v, want [--limit 5 --json]", flagArgs)
	}
}

func TestSplitPositionalBoolFlagDoesNotConsumeNextArg(t *testing.T) {
	fs := newTestFlagSet()
	flagArgs, positional := splitPositional(fs, []string{"--json", "Example Corp"})
	if !reflect.DeepEqual(positional, []string{"Example Corp"}) {
		t.Errorf("positional = %v, want [Example Corp] -- --json is a bool flag and shouldn't eat the next arg", positional)
	}
	if !reflect.DeepEqual(flagArgs, []string{"--json"}) {
		t.Errorf("flagArgs = %v, want [--json]", flagArgs)
	}
}

func TestSplitPositionalHandlesEmbeddedEquals(t *testing.T) {
	fs := newTestFlagSet()
	flagArgs, positional := splitPositional(fs, []string{"--limit=5", "Example Corp"})
	if !reflect.DeepEqual(positional, []string{"Example Corp"}) {
		t.Errorf("positional = %v, want [Example Corp]", positional)
	}
	if !reflect.DeepEqual(flagArgs, []string{"--limit=5"}) {
		t.Errorf("flagArgs = %v, want [--limit=5] (embedded value, no separate consumed arg)", flagArgs)
	}
}

func TestSplitPositionalDoubleDashStopsFlagParsing(t *testing.T) {
	fs := newTestFlagSet()
	flagArgs, positional := splitPositional(fs, []string{"--json", "--", "--not-a-flag", "-x"})
	if !reflect.DeepEqual(positional, []string{"--not-a-flag", "-x"}) {
		t.Errorf("positional = %v, want everything after -- treated as positional", positional)
	}
	if !reflect.DeepEqual(flagArgs, []string{"--json"}) {
		t.Errorf("flagArgs = %v, want [--json]", flagArgs)
	}
}

func TestSplitPositionalUnknownFlagDoesNotConsumeNextArg(t *testing.T) {
	fs := newTestFlagSet()
	flagArgs, positional := splitPositional(fs, []string{"--nonexistent", "Example Corp"})
	// An unrecognized flag is left for fs.Parse to error on; splitPositional
	// itself shouldn't guess whether it takes a value.
	if !reflect.DeepEqual(positional, []string{"Example Corp"}) {
		t.Errorf("positional = %v, want [Example Corp]", positional)
	}
	if !reflect.DeepEqual(flagArgs, []string{"--nonexistent"}) {
		t.Errorf("flagArgs = %v, want [--nonexistent]", flagArgs)
	}
}

func TestReadQueryTermsFileSkipsBlankLinesAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchlist.txt")
	content := "Example Org One\n\n# a comment\n  Example Org Two  \n#another comment\nExample Org Three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	terms, err := readQueryTermsFile(path)
	if err != nil {
		t.Fatalf("readQueryTermsFile: %v", err)
	}
	want := []string{"Example Org One", "Example Org Two", "Example Org Three"}
	if !reflect.DeepEqual(terms, want) {
		t.Errorf("terms = %v, want %v", terms, want)
	}
}

func TestReadQueryTermsFileMissingFileReturnsError(t *testing.T) {
	if _, err := readQueryTermsFile(filepath.Join(t.TempDir(), "does-not-exist.txt")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// TestReadQueryTermsFileDashReadsStdin guards --input-file -, which
// lets a watchlist be piped in from another command instead of always
// needing a real file on disk.
func TestReadQueryTermsFileDashReadsStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	content := "Example Org One\n\n# a comment\nExample Org Two\n"
	go func() {
		w.WriteString(content)
		w.Close()
	}()

	terms, err := readQueryTermsFile("-")
	if err != nil {
		t.Fatalf("readQueryTermsFile(\"-\"): %v", err)
	}
	want := []string{"Example Org One", "Example Org Two"}
	if !reflect.DeepEqual(terms, want) {
		t.Errorf("terms = %v, want %v", terms, want)
	}
}

func TestOrDash(t *testing.T) {
	if got := orDash(""); got != "-" {
		t.Errorf("orDash(\"\") = %q, want -", got)
	}
	if got := orDash("value"); got != "value" {
		t.Errorf("orDash(\"value\") = %q, want value", got)
	}
}

func TestGbpOrDash(t *testing.T) {
	if got := gbpOrDash(nil); got != "-" {
		t.Errorf("gbpOrDash(nil) = %q, want -", got)
	}
	v := int64(1234)
	if got := gbpOrDash(&v); got != "£1234" {
		t.Errorf("gbpOrDash(&1234) = %q, want £1234", got)
	}
}

func TestMoneyOrDash(t *testing.T) {
	if got := moneyOrDash(nil); got != "-" {
		t.Errorf("moneyOrDash(nil) = %q, want -", got)
	}
	v := int64(1234)
	if got := moneyOrDash(&v); got != "$1234" {
		t.Errorf("moneyOrDash(&1234) = %q, want $1234", got)
	}
}

// TestSameCompanyNumberIgnoresLeadingZeroPadding guards a real bug
// found live: the UK Charity Commission's CompaniesHouseNumber field
// returns numbers unpadded while Companies House's own officer
// appointments API always zero-pads to 8 characters, so a naive
// string comparison would miss a match between the two.
func TestSameCompanyNumberIgnoresLeadingZeroPadding(t *testing.T) {
	if !sameCompanyNumber("4325234", "04325234") {
		t.Error("expected an unpadded and a zero-padded form of the same number to compare equal")
	}
	if sameCompanyNumber("4325234", "04325235") {
		t.Error("expected genuinely different numbers to compare unequal")
	}
}

func TestVersionStringNoBuildInfoFallsBackPlainly(t *testing.T) {
	got := versionString(nil, false)
	if !strings.Contains(got, "paper-trail") || !strings.Contains(got, "unavailable") {
		t.Errorf("got %q, want a plain fallback message", got)
	}
}

// TestVersionStringDevelFallsBackToDev reproduces `go run`'s own
// build info shape, confirmed live: Main.Version comes back
// "(devel)" (not empty) for an ephemeral go-run build, which should
// still read as "dev", not the literal "(devel)".
func TestVersionStringDevelFallsBackToDev(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "(devel)"},
	}
	got := versionString(info, true)
	if !strings.Contains(got, "paper-trail dev") {
		t.Errorf("got %q, want it to report \"dev\", not the literal \"(devel)\"", got)
	}
}

// TestVersionStringIncludesRevisionAndDirtyFlag reproduces a real go
// build's build info (confirmed live via `go version -m` against an
// actual built binary in this git checkout): a pseudo-version plus
// vcs.revision/vcs.modified settings.
func TestVersionStringIncludesRevisionAndDirtyFlag(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "v0.0.0-20260722204817-f69d8765ad69+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "f69d8765ad69c73d5545a55be0ae7e7bafcdbcf9"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	got := versionString(info, true)
	if !strings.Contains(got, "f69d8765ad69") {
		t.Errorf("got %q, want the (truncated to 12 chars) commit hash", got)
	}
	if strings.Contains(got, "f69d8765ad69c73d5545a55be0ae7e7bafcdbcf9") {
		t.Errorf("got %q, want the commit hash truncated, not the full 40 chars", got)
	}
	if !strings.Contains(got, "dirty") {
		t.Errorf("got %q, want it to flag the dirty working tree", got)
	}
	if !strings.Contains(got, "go1.26.5") {
		t.Errorf("got %q, want the Go toolchain version", got)
	}
}

func TestVersionStringCleanTreeOmitsDirtyFlag(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "v1.0.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123def456"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	got := versionString(info, true)
	if strings.Contains(got, "dirty") {
		t.Errorf("got %q, want no dirty flag for a clean working tree", got)
	}
}
