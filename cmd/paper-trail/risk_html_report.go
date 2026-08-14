package main

import (
	"html/template"
	"os"
	"sort"
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
	Queries        []string
	Entities       []risk.Entity
	Notes          []string
	Score          risk.Score
	EntityCards    []entityCard
	CardGroups     []entityCardGroup
	Duplicates     map[string][]duplicateRef
	IrrelevantCard map[string]bool
	VerifiedAt     map[string]string
	Timeline       []timelineEntry
	People         []personEntry
	ExcludedCount  int
	ReviewedCount  int
	SourceHealth   sourceHealth
	Diff           *riskReportDiff
	DiffSource     string
	GeneratedAt    string
}

// entityCard groups every indicator naming one entity label, so the
// HTML report can render "everything found about X" as a single card
// instead of the flat weight-sorted list making the reader re-scan the
// whole report to piece one entity's findings back together (exactly
// the friction hit by hand -- grouping indicators by entity, summing
// weight per entity -- while reading a real multi-hundred-entity scan;
// see groupIndicatorsByEntity).
type entityCard struct {
	Label       string
	TotalWeight int
	Indicators  []risk.Indicator
}

// groupIndicatorsByEntity groups indicators (already weight-sorted by
// risk.Assess) by every entity label they name. An indicator naming
// more than one entity (shared_person and friends) appears on every
// one of those entities' cards, not just the first -- each entity is
// independently a lead worth seeing it from, the same "duplicated
// intentionally" reasoning risk.Score's own Corroborations already
// applies to entity pairs. Cards are sorted by total weight (the sum
// of every indicator's weight on that card) descending, breaking ties
// alphabetically by label so output stays deterministic run to run.
// Indicators within a card keep the order they arrived in, which is
// already weight-descending.
func groupIndicatorsByEntity(indicators []risk.Indicator) []entityCard {
	byLabel := make(map[string]*entityCard)
	var order []string
	for _, ind := range indicators {
		for _, label := range ind.Entities {
			card, ok := byLabel[label]
			if !ok {
				card = &entityCard{Label: label}
				byLabel[label] = card
				order = append(order, label)
			}
			card.Indicators = append(card.Indicators, ind)
			card.TotalWeight += ind.Weight
		}
	}

	cards := make([]entityCard, 0, len(order))
	for _, label := range order {
		cards = append(cards, *byLabel[label])
	}
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].TotalWeight != cards[j].TotalWeight {
			return cards[i].TotalWeight > cards[j].TotalWeight
		}
		return cards[i].Label < cards[j].Label
	})
	return cards
}

