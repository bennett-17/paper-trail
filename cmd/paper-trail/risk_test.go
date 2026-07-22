package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func TestTruncateIndicatorsKeepsTopNAndReportsHiddenCount(t *testing.T) {
	score := risk.Score{
		Total: 6,
		Indicators: []risk.Indicator{
			{Code: "disqualified_director", Weight: 6},
			{Code: "shared_person", Weight: 3},
			{Code: "shared_address", Weight: 2},
			{Code: "formation_cluster", Weight: 1},
		},
	}
	got, hidden := truncateIndicators(score, 2)
	if hidden != 2 {
		t.Errorf("hidden = %d, want 2", hidden)
	}
	if len(got.Indicators) != 2 {
		t.Fatalf("got %d indicators, want 2", len(got.Indicators))
	}
	if got.Indicators[0].Code != "disqualified_director" || got.Indicators[1].Code != "shared_person" {
		t.Errorf("kept indicators = %+v, want the 2 highest-weight ones", got.Indicators)
	}
	if got.Total != 6 {
		t.Errorf("Total = %d, want 6 (unchanged -- --top only limits what's shown)", got.Total)
	}
}

func TestTruncateIndicatorsZeroOrNegativeMeansShowAll(t *testing.T) {
	score := risk.Score{Indicators: []risk.Indicator{{Code: "a"}, {Code: "b"}, {Code: "c"}}}
	for _, top := range []int{0, -1} {
		got, hidden := truncateIndicators(score, top)
		if hidden != 0 {
			t.Errorf("top=%d: hidden = %d, want 0", top, hidden)
		}
		if len(got.Indicators) != 3 {
			t.Errorf("top=%d: got %d indicators, want all 3", top, len(got.Indicators))
		}
	}
}

func TestTruncateIndicatorsTopGreaterThanCountIsNoOp(t *testing.T) {
	score := risk.Score{Indicators: []risk.Indicator{{Code: "a"}, {Code: "b"}}}
	got, hidden := truncateIndicators(score, 10)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0 (nothing to hide)", hidden)
	}
	if len(got.Indicators) != 2 {
		t.Errorf("got %d indicators, want both", len(got.Indicators))
	}
}

func TestFilterIndicatorsNoOpWhenNoFilterSet(t *testing.T) {
	score := risk.Score{Indicators: []risk.Indicator{{Code: "a", Weight: 1}, {Code: "b", Weight: 5}}}
	got, hidden := filterIndicators(score, 0, nil)
	if hidden != 0 || len(got.Indicators) != 2 {
		t.Errorf("got %d indicators, %d hidden, want no-op (2 indicators, 0 hidden)", len(got.Indicators), hidden)
	}
}

func TestFilterIndicatorsByMinWeight(t *testing.T) {
	score := risk.Score{
		Total: 9,
		Indicators: []risk.Indicator{
			{Code: "disqualified_director", Weight: 6},
			{Code: "shared_person", Weight: 3},
			{Code: "formation_cluster", Weight: 1},
		},
	}
	got, hidden := filterIndicators(score, 3, nil)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1 (only formation_cluster is below weight 3)", hidden)
	}
	if len(got.Indicators) != 2 {
		t.Fatalf("got %d indicators, want 2", len(got.Indicators))
	}
	if got.Total != 9 {
		t.Errorf("Total = %d, want 9 (unchanged -- filtering only limits what's shown)", got.Total)
	}
}

func TestFilterIndicatorsByCode(t *testing.T) {
	score := risk.Score{
		Indicators: []risk.Indicator{
			{Code: "sanctions_match", Weight: 5},
			{Code: "shared_address", Weight: 1},
			{Code: "sanctions_match", Weight: 5},
		},
	}
	got, hidden := filterIndicators(score, 0, []string{"sanctions_match"})
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1 (the one shared_address indicator)", hidden)
	}
	if len(got.Indicators) != 2 {
		t.Fatalf("got %d indicators, want 2", len(got.Indicators))
	}
	for _, ind := range got.Indicators {
		if ind.Code != "sanctions_match" {
			t.Errorf("kept indicator with code %q, want only sanctions_match", ind.Code)
		}
	}
}

func TestFilterIndicatorsCombinesMinWeightAndCodeAsAnd(t *testing.T) {
	score := risk.Score{
		Indicators: []risk.Indicator{
			{Code: "sanctions_match", Weight: 5}, // matches both
			{Code: "sanctions_match", Weight: 1}, // wrong weight
			{Code: "shared_address", Weight: 5},  // wrong code
		},
	}
	got, hidden := filterIndicators(score, 3, []string{"sanctions_match"})
	if hidden != 2 {
		t.Errorf("hidden = %d, want 2", hidden)
	}
	if len(got.Indicators) != 1 || got.Indicators[0].Weight != 5 {
		t.Fatalf("got %+v, want exactly the weight-5 sanctions_match indicator", got.Indicators)
	}
}

