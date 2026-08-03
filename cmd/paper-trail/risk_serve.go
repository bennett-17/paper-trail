package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bennett-17/paper-trail/internal/riskcache"
)

// serveView is what servePageTemplate renders -- the search form's
// current value, whether a scan should be kicked off via the /scan SSE
// endpoint, and the rendered report (nil on every request now that a
// submitted query defers to /scan rather than running synchronously;
// kept as a field, and still exercised directly by
// TestServeTemplateRendersReportWhenPresent, since the template itself
// is still capable of rendering a report inline if a future caller
// ever wants to skip the SSE round-trip).
type serveView struct {
	FormQuery string
	Scanning  bool
	Report    *reportHTMLView
}

// runRiskServe starts a local, loopback-only HTTP server -- always
// bound to 127.0.0.1 regardless of what's passed, never any other
// interface, since there's no legitimate reason for this local
// investigation tool to be reachable from the network. "/" renders the
// search form and, once a query is submitted, a progress bar that
// opens a Server-Sent Events connection to "/scan", which is where the
// actual scan runs -- the same live-query gatherAndScore pipeline the
// CLI itself uses, streaming percent-complete updates as it goes and
// finishing with the same HTML report template --report-html and the
// old synchronous version of this handler both use. There's no session
// state or database, just one goroutine per open browser tab's /scan
// connection.
func runRiskServe(port string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, excludeTerms []string, quiet bool) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: building serve template: %v\n", err)
		os.Exit(1)
	}

	addr := net.JoinHostPort("127.0.0.1", port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveRiskRequest(w, r, tmpl)
	})
	mux.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
		serveScanSSE(w, r, tmpl, limit, cache, cacheTTL, excludeTerms)
	})

	if !quiet {
		fmt.Fprintf(os.Stderr, "paper-trail: serving local web UI at http://%s -- Ctrl+C to stop\n", addr)
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// serveRiskRequest renders the page shell only -- the search form, and
// (once a non-blank query is submitted) the progress-bar markup that
// opens the actual scan via /scan. It never calls gatherAndScore
// itself, so it's fast and fully offline-testable regardless of what
// query is submitted.
func serveRiskRequest(w http.ResponseWriter, r *http.Request, tmpl *template.Template) {
	formQuery := r.URL.Query().Get("q")
	queries := splitServeQueries(formQuery)
	view := serveView{FormQuery: formQuery, Scanning: len(queries) > 0}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, view); err != nil {
		// html/template buffers most execution failures before
		// writing anything, but that's not guaranteed once output has
		// started -- log rather than also trying to write an error
		// response on top of whatever's already been sent, and never
		// os.Exit here: a rendering problem on one request must not
		// take the whole server down for every other client.
		fmt.Fprintf(os.Stderr, "paper-trail serve: rendering response: %v\n", err)
	}
}

// sseProgressPayload is the JSON shape sent on every "progress" SSE
// event -- deliberately small (just what the browser's progress bar
// needs), not a full progressUpdate (which carries an
// unmarshal-unfriendly time.Duration).
type sseProgressPayload struct {
	Percent int    `json:"percent"`
	Source  string `json:"source,omitempty"`
	Message string `json:"message,omitempty"`
}

// writeSSEEvent writes one Server-Sent Events frame and flushes it
// immediately, so the browser sees it as soon as it's written rather
// than buffered until the response eventually closes. payload is
// marshaled to JSON -- json.Marshal never emits a raw, unescaped
// newline inside a string, so every event this function writes is
// guaranteed to fit SSE's one-line-per-"data:"-field framing without
// needing a dedicated escaper.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(`{"error":"internal: failed to encode progress payload"}`)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

