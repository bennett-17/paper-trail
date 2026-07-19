package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSetsUnsetVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\n\nEDGAR_USER_AGENT=\"Test User test@example.com\"\nQUOTED='hello world'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	os.Unsetenv("EDGAR_USER_AGENT")
	os.Unsetenv("QUOTED")
	t.Cleanup(func() {
		os.Unsetenv("EDGAR_USER_AGENT")
		os.Unsetenv("QUOTED")
	})

	if err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := os.Getenv("EDGAR_USER_AGENT"); got != "Test User test@example.com" {
		t.Errorf("EDGAR_USER_AGENT = %q, want unquoted value", got)
	}
	if got := os.Getenv("QUOTED"); got != "hello world" {
		t.Errorf("QUOTED = %q, want %q", got, "hello world")
	}
}

func TestLoadDoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("EDGAR_USER_AGENT=from-file@example.com\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	os.Setenv("EDGAR_USER_AGENT", "already-set@example.com")
	t.Cleanup(func() { os.Unsetenv("EDGAR_USER_AGENT") })

	if err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := os.Getenv("EDGAR_USER_AGENT"); got != "already-set@example.com" {
		t.Errorf("EDGAR_USER_AGENT = %q, want existing value preserved", got)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("Load of missing file: %v, want nil", err)
	}
}
