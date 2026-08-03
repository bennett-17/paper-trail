package main

import (
	"encoding/base64"
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
// handler (not a live network call -- serveRiskRequest never calls
// gatherAndScore itself, that's deferred to /scan) to confirm an
// empty/first-load request renders just the search form, no progress
// bar and no report section.
func TestServeRiskRequestNoQueryShowsFormOnly(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	serveRiskRequest(rec, req, tmpl)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<textarea") {
		t.Error("expected the search form in the response")
	}
	if strings.Contains(body, `id="progress-wrap"`) {
		t.Error("no query was submitted, so no progress bar should have been rendered")
	}
	if strings.Contains(body, "Risk assessment for") {
		t.Error("no query was submitted, so no report should have been rendered")
	}
}

// TestServeRiskRequestBlankQueryOnlyShowsFormOnly guards the same path
// as above but via a query string that's present yet entirely
// whitespace -- splitServeQueries must still treat this as "no
// query", not open a progress bar for a blank name.
func TestServeRiskRequestBlankQueryOnlyShowsFormOnly(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/?q=+++", nil)
	rec := httptest.NewRecorder()
	serveRiskRequest(rec, req, tmpl)

	body := rec.Body.String()
	if strings.Contains(body, `id="progress-wrap"`) {
		t.Error("a blank/whitespace-only query should not open a progress bar")
	}
}

// TestServeRiskRequestRealQueryShowsProgressBarNotReport confirms the
// new deferred-to-/scan behavior: a real, non-blank query renders the
// progress-bar shell and an EventSource connection to /scan, but never
// runs a scan (and so never renders "Risk assessment for") in this
// request itself.
func TestServeRiskRequestRealQueryShowsProgressBarNotReport(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/?q=Example+Corp", nil)
	rec := httptest.NewRecorder()
	serveRiskRequest(rec, req, tmpl)

	body := rec.Body.String()
	if !strings.Contains(body, `id="progress-wrap"`) {
		t.Error("expected the progress bar shell for a real query")
	}
	if !strings.Contains(body, "/scan?q=") {
		t.Error("expected an EventSource connection to /scan")
	}
	if strings.Contains(body, "Risk assessment for") {
		t.Error("serveRiskRequest must never run the scan itself -- that's /scan's job")
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

// TestServeTemplateEscapesFormQueryInsideScript guards the new
// EventSource script block specifically: FormQuery is interpolated as
// a JS string literal there (var formQuery = {{.FormQuery}};), a
// different escaping context from the textarea's HTML content --
// html/template must still neutralize an attempt to break out of that
// JS string.
func TestServeTemplateEscapesFormQueryInsideScript(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	var buf strings.Builder
	view := serveView{FormQuery: `Example</script><script>alert(1)</script>`, Scanning: true}
	if err := tmpl.Execute(&buf, view); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	html := buf.String()
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a raw, unescaped </script><script> break-out from the JS string literal leaked into the output")
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

// TestServeScanSSENoQueryReturnsError exercises /scan's fast,
// no-network error path (an empty or blank q) -- the only part of
// serveScanSSE that's safe to test offline, since a real query calls
// gatherAndScore, which makes live API calls (see serveScanSSE's own
// doc comment). httptest.NewRecorder() implements http.Flusher (a
// no-op Flush), so this exercises the real handler function, not a
// stand-in.
func TestServeScanSSENoQueryReturnsError(t *testing.T) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		t.Fatalf("reportTemplate: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/scan?q=", nil)
	rec := httptest.NewRecorder()
	serveScanSSE(rec, req, tmpl, 5, &riskcache.Cache{}, 0, nil)

	body := rec.Body.String()
	if !strings.Contains(body, "event: scanerror") {
		t.Errorf("body = %q, want a scanerror event for an empty query", body)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}

// TestWriteSSEEventEncodesPayloadAsOneLine confirms the framing
// writeSSEEvent produces: "event: <name>" then "data: <json>", each on
// its own line, terminated by a blank line -- the shape every SSE
// client (including this project's own EventSource script) requires.
func TestWriteSSEEventEncodesPayloadAsOneLine(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSSEEvent(rec, rec, "progress", sseProgressPayload{Percent: 42, Source: "GDELT", Message: `checking "Example"`})

	body := rec.Body.String()
	if !strings.HasPrefix(body, "event: progress\ndata: ") {
		t.Fatalf("body = %q, want it to start with the SSE event/data prefix", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("body = %q, want it to end with a blank line terminating the event", body)
	}
	if !strings.Contains(body, `"percent":42`) || !strings.Contains(body, `"source":"GDELT"`) {
		t.Errorf("body = %q, missing expected JSON fields", body)
	}
}

// TestResultEventPayloadRoundTripsThroughBase64 confirms the encoding
// serveScanSSE uses for its final "result" event -- report HTML can
// itself contain newlines, which would break SSE's line-based framing
// if sent raw, so it's base64-encoded first the same way the browser's
// atob() call expects.
func TestResultEventPayloadRoundTripsThroughBase64(t *testing.T) {
	html := "<div class=\"report\">\n  <h2>Risk assessment</h2>\n</div>"
	encoded := base64.StdEncoding.EncodeToString([]byte(html))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	if string(decoded) != html {
		t.Errorf("round-tripped HTML = %q, want %q", decoded, html)
	}
	if strings.ContainsAny(encoded, "\n\r") {
		t.Error("base64-encoded payload must not contain a raw newline, or it would break SSE framing")
	}
}
