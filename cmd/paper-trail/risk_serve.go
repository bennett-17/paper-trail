package main

import (
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
// current value, plus the rendered report (nil until a search has
// actually been run).
type serveView struct {
	FormQuery string
	Report    *reportHTMLView
}

// runRiskServe starts a local, loopback-only HTTP server -- always
// bound to 127.0.0.1 regardless of what's passed, never any other
// interface, since there's no legitimate reason for this local
// investigation tool to be reachable from the network -- presenting a
// search form and rendering results with the exact same HTML report
// template as --report-html. Each request runs its own independent
// scan via gatherAndScore; there's no session state or database, just
// the same live-query pipeline the CLI itself uses, one request at a
// time.
func runRiskServe(port string, limit int, cache *riskcache.Cache, cacheTTL time.Duration, excludeTerms []string, quiet bool) {
	tmpl, err := reportTemplate("serve", servePageTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: building serve template: %v\n", err)
		os.Exit(1)
	}

	addr := net.JoinHostPort("127.0.0.1", port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveRiskRequest(w, r, tmpl, limit, cache, cacheTTL, excludeTerms)
	})

	if !quiet {
		fmt.Fprintf(os.Stderr, "paper-trail: serving local web UI at http://%s -- Ctrl+C to stop\n", addr)
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func serveRiskRequest(w http.ResponseWriter, r *http.Request, tmpl *template.Template, limit int, cache *riskcache.Cache, cacheTTL time.Duration, excludeTerms []string) {
	formQuery := r.URL.Query().Get("q")
	view := serveView{FormQuery: formQuery}

	queries := splitServeQueries(formQuery)
	if len(queries) > 0 {
		entities, notes, score := gatherAndScore(queries, limit, cache, cacheTTL, nil)
		score, excludedCount := excludeIndicators(score, excludeTerms)
		report := newReportHTMLView(riskReportJSON{
			Queries:            queries,
			Entities:           entities,
			Notes:              notes,
			Score:              score,
			ExcludedIndicators: excludedCount,
		}, nil, "")
		view.Report = &report
	}

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

// servePageTemplate is --serve's browser page: a search form plus,
// once a search has run, the same "reportBody" block reportPageTemplate
// renders for --report-html's file output.
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
</style>
</head>
<body>

<h1>paper-trail</h1>
<form class="search-form" method="get" action="/">
  <textarea name="q" placeholder="One name per line -- multiple lines are cross-referenced together, same as passing several arguments on the command line">{{.FormQuery}}</textarea>
  <div><button type="submit">Search</button></div>
</form>

{{if .Report}}{{template "reportBody" .Report}}{{end}}

</body>
</html>
`