// serveScanSSE runs one risk scan and streams its progress to the
// browser as Server-Sent Events: a "progress" event (sseProgressPayload)
// each time gatherAndScore's progressReporter fires, then a single
// "result" event whose data is the rendered report body HTML,
// base64-encoded so arbitrary report content (which can itself contain
// newlines) can never break SSE's line-based framing. A "scanerror"
// event covers the two ways this can fail before any real work starts
// (no query, or streaming isn't supported by whatever's in front of
// this server).
//
// Not covered by the offline test suite beyond its fast, no-network
// error path (TestServeScanSSENoQueryReturnsError below): the real
// scan path calls gatherAndScore, which makes live API calls, the same
// reason risk_gather_test.go/risk_screen_test.go test gatherAndScore's
// pieces individually rather than the whole pipeline end-to-end.
// Live-verified manually against a real browser instead.
//
// If the browser tab closes mid-scan, this handler has no way to know
// -- gatherAndScore doesn't accept a context.Context, so the scan
// keeps running server-side to completion (the final writeSSEEvent
// call simply fails silently into a closed connection). Acceptable for
// a single-user local tool; threading cancellation through every
// gather/screen function would be a much larger change for a rare case.
func serveScanSSE(w http.ResponseWriter, r *http.Request, tmpl *template.Template, limit int, cache *riskcache.Cache, cacheTTL time.Duration, excludeTerms []string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	queries := splitServeQueries(r.URL.Query().Get("q"))
	if len(queries) == 0 {
		writeSSEEvent(w, flusher, "scanerror", struct {
			Error string `json:"error"`
		}{"no query provided"})
		return
	}

	progress := newSSEProgressReporter(func(u progressUpdate) {
		writeSSEEvent(w, flusher, "progress", sseProgressPayload{Percent: u.Percent, Source: u.Source, Message: u.Message})
	})

	entities, notes, score := gatherAndScore(queries, limit, cache, cacheTTL, progress)
	score, excludedCount := excludeIndicators(score, excludeTerms)
	report := newReportHTMLView(riskReportJSON{
		Queries:            queries,
		Entities:           entities,
		Notes:              notes,
		Score:              score,
		ExcludedIndicators: excludedCount,
	}, nil, "")

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "reportBody", report); err != nil {
		writeSSEEvent(w, flusher, "scanerror", struct {
			Error string `json:"error"`
		}{fmt.Sprintf("rendering report: %v", err)})
		return
	}

	fmt.Fprintf(w, "event: result\ndata: %s\n\n", base64.StdEncoding.EncodeToString(buf.Bytes()))
	flusher.Flush()
}

// splitServeQueries splits the search form's one-name-per-line
// textarea into individual query terms -- the same shape
// --input-file accepts, blank lines ignored -- so typing several
// names cross-references them together in one scan, same as passing
// multiple arguments on the command line.
func splitServeQueries(raw string) []string {
	var queries []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		queries = append(queries, line)
	}
	return queries
}

// servePageTemplate is --serve's browser page: a search form, then
// (once a query is submitted) a live progress bar backed by an
// EventSource connection to /scan, which swaps in the same
// "reportBody" block reportPageTemplate renders for --report-html's
// file output once the scan completes.
const servePageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>paper-trail</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
` + reportStyle + `
<style>
  .search-form { margin-bottom: 1.5em; }
  .search-form textarea {
    width: 100%;
    min-height: 4.5em;
    font-family: inherit;
    font-size: 1em;
    padding: 8px;
    border: 1px solid var(--panel-border);
    border-radius: 4px;
    background: var(--panel-bg);
    color: var(--fg);
  }
  .search-form button {
    margin-top: 8px;
    padding: 8px 22px;
    font-size: 1em;
    border-radius: 4px;
    border: 1px solid var(--panel-border);
    background: var(--panel-bg);
    color: var(--fg);
    cursor: pointer;
  }
  .search-form button:hover { opacity: 0.85; }
  .progress-wrap { margin: 1.5em 0; }
  .progress-track {
    width: 100%;
    height: 18px;
    background: var(--panel-bg);
    border: 1px solid var(--panel-border);
    border-radius: 9px;
    overflow: hidden;
  }
  .progress-fill {
    height: 100%;
    width: 0%;
    background: var(--low);
    transition: width 0.3s ease;
  }
  .progress-label { margin-top: 6px; font-size: 0.9em; color: var(--muted); }
</style>
</head>
<body>

<h1>paper-trail</h1>
<form class="search-form" method="get" action="/">
  <textarea name="q" placeholder="One name per line -- multiple lines are cross-referenced together, same as passing several arguments on the command line">{{.FormQuery}}</textarea>
  <div><button type="submit">Search</button></div>
</form>

{{if .Scanning}}
<div class="progress-wrap" id="progress-wrap">
  <div class="progress-track"><div class="progress-fill" id="progress-fill"></div></div>
  <div class="progress-label" id="progress-label">0% -- starting...</div>
</div>
<div id="report-target"></div>
<script>
(function(){
  var formQuery = {{.FormQuery}};
  var fill = document.getElementById('progress-fill');
  var label = document.getElementById('progress-label');
  var wrap = document.getElementById('progress-wrap');
  var target = document.getElementById('report-target');
  var es = new EventSource('/scan?q=' + encodeURIComponent(formQuery));
  es.addEventListener('progress', function(e){
    var d = JSON.parse(e.data);
    var status = d.source ? (' -- ' + d.source + (d.message ? ': ' + d.message : '')) : '';
    fill.style.width = d.percent + '%';
    label.textContent = d.percent + '%' + status;
  });
  es.addEventListener('result', function(e){
    target.innerHTML = atob(e.data);
    wrap.style.display = 'none';
    es.close();
  });
  es.addEventListener('scanerror', function(e){
    var d = JSON.parse(e.data);
    label.textContent = 'Error: ' + d.error;
    es.close();
  });
})();
</script>
{{end}}

{{if .Report}}{{template "reportBody" .Report}}{{end}}

</body>
</html>
`
