package risk

import "testing"

// These fuzz targets (go test -fuzz, stdlib native since Go 1.18) exist
// because entity names/addresses come from live third-party APIs, not
// input this program controls -- several doc comments elsewhere in
// this package already call that out (e.g. the HTML graph viewer's
// own script-tag-breakout guard). The goal here is robustness, not
// correctness: these functions should never panic on arbitrary,
// possibly-malformed UTF-8, no matter how unusual a real register's
// data turns out to be.

func FuzzFoldDiacritics(f *testing.F) {
	seeds := []string{
		"josé", "müller", "françois", "weiß", "václav", "garça", "åsa",
		"", "plain ascii", "MiXeD CaSe", "123 main st",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = foldDiacritics(s)
	})
}

func FuzzNormalizeText(f *testing.F) {
	seeds := []string{
		"123 Main St.", "Jane A. Example", "", "   ",
		"Prof. Doreen Cantrell FRS", "José García", "#200", "a,b,c...",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = normalizeText(s)
	})
}

func FuzzTokenizeName(f *testing.F) {
	seeds := []string{
		"Professor Doreen Ann Cantrell FRS",
		"CANTRELL, Doreen Ann, Professor",
		"Mr. John Smith Jr.",
		"", "   ", "José García", ",,,",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := tokenizeName(s)
		// strings.Fields (which tokenizeName ends with) never produces
		// an empty token -- guard that invariant explicitly rather
		// than trusting it silently, in case the surrounding logic
		// changes later.
		for _, tok := range got {
			if tok == "" {
				t.Errorf("tokenizeName(%q) produced an empty token: %v", s, got)
			}
		}
	})
}

func FuzzNormalizeAddressFuzzy(f *testing.F) {
	seeds := []string{
		"123 Main St, Suite 200",
		"123 Main St, Suite 450",
		"456 Other Ave #12",
		"", "   ", "Flat 3, 10 Example Road", "Suite",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = normalizeAddressFuzzy(s)
	})
}
