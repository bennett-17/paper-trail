package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

// knownDuplicateIndicatorCodes are indicator codes still constructed in
// more than one place. Each entry is a liability, not an approval: this
// exact shape has already produced two real defects.
//
// mail_drop_address existed twice and one copy was missed when the
// shared constructor was extracted, so the two silently diverged. The
// company-profile indicators existed twice, which is what let
// `calibrate` and the gatherer disagree about which indicators exist at
// all -- and that produced a base-rate table with one row that read as
// "these indicators almost never fire", an actively misleading result
// rather than merely an incomplete one.
//
// Every code below is duplicated across the Companies House gatherer
// and the UK-charity path's own Companies House lookup (or, for
// jurisdiction_risk, across the US and UK sanctions screens). The list
// may only ever shrink. Adding to it means shipping a known drift risk.
var knownDuplicateIndicatorCodes = map[string]bool{
	"insolvency_history":                true,
	"jurisdiction_risk":                 true,
	"multi_jurisdiction_ownership":      true,
	"ownership_loop":                    true,
	"person_jurisdiction_risk":          true,
	"sanctions_adjacent_officer_change": true,
}

// indicatorCodeSites parses this package plus internal/risk and returns
// how many distinct places construct each indicator code, by looking
// for a composite literal with a Code: "..." field. Parsing rather than
// grepping so a code mentioned in a comment or a description string
// cannot be mistaken for a construction.
func indicatorCodeSites(t *testing.T) map[string]int {
	t.Helper()
	counts := map[string]int{}
	dirs := []string{".", filepath.Join("..", "..", "internal", "risk")}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", name, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "Code" {
						continue
					}
					val, ok := kv.Value.(*ast.BasicLit)
					if !ok || val.Kind != token.STRING {
						continue
					}
					code, err := strconv.Unquote(val.Value)
					if err == nil && code != "" {
						counts[code]++
					}
				}
				return true
			})
		}
	}
	if len(counts) == 0 {
		t.Fatal("found no indicator constructions at all -- has the Indicator literal shape changed? This test needs updating alongside it")
	}
	return counts
}

// TestNoNewDuplicateIndicatorConstructions fails when an indicator code
// is constructed in more than one place and isn't already a known
// liability. Duplicated construction is how two copies of the same
// indicator drift apart -- different thresholds, different wording, or
// one copy updated and the other forgotten -- and it has caused real
// bugs here twice. The fix is a shared constructor, the pattern
// companyProfileIndicators, mailDropIndicator and
// officerAppointmentIndicators already follow.
func TestNoNewDuplicateIndicatorConstructions(t *testing.T) {
	var offenders []string
	for code, n := range indicatorCodeSites(t) {
		if n > 1 && !knownDuplicateIndicatorCodes[code] {
			offenders = append(offenders, code)
		}
	}
	sort.Strings(offenders)
	for _, code := range offenders {
		t.Errorf("indicator %q is constructed in more than one place -- extract a shared constructor rather than copying it, or the two copies will drift", code)
	}
}

// TestKnownDuplicatesAreStillDuplicated keeps the list above honest: an
// entry that has since been extracted must be REMOVED, so the list
// always reflects real outstanding work rather than quietly granting
// permission to duplicate a code that is now fine.
func TestKnownDuplicatesAreStillDuplicated(t *testing.T) {
	counts := indicatorCodeSites(t)
	for code := range knownDuplicateIndicatorCodes {
		if counts[code] <= 1 {
			t.Errorf("indicator %q is no longer duplicated (%d site(s)) -- remove it from knownDuplicateIndicatorCodes", code, counts[code])
		}
	}
}

// TestSetLevelIndicatorsAreRealCodes keeps risk.IsSetLevel's registry
// honest against the indicators that actually exist.
//
// The original plan for this guard was to flag any indicator built from
// an unbounded Entities slice unless it was registered set-level. That
// would fire on nearly every pairwise indicator in the project --
// shared_person and shared_address build their entity lists from
// variables too -- so it would have been noise, and a noisy guard gets
// muted. What can actually go wrong here is narrower: the registry
// naming a code that no longer exists (a rename or deletion leaves it
// silently doing nothing) or being emptied. Both are checkable.
//
// The failure this protects against is expensive and quiet. An
// unregistered set-level indicator is expanded to n(n-1)/2 graph edges
// and corroborates every pair inside itself; at n=329 that was 53,956
// fabricated edges and 31,474 fabricated corroborated pairs, none of
// which looks like an error in the output -- it looks like a finding.
func TestSetLevelIndicatorsAreRealCodes(t *testing.T) {
	constructed := indicatorCodeSites(t)

	// entity_cluster is the reason this mechanism exists; losing its
	// registration silently restores the original defect.
	if !risk.IsSetLevel("entity_cluster") {
		t.Error("entity_cluster is no longer registered as set-level -- it is derived from the other indicators by union-find, so expanding it pairwise fabricates edges and corroboration")
	}

	// Every registered code must still be constructed somewhere, or the
	// registration is dead weight pointing at a code that moved on.
	for _, code := range knownSetLevelCodes {
		if !risk.IsSetLevel(code) {
			t.Errorf("%q is expected to be registered set-level but IsSetLevel says otherwise", code)
		}
		if constructed[code] == 0 {
			t.Errorf("%q is registered set-level but no longer constructed anywhere -- rename or stale entry", code)
		}
	}

	// A pairwise indicator must never be registered: doing so would
	// silently delete real evidence from the graph rather than add
	// noise to it, which is the harder direction to notice.
	for _, code := range []string{"shared_person", "shared_address", "formation_cluster"} {
		if risk.IsSetLevel(code) {
			t.Errorf("%q is registered set-level, but its pairs are genuine evidence -- registering it drops real edges", code)
		}
	}
}

// knownSetLevelCodes mirrors risk.setLevelIndicators. Listed here
// rather than exported from the risk package so that adding a code
// there is a deliberate two-place change, the same shape
// knownDuplicateIndicatorCodes above uses.
var knownSetLevelCodes = []string{"entity_cluster"}
