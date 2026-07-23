package main

import (
	"html/template"
	"os"
	"time"

	"github.com/bennett-17/paper-trail/internal/risk"
)

// reportHTMLView is what reportHTMLTemplate renders -- the same
// information as the text/--json report (queries, entities, notes,
// score, indicators, corroborations, and an optional diff), reshaped
// for html/template rather than passing riskReportJSON directly, so
// the template itself doesn't need to know about JSON tags or
// pointer-vs-value diff handling.
type reportHTMLView struct {
	Queries       []string
	Entities      []risk.Entity
	Notes         []string
	Score         risk.Score
	ExcludedCount int
	Diff          *riskReportDiff
	DiffSource    string
	GeneratedAt   string
}

// weightClass mirrors weightColor's thresholds (5+ high, 3+ moderate)
// -- the same scale the text report's ANSI color and the --html
// graph's high-weight node outline both already use, so all three of
// this tool's colored views agree with each other.
func weightClass(weight int) string {
	switch {
	case weight >= 5:
		return "sev-high"
	case weight >= 3:
		return "sev-med"
	default:
		return "sev-low"
	}
}

// confidenceClass mirrors confidenceColor's band mapping.
func confidenceClass(band string) string {
	switch band {
	case "HIGH":
		return "sev-high"
	case "MEDIUM":
		return "sev-med"
	default:
		return "sev-low"
	}
}

// writeReportHTML writes a single self-contained HTML file rendering
// the full risk report (indicators, evidence, corroborations, and the
// diff against a previous run if any) -- unlike --html/--graph (the
// entity/indicator graph view), this mirrors the full text/--json
// report itself, for opening in a browser or handing to someone else
// who doesn't have paper-trail installed. No server, no CDN, no
// external JS/CSS -- everything needed is embedded in the file, same
// approach as internal/graph's WriteHTML.
func writeReportHTML(report riskReportJSON, diff *riskReportDiff, diffSource, path string) error {
	view := reportHTMLView{
		Queries:       report.Queries,
		Entities:      report.Entities,
		Notes:         report.Notes,
		Score:         report.Score,
		ExcludedCount: report.ExcludedIndicators,
		Diff:          diff,
		DiffSource:    diffSource,
		GeneratedAt:   time.Now().Format("2006-01-02 15:04:05 MST"),
	}

	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"weightClass":     weightClass,
		"confidenceClass": confidenceClass,
		"sub":             func(a, b int) int { return a - b },
	}).Parse(reportHTMLTemplate)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, view)
}

const reportHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>paper-trail risk report</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root {
    color-scheme: light dark;
    --bg: #ffffff;
    --fg: #1a1a1a;
    --muted: #666666;
    --panel-bg: #f5f5f5;
    --panel-border: #dddddd;
    --high: #e15759;
    --med: #f0ad4e;
    --low: #59a14f;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #121212;
      --fg: #e8e8e8;
      --muted: #999999;
      --panel-bg: #1e1e1e;
      --panel-border: #333333;
    }
  }
  :root[data-theme="dark"] {
    --bg: #121212;
    --fg: #e8e8e8;
    --muted: #999999;
    --panel-bg: #1e1e1e;
    --panel-border: #333333;
  }
  :root[data-theme="light"] {
    --bg: #ffffff;
    --fg: #1a1a1a;
    --muted: #666666;
    --panel-bg: #f5f5f5;
    --panel-border: #dddddd;
  }
  * { box-sizing: border-box; }
  body {
    background: var(--bg);
    color: var(--fg);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
    max-width: 900px;
    margin: 0 auto;
    padding: 24px 20px 60px;
    line-height: 1.5;
  }
  h1 { font-size: 1.4em; margin-bottom: 4px; }
  h2 { font-size: 1.1em; margin-top: 2em; border-bottom: 1px solid var(--panel-border); padding-bottom: 4px; }
  .meta { color: var(--muted); font-size: 0.9em; }
  .score-line { font-size: 1.2em; margin: 1em 0; }
  .badge {
    display: inline-block;
    padding: 2px 10px;
    border-radius: 4px;
    font-weight: 600;
    color: #fff;
  }
  .sev-high { background: var(--high); color: #fff; }
  .sev-med { background: var(--med); color: #1a1a1a; }
  .sev-low { background: var(--low); color: #fff; }
  .indicator {
    border: 1px solid var(--panel-border);
    border-left-width: 5px;
    border-radius: 4px;
    padding: 10px 14px;
    margin-bottom: 10px;
    background: var(--panel-bg);
  }
  .indicator.sev-high { border-left-color: var(--high); }
  .indicator.sev-med { border-left-color: var(--med); }
  .indicator.sev-low { border-left-color: var(--low); }
  .indicator .desc { font-weight: 600; }
  .indicator .field { font-size: 0.92em; color: var(--muted); margin-top: 4px; }
  .indicator .weight-tag {
    float: right;
    font-weight: 700;
  }
  ul.plain { list-style: none; padding-left: 0; }
  ul.plain li { padding: 3px 0; }
  .corroboration {
    border: 1px solid var(--panel-border);
    border-radius: 4px;
    padding: 8px 14px;
    margin-bottom: 8px;
    background: var(--panel-bg);
  }
  .notes li, .diff-new li { color: var(--muted); }
  .disclaimer {
    margin-top: 3em;
    padding-top: 1em;
    border-top: 1px solid var(--panel-border);
    font-size: 0.85em;
    color: var(--muted);
  }
  .diff-score { font-weight: 600; }
</style>
</head>
<body>

<h1>Risk assessment for {{range $i, $q := .Queries}}{{if $i}}, {{end}}&#34;{{$q}}&#34;{{end}}</h1>
<div class="meta">Generated {{.GeneratedAt}} &middot; {{len .Entities}} entit{{if ne (len .Entities) 1}}ies{{else}}y{{end}} found</div>

<h2>Entities</h2>
{{if .Entities}}
<ul class="plain">
{{range .Entities}}<li>{{.Label}}</li>
{{end}}
</ul>
{{else}}
<p class="meta">No entities located among the configured sources.</p>
{{end}}

{{if .Notes}}
<h2>Notes</h2>
<ul class="plain notes">
{{range .Notes}}<li>{{.}}</li>
{{end}}
</ul>
{{end}}

{{if gt .ExcludedCount 0}}
<p class="meta">{{.ExcludedCount}} indicator(s) permanently excluded (--exclude/--exclude-file) -- not counted in the score below at all.</p>
{{end}}

<div class="score-line">
  Risk score: <strong>{{.Score.Total}}</strong>
  &nbsp;<span class="badge {{confidenceClass .Score.Confidence}}">{{.Score.Confidence}}</span>
  <div class="meta">{{.Score.ConfidenceReason}}</div>
</div>

<h2>Indicators</h2>
{{if .Score.Indicators}}
{{range .Score.Indicators}}
<div class="indicator {{weightClass .Weight}}">
  <span class="weight-tag {{weightClass .Weight}}">+{{.Weight}}</span>
  <div class="desc">{{.Description}}</div>
  <div class="field">Entities: {{range $i, $e := .Entities}}{{if $i}}; {{end}}{{$e}}{{end}}</div>
  <div class="field">Evidence: {{.Evidence}}</div>
</div>
{{end}}
{{else}}
<p class="meta">No structural indicators found among the entities located.</p>
{{end}}

{{if .Score.Corroborations}}
<h2>Corroborated pairs</h2>
<p class="meta">Matched on 2+ independent kinds of evidence -- stronger than any single indicator above.</p>
{{range .Score.Corroborations}}
<div class="corroboration">
  <div>{{range $i, $e := .Entities}}{{if $i}} &harr; {{end}}{{$e}}{{end}}</div>
  <div class="field">matched on: {{range $i, $c := .Codes}}{{if $i}}, {{end}}{{$c}}{{end}}</div>
</div>
{{end}}
{{end}}

{{if .Diff}}
<h2>Diff against {{.DiffSource}}</h2>
<p class="diff-score">Score: {{.Diff.ScoreBefore}} &rarr; {{.Diff.ScoreAfter}} ({{if ge (sub .Diff.ScoreAfter .Diff.ScoreBefore) 0}}+{{end}}{{sub .Diff.ScoreAfter .Diff.ScoreBefore}})</p>
<p>{{len .Diff.NewEntities}} new entit{{if ne (len .Diff.NewEntities) 1}}ies{{else}}y{{end}}:</p>
<ul class="plain diff-new">
{{range .Diff.NewEntities}}<li>{{.Label}}</li>
{{end}}
</ul>
<p>{{len .Diff.NewIndicators}} new indicator(s):</p>
{{range .Diff.NewIndicators}}
<div class="indicator {{weightClass .Weight}}">
  <span class="weight-tag {{weightClass .Weight}}">+{{.Weight}}</span>
  <div class="desc">{{.Description}}</div>
  <div class="field">Entities: {{range $i, $e := .Entities}}{{if $i}}; {{end}}{{$e}}{{end}}</div>
  <div class="field">Evidence: {{.Evidence}}</div>
</div>
{{end}}
{{end}}

<div class="disclaimer">
  This is a lead-generation report, not a finding &mdash; verify every indicator by hand before drawing any conclusion. It is not a determination of money laundering, tax evasion, terrorism financing, or any other wrongdoing.
</div>

</body>
</html>
`
