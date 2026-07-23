package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestFilterCorroborationsNoOpWhenZeroOrNegative(t *testing.T) {
	score := risk.Score{Corroborations: []risk.Corroboration{{Codes: []string{"a"}}, {Codes: []string{"a", "b"}}}}
	for _, min := range []int{0, -1} {
		got, hidden := filterCorroborations(score, min)
		if hidden != 0 || len(got.Corroborations) != 2 {
			t.Errorf("min=%d: got %d corroborations, %d hidden, want no-op (2, 0)", min, len(got.Corroborations), hidden)
		}
	}
}

func TestFilterCorroborationsKeepsOnlyThoseMeetingThreshold(t *testing.T) {
	score := risk.Score{
		Total: 9, // unaffected -- Corroborations never contributed to Total
		Corroborations: []risk.Corroboration{
			{Entities: []string{"a", "b"}, Codes: []string{"shared_address", "shared_person"}},                 // 2 codes
			{Entities: []string{"c", "d"}, Codes: []string{"shared_address", "shared_person", "shared_email"}}, // 3 codes
			{Entities: []string{"e", "f"}, Codes: []string{"shared_address"}},                                  // 1 code -- shouldn't happen in practice, but exercises the boundary
		},
	}
	got, hidden := filterCorroborations(score, 2)
	if hidden != 1 {
		t.Fatalf("hidden = %d, want 1 (only the 1-code entry)", hidden)
	}
	if len(got.Corroborations) != 2 {
		t.Fatalf("got %d corroborations, want 2", len(got.Corroborations))
	}
	if got.Total != 9 {
		t.Errorf("Total = %d, want 9 (unchanged -- filtering only limits what's shown)", got.Total)
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

func TestExcludeIndicatorsNoOpWhenNoTerms(t *testing.T) {
	score := risk.Score{
		Total:      5,
		Indicators: []risk.Indicator{{Code: "a", Weight: 5, Evidence: "123 Main St"}},
		Confidence: "MEDIUM",
	}
	got, excluded := excludeIndicators(score, nil)
	if excluded != 0 {
		t.Errorf("excluded = %d, want 0", excluded)
	}
	if got.Total != 5 || got.Confidence != "MEDIUM" {
		t.Errorf("got = %+v, want unchanged", got)
	}
}

func TestExcludeIndicatorsMatchesEvidenceCaseInsensitively(t *testing.T) {
	score := risk.Score{
		Total: 3,
		Indicators: []risk.Indicator{
			{Code: "shared_address", Weight: 2, Evidence: "123 MAIN ST, Suite 200", Entities: []string{"edgar: Example Corp (1)"}},
			{Code: "formation_cluster", Weight: 1, Evidence: "formed within 3 days", Entities: []string{"edgar: Other Corp (2)"}},
		},
	}
	got, excluded := excludeIndicators(score, []string{"main st"})
	if excluded != 1 {
		t.Fatalf("excluded = %d, want 1", excluded)
	}
	if len(got.Indicators) != 1 || got.Indicators[0].Code != "formation_cluster" {
		t.Fatalf("got.Indicators = %+v, want only formation_cluster left", got.Indicators)
	}
	if got.Total != 1 {
		t.Errorf("Total = %d, want 1 (recomputed from what's left, not the original 3)", got.Total)
	}
}

func TestExcludeIndicatorsMatchesEntityLabels(t *testing.T) {
	score := risk.Score{
		Total: 3,
		Indicators: []risk.Indicator{
			{Code: "shared_person", Weight: 3, Evidence: "Jane Example", Entities: []string{"edgar: Cleared Corp (1)", "edgar: Other Corp (2)"}},
		},
	}
	got, excluded := excludeIndicators(score, []string{"Cleared Corp"})
	if excluded != 1 {
		t.Fatalf("excluded = %d, want 1", excluded)
	}
	if len(got.Indicators) != 0 || got.Total != 0 {
		t.Errorf("got = %+v, want everything removed and Total 0", got)
	}
}

// TestExcludeIndicatorsRecomputesConfidenceAndCorroborations guards the
// difference between --exclude and --top/--min-weight/--indicator:
// this needs to recompute Confidence (and Corroborations) from what's
// left, not leave them reflecting an indicator that's been dismissed
// as not a real finding.
func TestExcludeIndicatorsRecomputesConfidenceAndCorroborations(t *testing.T) {
	high := risk.Indicator{Code: "disqualified_director", Weight: 6, Evidence: "dismissed lead", Entities: []string{"a", "b"}}
	other := risk.Indicator{Code: "shared_address", Weight: 1, Evidence: "123 Main St", Entities: []string{"a", "b"}}
	score := risk.Assess(nil, []risk.Indicator{high, other})
	if score.Confidence != "HIGH" {
		t.Fatalf("precondition failed: Confidence = %q, want HIGH (weight-6 indicator present)", score.Confidence)
	}

	got, excluded := excludeIndicators(score, []string{"dismissed lead"})
	if excluded != 1 {
		t.Fatalf("excluded = %d, want 1", excluded)
	}
	if got.Confidence == "HIGH" {
		t.Errorf("Confidence = %q, want recomputed down from HIGH now that the weight-6 indicator is excluded", got.Confidence)
	}
	if got.Total != 1 {
		t.Errorf("Total = %d, want 1 (only the remaining shared_address indicator)", got.Total)
	}
	if strings.Contains(got.ConfidenceReason, "disqualified_director") {
		t.Errorf("ConfidenceReason = %q, want it to no longer cite the excluded indicator", got.ConfidenceReason)
	}
}

func TestParseExcludeTermsCombinesFlagAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclude.txt")
	content := "Cleared Corp\n\n# a comment\nAnother Cleared Entity\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	terms, err := parseExcludeTerms("flag term", path)
	if err != nil {
		t.Fatalf("parseExcludeTerms: %v", err)
	}
	want := []string{"flag term", "Cleared Corp", "Another Cleared Entity"}
	if len(terms) != len(want) {
		t.Fatalf("got %v, want %v", terms, want)
	}
	for i := range want {
		if terms[i] != want[i] {
			t.Errorf("got %v, want %v", terms, want)
		}
	}
}

func TestParseExcludeTermsMissingFileReturnsError(t *testing.T) {
	if _, err := parseExcludeTerms("", filepath.Join(t.TempDir(), "does-not-exist.txt")); err == nil {
		t.Fatal("expected an error for a missing --exclude-file")
	}
}

func TestValidateFailOnAcceptsKnownBandsCaseInsensitively(t *testing.T) {
	for _, v := range []string{"LOW", "low", "Medium", "HIGH", ""} {
		if err := validateFailOn(v); err != nil {
			t.Errorf("validateFailOn(%q) = %v, want nil", v, err)
		}
	}
}

func TestValidateFailOnRejectsUnknownValue(t *testing.T) {
	if err := validateFailOn("SEVERE"); err == nil {
		t.Error("expected an error for an unrecognized --fail-on value")
	}
}

func TestValidateWatchFlagsAllowsUnwatchedDefaults(t *testing.T) {
	if err := validateWatchFlags(0, "", ""); err != nil {
		t.Errorf("validateWatchFlags(0, \"\", \"\") = %v, want nil", err)
	}
	if err := validateWatchFlags(0, "HIGH", ""); err != nil {
		t.Errorf("--fail-on alone (no --watch) should still be allowed: %v", err)
	}
	if err := validateWatchFlags(0, "HIGH", "https://example.com/hook"); err != nil {
		t.Errorf("--webhook with --fail-on (no --watch) should still be allowed: %v", err)
	}
}

func TestValidateWatchFlagsRejectsFailOnTogetherWithWatch(t *testing.T) {
	if err := validateWatchFlags(time.Hour, "HIGH", ""); err == nil {
		t.Error("expected an error combining --watch with --fail-on")
	}
}

func TestValidateWatchFlagsRejectsIntervalBelowMinimum(t *testing.T) {
	if err := validateWatchFlags(30*time.Second, "", ""); err == nil {
		t.Error("expected an error for a --watch interval under the 1m floor")
	}
	if err := validateWatchFlags(watchMinInterval, "", ""); err != nil {
		t.Errorf("the minimum interval itself should be accepted: %v", err)
	}
}

func TestValidateWatchFlagsAllowsWebhookWithWatchAloneNoFailOn(t *testing.T) {
	// --watch's own alert trigger doesn't need --fail-on at all.
	if err := validateWatchFlags(time.Hour, "", "https://example.com/hook"); err != nil {
		t.Errorf("validateWatchFlags(1h, \"\", webhook) = %v, want nil", err)
	}
}

func TestValidateWatchFlagsRejectsWebhookWithNeitherFailOnNorWatch(t *testing.T) {
	if err := validateWatchFlags(0, "", "https://example.com/hook"); err == nil {
		t.Error("expected an error for --webhook with neither --fail-on nor --watch set")
	}
}

func TestShouldFailOnEmptyThresholdNeverFails(t *testing.T) {
	for _, c := range []string{"LOW", "MEDIUM", "HIGH"} {
		if shouldFailOn(c, "") {
			t.Errorf("shouldFailOn(%q, \"\") = true, want false (no threshold set)", c)
		}
	}
}

func TestShouldFailOnComparesRankNotExactMatch(t *testing.T) {
	cases := []struct {
		confidence, threshold string
		want                  bool
	}{
		{"HIGH", "MEDIUM", true},  // exceeds threshold
		{"HIGH", "HIGH", true},    // meets threshold exactly
		{"MEDIUM", "HIGH", false}, // below threshold
		{"LOW", "LOW", true},      // meets lowest threshold exactly
		{"medium", "LOW", true},   // case-insensitive
	}
	for _, c := range cases {
		got := shouldFailOn(c.confidence, c.threshold)
		if got != c.want {
			t.Errorf("shouldFailOn(%q, %q) = %v, want %v", c.confidence, c.threshold, got, c.want)
		}
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

func TestWriteSummaryTextMode(t *testing.T) {
	report := riskReportJSON{
		Queries:  []string{"Example Corp"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)},
		Score: risk.Score{
			Total:            8,
			Confidence:       "HIGH",
			ConfidenceReason: "disqualified_director indicator at weight 6",
			Indicators:       []risk.Indicator{{Code: "a"}, {Code: "b"}, {Code: "c"}},
		},
	}
	var buf bytes.Buffer
	writeSummary(&buf, report, nil, false, false)
	out := buf.String()
	if !strings.Contains(out, "Score: 8 (HIGH -- disqualified_director indicator at weight 6)") {
		t.Errorf("output %q doesn't contain the score/confidence/reason", out)
	}
	if !strings.Contains(out, "3 indicator(s)") || !strings.Contains(out, "1 entit(ies)") {
		t.Errorf("output %q doesn't contain the indicator/entity counts", out)
	}
}

func TestWriteSummaryTextModeIncludesHiddenExcludedAndDiff(t *testing.T) {
	report := riskReportJSON{
		Score:              risk.Score{Total: 3, Confidence: "LOW"},
		HiddenIndicators:   2,
		ExcludedIndicators: 1,
	}
	diff := &riskReportDiff{ScoreBefore: 1, ScoreAfter: 3, NewIndicators: []risk.Indicator{{Code: "a"}}}
	var buf bytes.Buffer
	writeSummary(&buf, report, diff, false, false)
	out := buf.String()
	if !strings.Contains(out, "2 hidden") {
		t.Errorf("output %q doesn't mention hidden count", out)
	}
	if !strings.Contains(out, "1 excluded") {
		t.Errorf("output %q doesn't mention excluded count", out)
	}
	if !strings.Contains(out, "vs baseline: 1->3, 1 new indicator(s)") {
		t.Errorf("output %q doesn't mention the diff", out)
	}
}

func TestWriteSummaryJSONMode(t *testing.T) {
	report := riskReportJSON{
		Queries:  []string{"Example Corp"},
		Entities: []risk.Entity{risk.NewEntity("edgar", "1", "Example Corp", nil, nil)},
		Score:    risk.Score{Total: 8, Confidence: "HIGH", ConfidenceReason: "sanctions_match indicator at weight 5", Indicators: []risk.Indicator{{Code: "a"}}},
	}
	var buf bytes.Buffer
	writeSummary(&buf, report, nil, true, false)

	var got riskSummaryJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshaling output: %v", err)
	}
	if got.Total != 8 || got.Confidence != "HIGH" || got.EntityCount != 1 || got.IndicatorCount != 1 {
		t.Errorf("got %+v, want Total=8 Confidence=HIGH EntityCount=1 IndicatorCount=1", got)
	}
	if got.ConfidenceReason != "sanctions_match indicator at weight 5" {
		t.Errorf("ConfidenceReason = %q, want it to round-trip through JSON", got.ConfidenceReason)
	}
}

// TestSendWebhookAlertSlackFormat guards the Slack incoming-webhook
// payload shape (confirmed live against Slack's own current docs):
// {"text": "..."} , not the generic summary object.
func TestSendWebhookAlertSlackFormat(t *testing.T) {
	var gotBody map[string]any
	var gotContentType string
	mux := http.NewServeMux()
	// sendWebhookAlert detects Slack by looking for "hooks.slack.com"
	// as a substring anywhere in the URL, not by matching the real
	// host -- so prefixing the httptest server's path with it exercises
	// the exact same branch a real https://hooks.slack.com/... URL
	// would take.
	mux.HandleFunc("/hooks.slack.com/services/T00/B00/XXX", func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL + "/hooks.slack.com/services/T00/B00/XXX"

	summary := riskSummaryJSON{Total: 8, Confidence: "HIGH", ConfidenceReason: "test reason", Queries: []string{"Example Corp"}}
	if err := sendWebhookAlert(url, summary); err != nil {
		t.Fatalf("sendWebhookAlert: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	text, ok := gotBody["text"].(string)
	if !ok || text == "" {
		t.Fatalf("gotBody = %+v, want a non-empty \"text\" field (Slack's format)", gotBody)
	}
	if !strings.Contains(text, "HIGH") || !strings.Contains(text, "8") {
		t.Errorf("text = %q, want it to mention the score and confidence", text)
	}
	if _, hasContent := gotBody["content"]; hasContent {
		t.Error("Slack payload should not have a \"content\" field (that's Discord's)")
	}
}

// TestSendWebhookAlertDiscordFormat guards the Discord webhook payload
// shape (confirmed live against Discord's own current docs):
// {"content": "..."}, not Slack's "text" or the generic summary.
func TestSendWebhookAlertDiscordFormat(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/discord.com/api/webhooks/123/abc", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	url := srv.URL + "/discord.com/api/webhooks/123/abc"

	summary := riskSummaryJSON{Total: 8, Confidence: "HIGH", ConfidenceReason: "test reason"}
	if err := sendWebhookAlert(url, summary); err != nil {
		t.Fatalf("sendWebhookAlert: %v", err)
	}
	content, ok := gotBody["content"].(string)
	if !ok || content == "" {
		t.Fatalf("gotBody = %+v, want a non-empty \"content\" field (Discord's format)", gotBody)
	}
	if _, hasText := gotBody["text"]; hasText {
		t.Error("Discord payload should not have a \"text\" field (that's Slack's)")
	}
}

// TestSendWebhookAlertGenericFormat guards the fallback for any other
// URL: the full compact summary as JSON, not a Slack/Discord-shaped
// message.
func TestSendWebhookAlertGenericFormat(t *testing.T) {
	var gotBody riskSummaryJSON
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	summary := riskSummaryJSON{Total: 8, Confidence: "HIGH", ConfidenceReason: "test reason", EntityCount: 3}
	if err := sendWebhookAlert(srv.URL+"/", summary); err != nil {
		t.Fatalf("sendWebhookAlert: %v", err)
	}
	if gotBody.Total != 8 || gotBody.Confidence != "HIGH" || gotBody.EntityCount != 3 {
		t.Errorf("gotBody = %+v, want the full summary round-tripped", gotBody)
	}
}

func TestSendWebhookAlertNonOKStatusIsAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := sendWebhookAlert(srv.URL+"/", riskSummaryJSON{}); err == nil {
		t.Fatal("expected an error for a non-2xx webhook response")
	}
}

// TestColorDisabledByFlagOrEnvExplicitFlag guards --no-color as an
// unconditional override, independent of the NO_COLOR env var or
// whether the output would otherwise look like a terminal (that half
// is colorEnabled's job, tested separately below).
func TestColorDisabledByFlagOrEnvExplicitFlag(t *testing.T) {
	if !colorDisabledByFlagOrEnv(true) {
		t.Error("colorDisabledByFlagOrEnv(true) = false, want true")
	}
}

// TestColorDisabledByFlagOrEnvNoColorVar guards the NO_COLOR
// convention (https://no-color.org): any non-empty value disables
// color regardless of its content.
func TestColorDisabledByFlagOrEnvNoColorVar(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if !colorDisabledByFlagOrEnv(false) {
		t.Error("colorDisabledByFlagOrEnv(false) with NO_COLOR set = false, want true")
	}
}

func TestColorDisabledByFlagOrEnvNeitherSet(t *testing.T) {
	t.Setenv("NO_COLOR", "") // ensure a clean slate regardless of the outer environment
	os.Unsetenv("NO_COLOR")
	if colorDisabledByFlagOrEnv(false) {
		t.Error("colorDisabledByFlagOrEnv(false) with nothing set = true, want false")
	}
}

// TestColorEnabledDisabledForNonTerminalWriter guards the common real
// case: writing to a plain file (e.g. --output, or stdout redirected
// to a file/pipe in a script) should never emit escape codes, since
// they're noise there, not information.
func TestColorEnabledDisabledForNonTerminalWriter(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	if colorEnabled(f, false) {
		t.Error("colorEnabled for a plain file = true, want false")
	}
}

// TestColorEnabledDisabledForNonFileWriter guards a non-*os.File
// io.Writer (e.g. a bytes.Buffer, which is what most of this file's
// other tests use) -- there's no terminal to check, so this must
// default to disabled, not panic on the failed type assertion.
func TestColorEnabledDisabledForNonFileWriter(t *testing.T) {
	var buf bytes.Buffer
	if colorEnabled(&buf, false) {
		t.Error("colorEnabled for a bytes.Buffer = true, want false")
	}
}

func TestColorizeWrapsOnlyWhenEnabled(t *testing.T) {
	if got := colorize("HIGH", ansiRed, false); got != "HIGH" {
		t.Errorf("colorize with enabled=false = %q, want unchanged", got)
	}
	got := colorize("HIGH", ansiRed, true)
	want := ansiRed + "HIGH" + ansiReset
	if got != want {
		t.Errorf("colorize with enabled=true = %q, want %q", got, want)
	}
}

func TestConfidenceColorMapsAllThreeBands(t *testing.T) {
	cases := map[string]string{"HIGH": ansiRed, "MEDIUM": ansiYellow, "LOW": ansiGreen}
	for band, want := range cases {
		if got := confidenceColor(band); got != want {
			t.Errorf("confidenceColor(%q) = %q, want %q", band, got, want)
		}
	}
}

func TestWeightColorMatchesConfidenceBandThresholds(t *testing.T) {
	// Same thresholds internal/risk's own confidenceBand uses (5+
	// high, 3+ moderate) -- an indicator's color should match the
	// same scale that produced the confidence band shown above it.
	cases := []struct {
		weight int
		want   string
	}{
		{6, ansiRed}, {5, ansiRed}, {4, ansiYellow}, {3, ansiYellow}, {2, ansiGreen}, {1, ansiGreen},
	}
	for _, c := range cases {
		if got := weightColor(c.weight); got != c.want {
			t.Errorf("weightColor(%d) = %q, want %q", c.weight, got, c.want)
		}
	}
}

func TestParseConfigFileLinesSkipsBlankLinesAndComments(t *testing.T) {
	content := "limit = 10\n\n# a comment\ncache-ttl = 24h\n#another comment\nquiet = true\n"
	values, warnings := parseConfigFileLines(content)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	want := map[string]string{"limit": "10", "cache-ttl": "24h", "quiet": "true"}
	if len(values) != len(want) {
		t.Fatalf("got %v, want %v", values, want)
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("values[%q] = %q, want %q", k, values[k], v)
		}
	}
}

func TestParseConfigFileLinesTrimsWhitespaceAroundKeyAndValue(t *testing.T) {
	values, _ := parseConfigFileLines("  limit   =   10  \n")
	if values["limit"] != "10" {
		t.Errorf("values[limit] = %q, want 10", values["limit"])
	}
}

func TestParseConfigFileLinesWarnsOnMalformedLine(t *testing.T) {
	values, warnings := parseConfigFileLines("limit 10\n")
	if len(values) != 0 {
		t.Errorf("values = %v, want none parsed from a line with no '='", values)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
}

func TestApplyConfigFileDefaultsMissingFileIsNotAnError(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Int("limit", 5, "")
	warnings := applyConfigFileDefaults(fs, map[string]bool{}, filepath.Join(t.TempDir(), "does-not-exist"))
	if warnings != nil {
		t.Errorf("warnings = %v, want nil for a missing config file", warnings)
	}
}

func TestApplyConfigFileDefaultsSetsUnsetFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paper-trailrc")
	if err := os.WriteFile(path, []byte("limit = 10\nquiet = true\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	limit := fs.Int("limit", 5, "")
	quiet := fs.Bool("quiet", false, "")

	warnings := applyConfigFileDefaults(fs, map[string]bool{}, path)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if *limit != 10 {
		t.Errorf("limit = %d, want 10 (from the config file)", *limit)
	}
	if !*quiet {
		t.Error("quiet = false, want true (from the config file)")
	}
}

// TestApplyConfigFileDefaultsExplicitFlagWins guards the core
// precedence rule: a flag actually passed on the command line must
// never be overridden by the config file.
func TestApplyConfigFileDefaultsExplicitFlagWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paper-trailrc")
	if err := os.WriteFile(path, []byte("limit = 10\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	limit := fs.Int("limit", 5, "")
	fs.Parse([]string{"-limit=99"}) // simulates the user explicitly passing --limit 99

	explicitlySet := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicitlySet[f.Name] = true })
	applyConfigFileDefaults(fs, explicitlySet, path)

	if *limit != 99 {
		t.Errorf("limit = %d, want 99 (the explicit CLI value, not the config file's 10)", *limit)
	}
}

func TestApplyConfigFileDefaultsWarnsOnUnrecognizedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paper-trailrc")
	if err := os.WriteFile(path, []byte("not-a-real-flag = 10\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	warnings := applyConfigFileDefaults(fs, map[string]bool{}, path)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not-a-real-flag") {
		t.Errorf("warnings = %v, want exactly 1 mentioning the unrecognized key", warnings)
	}
}

func TestWriteBatchRowsCSVFormat(t *testing.T) {
	rows := []riskBatchRow{
		{Query: "Example Corp", EntitiesFound: 2, Score: 7, Confidence: "MEDIUM", ConfidenceReason: "sanctions_match indicator at weight 5", IndicatorCount: 3, TopIndicator: "sanctions_match"},
		{Query: "Clean Org, Inc", EntitiesFound: 1, Score: 0, Confidence: "LOW", ConfidenceReason: "no indicators found", IndicatorCount: 0},
	}
	var buf bytes.Buffer
	if err := writeBatchRows(&buf, rows, false); err != nil {
		t.Fatalf("writeBatchRows: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "query,entities_found,score,confidence,confidence_reason,indicator_count,top_indicator") {
		t.Errorf("missing header row: %q", out)
	}
	if !strings.Contains(out, "Example Corp,2,7,MEDIUM,sanctions_match indicator at weight 5,3,sanctions_match") {
		t.Errorf("missing/malformed first row: %q", out)
	}
	// A name containing a comma must come back quoted (real CSV
	// escaping, not just naive comma-joining) -- encoding/csv handles
	// this, but worth guarding against a future hand-rolled rewrite.
	if !strings.Contains(out, `"Clean Org, Inc"`) {
		t.Errorf("comma-containing query not quoted: %q", out)
	}
}

func TestWriteBatchRowsJSONFormat(t *testing.T) {
	rows := []riskBatchRow{
		{Query: "Example Corp", EntitiesFound: 2, Score: 7, Confidence: "MEDIUM", ConfidenceReason: "sanctions_match indicator at weight 5", IndicatorCount: 3, TopIndicator: "sanctions_match"},
	}
	var buf bytes.Buffer
	if err := writeBatchRows(&buf, rows, true); err != nil {
		t.Fatalf("writeBatchRows: %v", err)
	}
	var decoded []riskBatchRow
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding JSON output: %v", err)
	}
	if len(decoded) != 1 || decoded[0] != rows[0] {
		t.Errorf("decoded = %+v, want %+v", decoded, rows)
	}
}

func TestWriteBatchRowsEmptyStillWritesHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := writeBatchRows(&buf, nil, false); err != nil {
		t.Fatalf("writeBatchRows: %v", err)
	}
	if !strings.Contains(buf.String(), "query,entities_found,score,confidence,confidence_reason,indicator_count,top_indicator") {
		t.Errorf("expected the header row even with zero rows: %q", buf.String())
	}
}

func TestApplyConfigFileDefaultsWarnsOnBadValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paper-trailrc")
	if err := os.WriteFile(path, []byte("limit = not-a-number\n"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Int("limit", 5, "")

	warnings := applyConfigFileDefaults(fs, map[string]bool{}, path)
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want exactly 1 for a value the int flag rejects", warnings)
	}
}