func TestParseIndicatorCodesTrimsAndDropsEmpty(t *testing.T) {
	got := parseIndicatorCodes(" sanctions_match ,, disqualified_director ,")
	want := []string{"sanctions_match", "disqualified_director"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestParseIndicatorCodesEmptyStringReturnsNil(t *testing.T) {
	if got := parseIndicatorCodes(""); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestIndicatorIdentityDistinguishesDifferentEvidenceOnSameCode(t *testing.T) {
	a := risk.Indicator{Code: "shared_address", Entities: []string{"edgar: Example Corp (1)", "ukcharity: Example Trust (2)"}, Evidence: "123 Main St"}
	b := risk.Indicator{Code: "shared_address", Entities: []string{"edgar: Example Corp (1)", "ukcharity: Example Trust (2)"}, Evidence: "456 Other Ave"}
	if indicatorIdentity(a) == indicatorIdentity(b) {
		t.Error("two shared_address indicators with different evidence should have different identities")
	}
}

func TestIndicatorIdentityIsStableAcrossEqualIndicators(t *testing.T) {
	a := risk.Indicator{Code: "shared_person", Entities: []string{"a", "b"}, Evidence: "Jane Example"}
	b := risk.Indicator{Code: "shared_person", Entities: []string{"a", "b"}, Evidence: "Jane Example"}
	if indicatorIdentity(a) != indicatorIdentity(b) {
		t.Error("identical indicators should have the same identity")
	}
}

func TestDiffRiskReportsFindsNewEntitiesAndIndicators(t *testing.T) {
	previous := riskReportJSON{
		Entities: []risk.Entity{
			risk.NewEntity("edgar", "1", "Example Corp", nil, nil),
		},
		Score: risk.Score{
			Total: 2,
			Indicators: []risk.Indicator{
				{Code: "shared_address", Entities: []string{"edgar: Example Corp (1)"}, Evidence: "123 Main St", Weight: 2},
			},
		},
	}

	newEntities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Example Corp", nil, nil), // unchanged, should NOT show up as new
		risk.NewEntity("edgar", "2", "New Company", nil, nil),  // genuinely new
	}
	newScore := risk.Score{
		Total: 5,
		Indicators: []risk.Indicator{
			{Code: "shared_address", Entities: []string{"edgar: Example Corp (1)"}, Evidence: "123 Main St", Weight: 2},                           // same as before
			{Code: "shared_person", Entities: []string{"edgar: Example Corp (1)", "edgar: New Company (2)"}, Evidence: "Jane Example", Weight: 3}, // new
		},
	}

	diff := diffRiskReports(previous, newEntities, newScore)

	if len(diff.NewEntities) != 1 || diff.NewEntities[0].ID != "2" {
		t.Fatalf("NewEntities = %+v, want exactly the new-company entity", diff.NewEntities)
	}
	if len(diff.NewIndicators) != 1 || diff.NewIndicators[0].Code != "shared_person" {
		t.Fatalf("NewIndicators = %+v, want exactly the new shared_person indicator", diff.NewIndicators)
	}
	if diff.ScoreBefore != 2 || diff.ScoreAfter != 5 {
		t.Errorf("ScoreBefore/After = %d/%d, want 2/5", diff.ScoreBefore, diff.ScoreAfter)
	}
}

func TestDiffRiskReportsNoChangesIsEmpty(t *testing.T) {
	entities := []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)}
	score := risk.Score{
		Total:      2,
		Indicators: []risk.Indicator{{Code: "shared_address", Entities: []string{"edgar: Example Corp (1)"}, Evidence: "123 Main St", Weight: 2}},
	}
	previous := riskReportJSON{Entities: entities, Score: score}

	diff := diffRiskReports(previous, entities, score)
	if len(diff.NewEntities) != 0 {
		t.Errorf("NewEntities = %v, want none for an identical re-run", diff.NewEntities)
	}
	if len(diff.NewIndicators) != 0 {
		t.Errorf("NewIndicators = %v, want none for an identical re-run", diff.NewIndicators)
	}
	if diff.ScoreBefore != diff.ScoreAfter {
		t.Errorf("ScoreBefore/After = %d/%d, want equal for an identical re-run", diff.ScoreBefore, diff.ScoreAfter)
	}
}

func TestProgressReporterWritesToItsWriter(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressReporter(&buf)
	p.report("SEC EDGAR", "term %d/%d: %q", 1, 5, "Example Corp")

	out := buf.String()
	if !strings.Contains(out, "SEC EDGAR: term 1/5: \"Example Corp\"") {
		t.Errorf("output %q doesn't contain the expected message", out)
	}
	if !strings.HasPrefix(out, "[+") {
		t.Errorf("output %q doesn't start with the elapsed-time prefix", out)
	}
}

// TestNilProgressReporterIsANoOp guards the whole point of using a
// pointer receiver with a nil check: every gather/screen function
// calls progress.report(...) unconditionally, relying on a nil
// *progressReporter (returned when --quiet is set) to silently do
// nothing rather than panic.
func TestNilProgressReporterIsANoOp(t *testing.T) {
	var p *progressReporter
	p.report("source", "message") // must not panic
}
