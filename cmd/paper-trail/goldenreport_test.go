package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the golden report file from current output")

// generatedAtRE matches the report's own timestamp line, which changes
// every run and would otherwise make the golden file fail constantly.
var generatedAtRE = regexp.MustCompile(`Generated [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} [A-Z]{2,5}`)

// goldenReportFixture is deliberately broad: it exercises every section
// of the report at once (tabs, screen coverage, entity cards across
// severity tiers, reviewed marks, cached flags, duplicates, timeline,
// people, corroborations, the graph iframe and a diff) so that a change
// to any of them shows up here.
func goldenReportFixture() (riskReportJSON, *riskReportDiff) {
	a := risk.NewEntity("companieshouse", "1", "Alpha Ltd", []string{"1 Main Street, London, EC1A 1AA"}, []string{"Jane Doe"})
	a.FormedOn, a.DissolvedOn = "2020-01-10", "2021-03-01"
	a.PersonDetails = []risk.Person{{Name: "Jane Doe", BirthMonth: 4, BirthYear: 1970, Address: "1 Main Street, London, EC1A 1AA"}}
	a.VerifiedAt = "2026-08-01T12:00:00Z"

	b := risk.NewEntity("edgar", "2", "Alpha Corp", []string{"1 Main Street, London, EC1A 1AA"}, []string{"Jane Doe"})

	report := riskReportJSON{
		Queries:  []string{"Alpha"},
		Entities: []risk.Entity{a, b},
		Notes:    []string{"GLEIF: no match for \"Alpha\""},
		Score: risk.Score{
			Total: 11, Confidence: "MEDIUM", ConfidenceReason: "shared_person indicator at weight 3",
			Indicators: []risk.Indicator{
				{Code: "sanctions_match", Description: "Name matched a US restricted-party list", Weight: 5,
					Entities: []string{"companieshouse: Alpha Ltd (1)"}, Evidence: "ALPHA LTD -- OFAC SDN"},
				{Code: "shared_person", Description: "Same officer at both", Weight: 3,
					Entities: []string{"companieshouse: Alpha Ltd (1)", "edgar: Alpha Corp (2)"}, Evidence: "Jane Doe"},
				{Code: "shared_address", Description: "Same registered address", Weight: 2,
					Entities: []string{"companieshouse: Alpha Ltd (1)", "edgar: Alpha Corp (2)"}, Evidence: "1 Main Street"},
				{Code: "formation_cluster", Description: "Formed together", Weight: 1,
					Entities: []string{"companieshouse: Alpha Ltd (1)"}, Evidence: "span", Date: "2020-01-10", Reviewed: true},
			},
			Corroborations: []risk.Corroboration{{
				Entities: []string{"companieshouse: Alpha Ltd (1)", "edgar: Alpha Corp (2)"},
				Codes:    []string{"shared_address", "shared_person"},
			}},
		},
		ReviewedIndicators: 1,
		ScreenCoverage:     []screenCoverage{{Source: "UK sanctions screen", Screened: 4, Matched: 0}},
		SourceHealth:       sourceHealth{Skipped: []string{"NZBN"}},
	}
	diff := &riskReportDiff{ScoreBefore: 8, ScoreAfter: 11}
	return report, diff
}

// TestReportHTMLMatchesGolden pins the whole rendered report. The report
// is the most-changed surface in this project -- it has gained tabs, a
// graph iframe, screen coverage, reviewed marks and cached flags, all
// without anything catching unintended drift in the parts nobody was
// looking at. Individual feature tests assert their own section; this
// asserts everything else stayed put.
//
// Regenerate deliberately with:
//
//	go test ./cmd/paper-trail -run TestReportHTMLMatchesGolden -update-golden
//
// and read the diff before committing it -- an unexplained change here
// is the signal, not an inconvenience.
func TestReportHTMLMatchesGolden(t *testing.T) {
	report, diff := goldenReportFixture()
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeReportHTML(report, diff, "baseline.json", path); err != nil {
		t.Fatalf("writeReportHTML: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	// The generated-at line is inherently unstable; everything else must be exact.
	normalized := generatedAtRE.ReplaceAll(got, []byte("Generated <TIMESTAMP>"))

	goldenPath := filepath.Join("testdata", "report.golden.html")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, normalized, 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("golden file rewritten: %s (%d bytes)", goldenPath, len(normalized))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file (run with -update-golden to create it): %v", err)
	}
	if string(normalized) != string(want) {
		t.Errorf("rendered report differs from testdata/report.golden.html (%d bytes vs %d).\n"+
			"If the change is intended, re-run with -update-golden and review the diff before committing.",
			len(normalized), len(want))
	}
}
