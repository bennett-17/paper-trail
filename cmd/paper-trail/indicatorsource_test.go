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
