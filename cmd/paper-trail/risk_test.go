package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	writeSummary(&buf, report, nil, false)
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
	writeSummary(&buf, report, diff, false)
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
	writeSummary(&buf, report, nil, true)

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