// otherEntities returns ind's named entities minus self -- used by the
// template so a card for one entity still shows which OTHER entities a
// multi-entity indicator (e.g. shared_person) also names, instead of
// silently dropping that context just because it's rendered once per
// entity now rather than once per indicator.
func otherEntities(ind risk.Indicator, self string) []string {
	others := make([]string, 0, len(ind.Entities))
	for _, e := range ind.Entities {
		if e != self {
			others = append(others, e)
		}
	}
	return others
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

// newReportHTMLView builds the view reportBodyTemplate renders from a
// riskReportJSON -- shared by writeReportHTML (the --report-html file
// output) and --serve's per-request handler, so both go through the
// exact same report rendering.
func newReportHTMLView(report riskReportJSON, diff *riskReportDiff, diffSource string) reportHTMLView {
	cards := groupIndicatorsByEntity(report.Score.Indicators)

	irrelevant := make(map[string]bool, len(cards))
	for _, c := range cards {
		if !cardSharesWordWithQueries(c.Label, report.Queries) {
			irrelevant[c.Label] = true
		}
	}

	return reportHTMLView{
		Queries:        report.Queries,
		Entities:       report.Entities,
		Notes:          report.Notes,
		Score:          report.Score,
		EntityCards:    cards,
		CardGroups:     groupCardsByTier(cards),
		Duplicates:     possibleDuplicates(cards, report.Entities),
		IrrelevantCard: irrelevant,
		VerifiedAt:     verifiedAtByLabel(report.Entities),
		Timeline:       buildTimeline(report.Score.Indicators),
		People:         buildPersonPanel(report.Entities),
		ExcludedCount:  report.ExcludedIndicators,
		ReviewedCount:  report.ReviewedIndicators,
		SourceHealth:   report.SourceHealth,
		Diff:           diff,
		DiffSource:     diffSource,
		GeneratedAt:    time.Now().Format("2006-01-02 15:04:05 MST"),
	}
}

// timelineEntry is one dated indicator, laid out chronologically in
// the HTML report's own timeline section rather than by weight or by
// entity. Same data as the entity cards -- nothing here is a separate
// finding -- reordered along the one axis those views can't show.
type timelineEntry struct {
	Date        string // YYYY-MM-DD, always parseable (see buildTimeline)
	Code        string
	Entity      string // the indicator's first named entity, "" if it names none
	Description string
	Evidence    string
	Weight      int
}

// buildTimeline collects every indicator carrying a usable Date into
// one chronological list, oldest first. This is a deliberately partial
// first version: only a subset of indicator types populate Date at all
// today (see risk.Indicator.Date), and one without a usable date is
// simply absent here rather than shown with a guessed one -- so the
// timeline is honest about what it knows, never padded. Widening
// coverage is additive: any construction site that starts setting Date
// shows up here automatically, with no change to this function.
//
// An unparseable stored Date is skipped rather than crashing or
// sorting arbitrarily, so a future source sending an unexpected format
// degrades to "absent from the timeline", the same as no date at all.
func buildTimeline(indicators []risk.Indicator) []timelineEntry {
	var out []timelineEntry
	for _, ind := range indicators {
		date := risk.NormalizeIndicatorDate(ind.Date)
		if date == "" {
			continue
		}
		entity := ""
		if len(ind.Entities) > 0 {
			entity = ind.Entities[0]
		}
		out = append(out, timelineEntry{
			Date:        date,
			Code:        ind.Code,
			Entity:      entity,
			Description: ind.Description,
			Evidence:    ind.Evidence,
			Weight:      ind.Weight,
		})
	}
	// Oldest first: a timeline reads forward. Ties break on code then
	// entity so output stays deterministic run to run (several real
	// indicators genuinely share one date -- that's often the point).
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Entity < out[j].Entity
	})
	return out
}

// verifiedAtByLabel indexes report.Entities' VerifiedAt by Label(),
// keeping only entities that actually have one set (i.e. were served
// from --cache-ttl's on-disk cache this run, see riskcache.Cache.Get)
// -- displayed as "cached, verified <time>", reformatted from the
// stored RFC3339 value for readability, falling back to the raw
// stored string on the (should-never-happen) case it doesn't parse
// rather than silently dropping a value that IS there.
func verifiedAtByLabel(entities []risk.Entity) map[string]string {
	out := map[string]string{}
	for _, e := range entities {
		if e.VerifiedAt == "" {
			continue
		}
		display := e.VerifiedAt
		if t, err := time.Parse(time.RFC3339, e.VerifiedAt); err == nil {
			display = t.Format("2006-01-02 15:04 MST")
		}
		out[e.Label()] = display
	}
	return out
}

// reportTemplate parses page (a full HTML document's worth of
// template text, e.g. reportPageTemplate or --serve's
// servePageTemplate) together with reportBodyTemplate, which defines
// the "reportBody" named block both invoke via
// {{template "reportBody" .}} to render the actual report content --
// so the report itself (indicators, evidence, corroborations, diff)
// is written once and shared between the file-output and served-page
// cases, not duplicated.
func reportTemplate(name, page string) (*template.Template, error) {
	return template.New(name).Funcs(template.FuncMap{
		"weightClass":        weightClass,
		"confidenceClass":    confidenceClass,
		"sub":                func(a, b int) int { return a - b },
		"otherEntities":      otherEntities,
		"dupsWithConfidence": dupsWithConfidence,
	}).Parse(page + reportBodyTemplate)
}

