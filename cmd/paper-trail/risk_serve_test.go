package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
	"github.com/bennett-17/paper-trail/internal/riskcache"
)

func TestSplitServeQueriesSplitsLinesAndSkipsBlank(t *testing.T) {
	got := splitServeQueries("Alpha Inc\n\n  Beta LLC  \n\nGamma Trust\n")
	want := []string{"Alpha Inc", "Beta LLC", "Gamma Trust"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitServeQueries = %v, want %v", got, want)
	}
}

func TestSplitServeQueriesBlankInputReturnsNil(t *testing.T) {
	if got := splitServeQueries("   \n\n  \n"); got != nil {
		t.Errorf("splitServeQueries(blank) = %v, want nil", got)
	}
}

// TestServeRiskRequestNoQueryShowsFormOnly exercises the real HTTP
// handler (not a live network call, since gatherAndScore is only
// invoked when the form actually submitted a query) to confirm an
// empty/first-load request renders just the search form, no report
// section.
func TestServeRiskRequestNoQueryShowsFormOnly(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	serveRiskRequest(rec, req, tmpl, 5, &riskcache.Cache{}, 0, nil)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<textarea") {
		t.Error("expected the search form in the response")
	}
	if strings.Contains(body, "Risk assessment for") {
		t.Error("no query was submitted, so no report should have been rendered")
	}
}

// TestServeRiskRequestBlankQuoteOnlyShowsFormOnly guards the same path
// as above but via a query string that's present yet entirely
// whitespace -- splitServeQueries must still treat this as "no
// query", not run a scan against a blank name.
func TestServeRiskRequestBlankQueryOnlyShowsFormOnly(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/?q=+++", nil)
	rec := httptest.NewRecorder()
	serveRiskRequest(rec, req, tmpl, 5, &riskcache.Cache{}, 0, nil)

	body := rec.Body.String()
	if strings.Contains(body, "Risk assessment for") {
		t.Error("a blank/whitespace-only query should not trigger a scan")
	}
}

func TestServeTemplateEscapesFormQuery(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	var buf strings.Builder
	view := serveView{FormQuery: `"><script>alert(1)</script>`}
	if err := tmpl.Execute(&buf, view); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	html := buf.String()
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a raw, unescaped <script> tag from the reflected form value leaked into the output")
	}
}

func TestServeTemplateRendersReportWhenPresent(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	report := newReportHTMLView(riskReportJSON{
		Queries: []string{"Example Corp"},
		Score:   risk.Score{Total: 3, Confidence: "LOW", ConfidenceReason: "no indicators found"},
	}, nil, "")
	view := serveView{FormQuery: "Example Corp", Report: &report}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, view); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "Risk assessment for") {
		t.Error("expected the report body to render when Report is set")
	}
	if !strings.Contains(html, "Example Corp") {
		t.Error("expected the query to appear in the rendered report")
	}
}