// dupsWithConfidence filters dups down to just the labels at the given
// confidence ("likely" or "possible") -- lets the template render the
// two tiers with their own wording (see reportBodyTemplate's dup-note
// block) without a template-language filter of its own.
func dupsWithConfidence(dups []duplicateRef, confidence string) []string {
	var out []string
	for _, d := range dups {
		if d.Confidence == confidence {
			out = append(out, d.Label)
		}
	}
	return out
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
	view := newReportHTMLView(report, diff, diffSource)

	tmpl, err := reportTemplate("report", reportPageTemplate)
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

// reportStyle is the CSS shared by --report-html's file output and
// --serve's browser page, so the two always look consistent.
const reportStyle = `<style>
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
  .entity-card {
    border: 1px solid var(--panel-border);
    border-left-width: 6px;
    border-radius: 4px;
    padding: 12px 14px 4px;
    margin-bottom: 14px;
  }
  .entity-card.sev-high { border-left-color: var(--high); }
  .entity-card.sev-med { border-left-color: var(--med); }
  .entity-card.sev-low { border-left-color: var(--low); }
  .entity-card-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: 10px;
    margin-bottom: 10px;
  }
  .entity-card-name { font-weight: 700; font-size: 1.02em; }
  .entity-card .indicator { margin-bottom: 10px; }
  .source-health {
    border: 1px solid var(--med);
    border-radius: 4px;
    padding: 8px 14px;
    margin: 10px 0;
    font-size: 0.9em;
  }
  .source-health .field { margin: 2px 0; }
  .entity-filter {
    display: block;
    width: 100%;
    max-width: 420px;
    margin: 10px 0 16px;
    padding: 7px 10px;
    font-size: 0.95em;
    border: 1px solid var(--panel-border);
    border-radius: 4px;
    background: var(--panel-bg);
    color: var(--fg);
  }
  .tier-group { margin-bottom: 18px; }
  .tier-group > summary {
    cursor: pointer;
    font-weight: 700;
    padding: 6px 0;
    border-bottom: 1px solid var(--panel-border);
    margin-bottom: 10px;
  }
  .tier-count { font-weight: 400; color: var(--muted); }
  .flag {
    font-size: 0.78em;
    font-weight: 600;
    padding: 1px 8px;
    border-radius: 10px;
    white-space: nowrap;
  }
  .flag-irrelevant { background: var(--med); color: #1a1a1a; }
  .flag-cached { background: var(--panel-border); color: var(--muted); font-weight: 400; }
  /* A reviewed indicator is dimmed and collapsed by default -- a fourth
     axis alongside the severity tiers, not a replacement for them: a
     reviewed Confirmed-fact indicator is still worth a second glance
     eventually, just not on every re-run. */
  .indicator-reviewed { opacity: 0.62; }
  .indicator-reviewed summary { cursor: pointer; }
  .indicator-reviewed[open] { opacity: 0.85; }
  /* Timeline entries reuse the same left-severity-stripe language as
     .entity-card, so a reader doesn't learn a second visual system for
     the same weights. */
  .timeline-entry {
    border-left: 3px solid var(--panel-border);
    padding: 6px 0 6px 12px;
    margin-bottom: 10px;
  }
  .timeline-entry.sev-high { border-left-color: var(--high); }
  .timeline-entry.sev-med { border-left-color: var(--med); }
  .timeline-entry.sev-low { border-left-color: var(--low); }
  .timeline-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .timeline-date { font-weight: 700; font-variant-numeric: tabular-nums; }
  .timeline-code { color: var(--muted); font-size: 0.9em; }
  .reviewed-tag {
    font-size: 0.78em;
    font-weight: 600;
    padding: 1px 8px;
    border-radius: 10px;
    white-space: nowrap;
    background: var(--panel-border);
    color: var(--muted);
  }
  .dup-note {
    font-size: 0.88em;
    color: var(--muted);
    border-top: 1px dashed var(--panel-border);
    padding-top: 6px;
    margin: 6px 0 10px;
  }
  .dup-note.dup-likely { color: var(--med); font-weight: 600; }
  .person-card {
    border: 1px solid var(--panel-border);
    border-radius: 4px;
    padding: 8px 14px;
    margin-bottom: 8px;
    background: var(--panel-bg);
  }
  .person-name { font-weight: 700; }
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
</style>`

// reportFilterScript defines filterEntityCards (see the entity-filter
// input in reportBodyTemplate) and MUST live in each page shell
// (reportPageTemplate/servePageTemplate) rather than inside
// reportBodyTemplate itself -- --serve's browser page swaps the report
// body into the DOM via innerHTML once a scan finishes (see
// servePageTemplate's "result" SSE handler), and a <script> tag
// inserted that way is parsed but never executed, silently leaving
// filterEntityCards undefined the moment the reader tries to use the
// search box (caught live: the box accepted input but nothing
// filtered). Defining the function in the page shell instead means
// it's already in scope by the time the report body -- which only
// ever contains the input element and its oninput reference, not the
// function itself -- gets injected, in --serve as well as
// --report-html's ordinary, unproblematic full-page load.
const reportFilterScript = `<script>
// Client-side only -- filters the already-rendered entity cards by
// substring match against each card's own text (name + evidence), no
// server round-trip. A card's tier-group auto-opens while it has a
// match and hides entirely once none of its cards do, so a search
// term can surface a match buried in the collapsed weak-signal tier
// without the reader having to expand it by hand first.
function filterEntityCards(query) {
  var q = query.trim().toLowerCase();
  var groups = document.querySelectorAll('.tier-group');
  var totalVisible = 0;
  groups.forEach(function (group) {
    var cards = group.querySelectorAll('.entity-card');
    var visibleInGroup = 0;
    cards.forEach(function (card) {
      var match = q === '' || card.textContent.toLowerCase().indexOf(q) !== -1;
      card.style.display = match ? '' : 'none';
      if (match) visibleInGroup++;
    });
    group.style.display = visibleInGroup > 0 ? '' : 'none';
    if (q !== '' && visibleInGroup > 0) group.open = true;
    totalVisible += visibleInGroup;
  });
  var empty = document.getElementById('entity-filter-empty');
  if (empty) empty.style.display = (q !== '' && totalVisible === 0) ? '' : 'none';
}
</script>`

// reportPageTemplate is the full HTML document --report-html writes
// to a file: a normal page wrapping reportBodyTemplate's "reportBody"
// block.
const reportPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>paper-trail risk report</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
` + reportStyle + `
` + reportFilterScript + `
</head>
<body>
{{template "reportBody" .}}
</body>
</html>
`

// reportBodyTemplate defines the "reportBody" named block -- the
// report content itself, shared verbatim between reportPageTemplate
// (--report-html's file output) and --serve's servePageTemplate.
const reportBodyTemplate = `{{define "reportBody"}}
<h1>Risk assessment for {{range $i, $q := .Queries}}{{if $i}}, {{end}}&#34;{{$q}}&#34;{{end}}</h1>
<div class="meta">Generated {{.GeneratedAt}} &middot; {{len .Entities}} entit{{if ne (len .Entities) 1}}ies{{else}}y{{end}} found</div>

{{if or .SourceHealth.Degraded .SourceHealth.Skipped}}
<div class="source-health">
  {{if .SourceHealth.Degraded}}<div class="field"><strong>Degraded</strong> (repeatedly failed mid-scan -- likely a live outage or rate limit; whatever this source didn't find is a real gap, not a confirmed absence): {{range $i, $s := .SourceHealth.Degraded}}{{if $i}}, {{end}}{{$s}}{{end}}</div>{{end}}
  {{if .SourceHealth.Skipped}}<div class="field"><strong>Skipped</strong> (no API key configured -- routine, not an error): {{range $i, $s := .SourceHealth.Skipped}}{{if $i}}, {{end}}{{$s}}{{end}}</div>{{end}}
</div>
{{end}}

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
{{if gt .ReviewedCount 0}}
<p class="meta">{{.ReviewedCount}} indicator(s) marked reviewed (--reviewed-file) -- still counted in the score below in full, just dimmed and collapsed so they stop competing for attention on every re-run.</p>
{{end}}

<div class="score-line">
  Risk score: <strong>{{.Score.Total}}</strong>
  &nbsp;<span class="badge {{confidenceClass .Score.Confidence}}">{{.Score.Confidence}}</span>
  <div class="meta">{{.Score.ConfidenceReason}}</div>
</div>

<h2>Findings by entity</h2>
{{if .EntityCards}}
<p class="meta">Every entity named by at least one indicator, grouped by severity tier and, within each tier, sorted by that entity's own total weight -- so everything found about one entity reads as a single card, and the tail of single-signal noise stays out of the way until you go looking for it.</p>
<input type="search" class="entity-filter" placeholder="Filter by name or evidence&hellip;" oninput="filterEntityCards(this.value)" aria-label="Filter entity cards">
{{range .CardGroups}}
<details class="tier-group"{{if not .Collapsed}} open{{end}}>
  <summary>{{.Label}} <span class="tier-count">({{len .Cards}})</span></summary>
  {{range .Cards}}
  {{$label := .Label}}
  {{$dups := index $.Duplicates $label}}
  <div class="entity-card {{weightClass .TotalWeight}}">
    <div class="entity-card-head">
      <span class="entity-card-name">{{$label}}</span>
      {{if index $.IrrelevantCard $label}}<span class="flag flag-irrelevant" title="This entity's own name doesn't share a distinctive word with any search term -- it may have surfaced via a generic keyword collision rather than a genuine connection to what was searched for. Not a verdict, just worth a second look.">possibly unrelated to search</span>{{end}}
      {{with index $.VerifiedAt $label}}<span class="flag flag-cached" title="This entity was reused from --cache-ttl's on-disk cache rather than fetched live this run -- everything else in this report reflects the live state as of the report's own generated-at time above, but this one entity's data is only as fresh as its own cache timestamp.">(cached, verified {{.}})</span>{{end}}
      <span class="weight-tag {{weightClass .TotalWeight}}">+{{.TotalWeight}}</span>
    </div>
    {{$likelyDups := dupsWithConfidence $dups "likely"}}
    {{$possibleDups := dupsWithConfidence $dups "possible"}}
    {{if $likelyDups}}<div class="field dup-note dup-likely">Likely the same entity as: {{range $i, $d := $likelyDups}}{{if $i}}, {{end}}{{$d}}{{end}} &mdash; same normalized name AND a shared address or person; verify before treating as one, sources are shown separately on purpose.</div>{{end}}
    {{if $possibleDups}}<div class="field dup-note">Possibly the same entity as: {{range $i, $d := $possibleDups}}{{if $i}}, {{end}}{{$d}}{{end}} &mdash; verify before treating as one; sources are shown separately on purpose.</div>{{end}}
    {{range .Indicators}}
    {{if .Reviewed}}
    <details class="indicator indicator-reviewed {{weightClass .Weight}}">
      <summary><span class="weight-tag {{weightClass .Weight}}">+{{.Weight}}</span> <span class="reviewed-tag" title="Marked reviewed via --reviewed-file: you've already looked at this one. It still counts toward the score in full -- unlike --exclude, which removes an indicator from the score entirely -- it's just collapsed here so it stops competing for attention on every re-run.">reviewed</span> {{.Description}}</summary>
      {{$others := otherEntities . $label}}
      {{if $others}}<div class="field">Also linked to: {{range $i, $e := $others}}{{if $i}}; {{end}}{{$e}}{{end}}</div>{{end}}
      <div class="field">Evidence: {{.Evidence}}</div>
    </details>
    {{else}}
    <div class="indicator {{weightClass .Weight}}">
      <span class="weight-tag {{weightClass .Weight}}">+{{.Weight}}</span>
      <div class="desc">{{.Description}}</div>
      {{$others := otherEntities . $label}}
      {{if $others}}<div class="field">Also linked to: {{range $i, $e := $others}}{{if $i}}; {{end}}{{$e}}{{end}}</div>{{end}}
      <div class="field">Evidence: {{.Evidence}}</div>
    </div>
    {{end}}
    {{end}}
  </div>
  {{end}}
</details>
{{end}}
<p class="meta entity-filter-empty" id="entity-filter-empty" style="display:none">No entities match that filter.</p>
{{else}}
<p class="meta">No structural indicators found among the entities located.</p>
{{end}}

{{if .People}}
<h2>Findings by person</h2>
<p class="meta">Officers, directors, or trustees named on 2 or more entities -- the same cross-entity trace this report's shared_person indicator already flags, laid out per person instead of per pair.</p>
{{range .People}}
<div class="person-card">
  <div class="person-name">{{.Name}}</div>
  <div class="field">Linked to {{len .Entities}} entities: {{range $i, $e := .Entities}}{{if $i}}; {{end}}{{$e}}{{end}}</div>
</div>
{{end}}
{{end}}

{{if .Timeline}}
<h2>Timeline</h2>
<p class="meta">Every indicator above that carries a specific date, in chronological order &mdash; the one axis the by-entity and by-person views can't show. Nothing here is a new finding: each entry appears above too, just reordered by when it happened. Deliberately partial &mdash; only some indicator types carry a date at all today, and an indicator without one is left out entirely rather than shown with a guessed date, so absence from this list says nothing about an indicator's importance.</p>
{{range .Timeline}}
<div class="timeline-entry {{weightClass .Weight}}">
  <div class="timeline-head">
    <span class="timeline-date">{{.Date}}</span>
    <span class="weight-tag {{weightClass .Weight}}">+{{.Weight}}</span>
    <span class="timeline-code">{{.Code}}</span>
  </div>
  {{if .Entity}}<div class="field">{{.Entity}}</div>{{end}}
  <div class="field">Evidence: {{.Evidence}}</div>
</div>
{{end}}
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
{{end}}`
