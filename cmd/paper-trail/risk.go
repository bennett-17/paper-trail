package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/edgar"
	"github.com/bennett-17/paper-trail/internal/graph"
	"github.com/bennett-17/paper-trail/internal/risk"
	"github.com/bennett-17/paper-trail/internal/riskcache"
)

// mailDropAddressThreshold is how many companies register-wide must
// share a postcode before it's flagged as a likely company-formation-
// agent mail-drop address, not a genuine operating address. Chosen
// from live observation: ordinary single-business UK postcodes ran
// 5-70 companies (with one small-business address at 637, likely a
// shared office park), while a known mail-drop address ran ~190,000 --
// 1,000 sits comfortably above any genuine single-business address
// observed and well below actual mail-drop scale.
const mailDropAddressThreshold = 1000

// fewTrusteesThreshold is how few trustees a charity can have before
// few_trustees fires. UK charity governance guidance (the Charity
// Commission's own CC3 guidance) generally recommends a minimum of
// three trustees so no single person can unilaterally control
// decisions or funds -- two or fewer is the threshold used here.
const fewTrusteesThreshold = 2

// pscChainMaxDepth bounds how many corporate-PSC hops followPSCChain
// will follow beyond the root company, both to keep the extra API
// calls bounded and to guard against an ownership cycle (not observed
// live, but cheap to guard against regardless).
const pscChainMaxDepth = 3

// highOfficerCompensationRatio and highOfficerCompensationMinExpenses
// together gate the high_officer_compensation indicator: total
// compensation to current officers/directors/trustees/key employees
// exceeding this share of total functional expenses, but only above a
// minimum expense floor. Chosen from live observation against several
// real large nonprofits (Wikimedia Foundation, MSF USA), which ran
// 0.2%-2.9% -- well below this threshold -- while a small or all-
// volunteer organization can legitimately run much higher on a tiny
// budget (a single paid founder can be 100% of a $50k budget), which
// is what the expense floor is for.
const (
	highOfficerCompensationRatio       = 0.30
	highOfficerCompensationMinExpenses = 1_000_000
)

// shellCompanyAssetThreshold is how low an EDGAR filer's total assets
// can go before shell_company_assets fires. Chosen from live
// observation: Vanjia Corporation, a company that explicitly discloses
// itself as a shell on its own 10-K cover page, reported $63k-$72k in
// total assets across its recent filings; Processa Pharmaceuticals, a
// genuine pre-revenue clinical-stage biotech (so a company that could
// otherwise look "shell-like" on revenue alone), reported $4.5M-$7.8M
// -- comfortably two orders of magnitude above. This only catches
// nominal-assets shells, not every kind: a pre-merger SPAC sitting on
// a large trust account is a textbook shell with substantial reported
// assets, a different pattern entirely.
const shellCompanyAssetThreshold = 150_000

// riskReportJSON is the shape of a risk --json report -- named (not
// anonymous) so --diff can decode a previously saved one back in for
// comparison against a new run.
type riskReportJSON struct {
	Queries  []string      `json:"queries"`
	Entities []risk.Entity `json:"entities"`
	Notes    []string      `json:"notes"`
	Score    risk.Score    `json:"score"`
	// HiddenIndicators is how many indicators --top, --min-weight, or
	// --indicator left out of Score.Indicators -- Score.Total still
	// reflects all of them. Zero (the default, omitted) unless one of
	// those flags was set and actually filtered something out.
	HiddenIndicators int `json:"hiddenIndicators,omitempty"`
	// ExcludedIndicators is how many indicators --exclude/--exclude-file
	// permanently removed -- unlike HiddenIndicators above, these are
	// NOT reflected in Score.Total/Score.Confidence, since --exclude
	// means "not a real finding", not just "don't show it". Zero (the
	// default, omitted) unless one of those flags actually removed
	// something.
	ExcludedIndicators int `json:"excludedIndicators,omitempty"`
	// HiddenCorroborations is how many corroborated pairs
	// --min-corroboration left out of Score.Corroborations -- a
	// separate count from HiddenIndicators since it's a different
	// collection. Zero (the default, omitted) unless --min-corroboration
	// was set and actually filtered something out.
	HiddenCorroborations int `json:"hiddenCorroborations,omitempty"`
}

// riskReportDiff summarizes what changed between a previous risk
// --json report and a new run: entities that weren't in the old
// report, indicators that weren't in the old report, and the plain
// score delta. Comparison is by Label()/indicator identity, not a
// byte-for-byte diff -- an indicator's Entities/Evidence together
// with its Code is treated as its identity, since two different
// indicators can share a Code (e.g. two separate shared_address
// matches) but never share all three.
type riskReportDiff struct {
	NewEntities   []risk.Entity    `json:"newEntities"`
	NewIndicators []risk.Indicator `json:"newIndicators"`
	ScoreBefore   int              `json:"scoreBefore"`
	ScoreAfter    int              `json:"scoreAfter"`
}

func indicatorIdentity(ind risk.Indicator) string {
	return ind.Code + "|" + strings.Join(ind.Entities, ";") + "|" + ind.Evidence
}

// truncateIndicators implements --top: limits score.Indicators to the
// top highest-weight ones (already sorted that way by risk.Assess),
// returning the truncated score and how many were left out. Total and
// Confidence are untouched -- they still reflect every indicator
// found, only which ones are *shown* is limited. top <= 0 means
// "show all" (the default) and is a no-op.
func truncateIndicators(score risk.Score, top int) (risk.Score, int) {
	if top <= 0 || len(score.Indicators) <= top {
		return score, 0
	}
	hidden := len(score.Indicators) - top
	score.Indicators = score.Indicators[:top]
	return score, hidden
}

// filterIndicators implements --min-weight and --indicator: keeps only
// indicators whose weight is >= minWeight AND (codes is empty OR the
// indicator's Code is one of codes), returning the filtered score and
// how many indicators were removed. minWeight <= 0 and an empty codes
// is a no-op (the default -- show all). Like --top, this only limits
// what's *shown*: Total and Confidence are computed before filtering
// and are left untouched, so they still reflect every indicator found.
func filterIndicators(score risk.Score, minWeight int, codes []string) (risk.Score, int) {
	if minWeight <= 0 && len(codes) == 0 {
		return score, 0
	}
	allowedCode := make(map[string]bool, len(codes))
	for _, c := range codes {
		allowedCode[c] = true
	}
	kept := make([]risk.Indicator, 0, len(score.Indicators))
	for _, ind := range score.Indicators {
		if ind.Weight < minWeight {
			continue
		}
		if len(codes) > 0 && !allowedCode[ind.Code] {
			continue
		}
		kept = append(kept, ind)
	}
	hidden := len(score.Indicators) - len(kept)
	score.Indicators = kept
	return score, hidden
}

// filterCorroborations implements --min-corroboration: keeps only
// corroborated pairs matched on at least minCorroboration distinct
// indicator codes, returning the filtered score and how many were
// removed. minCorroboration <= 0 is a no-op (show all, the default).
// Like filterIndicators/truncateIndicators, this only limits what's
// *shown* -- Corroborations never contributed to Total in the first
// place (see the Corroboration doc comment), so there's nothing to
// recompute the way --exclude has to.
func filterCorroborations(score risk.Score, minCorroboration int) (risk.Score, int) {
	if minCorroboration <= 0 {
		return score, 0
	}
	kept := make([]risk.Corroboration, 0, len(score.Corroborations))
	for _, c := range score.Corroborations {
		if len(c.Codes) >= minCorroboration {
			kept = append(kept, c)
		}
	}
	hidden := len(score.Corroborations) - len(kept)
	score.Corroborations = kept
	return score, hidden
}

// parseIndicatorCodes splits a comma-separated --indicator flag value
// into individual codes, trimming whitespace and dropping empty
// entries (so a trailing comma or extra spaces don't produce a bogus
// empty-string code that could never match).
func parseIndicatorCodes(flagValue string) []string {
	if flagValue == "" {
		return nil
	}
	var codes []string
	for _, c := range strings.Split(flagValue, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			codes = append(codes, c)
		}
	}
	return codes
}

// excludeIndicators implements --exclude/--exclude-file: permanently
// removes indicators whose Evidence or any Entities label contains
// (case-insensitively) any of the given terms, returning the filtered
// score and how many were removed. Unlike filterIndicators/
// truncateIndicators (--min-weight/--indicator/--top), which only
// limit what's *shown*, this means "I've already reviewed this and
// it's not a real finding" -- so Total, Confidence, and Corroborations
// are all recomputed from what's left, not left reflecting indicators
// that no longer count. An empty terms is a no-op (the default).
func excludeIndicators(score risk.Score, terms []string) (risk.Score, int) {
	if len(terms) == 0 {
		return score, 0
	}
	lowerTerms := make([]string, len(terms))
	for i, t := range terms {
		lowerTerms[i] = strings.ToLower(t)
	}
	matches := func(ind risk.Indicator) bool {
		haystack := strings.ToLower(ind.Evidence + " " + strings.Join(ind.Entities, " "))
		for _, t := range lowerTerms {
			if strings.Contains(haystack, t) {
				return true
			}
		}
		return false
	}
	kept := make([]risk.Indicator, 0, len(score.Indicators))
	for _, ind := range score.Indicators {
		if !matches(ind) {
			kept = append(kept, ind)
		}
	}
	excluded := len(score.Indicators) - len(kept)
	if excluded == 0 {
		return score, 0
	}
	total := 0
	for _, ind := range kept {
		total += ind.Weight
	}
	corroborations := risk.ComputeCorroborations(kept)
	score.Indicators = kept
	score.Total = total
	score.Corroborations = corroborations
	score.Confidence, score.ConfidenceReason = risk.ConfidenceBand(kept, corroborations, total)
	return score, excluded
}

// parseExcludeTerms combines --exclude's comma-separated value with
// --exclude-file's one-term-per-line file (blank lines and #-prefixed
// comments ignored, same format as --input-file), so a long-lived
// allowlist doesn't have to be retyped as a single flag value every
// run.
func parseExcludeTerms(flagValue, filePath string) ([]string, error) {
	terms := parseIndicatorCodes(flagValue) // same comma-split/trim/drop-empty logic, generically reused
	if filePath != "" {
		fromFile, err := readQueryTermsFile(filePath)
		if err != nil {
			return nil, err
		}
		terms = append(terms, fromFile...)
	}
	return terms, nil
}

// confidenceBandRank orders risk.Score's confidence bands (LOW <
// MEDIUM < HIGH) for --fail-on comparison. Case-insensitive lookup --
// callers normalize with strings.ToUpper first.
var confidenceBandRank = map[string]int{
	"LOW":    1,
	"MEDIUM": 2,
	"HIGH":   3,
}

// validateFailOn checks a --fail-on value up front (before any live
// source is queried), matching the fail-fast treatment --diff and
// --exclude-file already get for their own inputs.
func validateFailOn(value string) error {
	if value == "" {
		return nil
	}
	if _, ok := confidenceBandRank[strings.ToUpper(value)]; !ok {
		return fmt.Errorf("must be LOW, MEDIUM, or HIGH, got %q", value)
	}
	return nil
}

// shouldFailOn reports whether confidence meets or exceeds the
// --fail-on threshold. threshold == "" (the default) never fails.
// Assumes threshold was already validated by validateFailOn -- an
// unrecognized confidence value (shouldn't happen; risk.Assess only
// ever produces LOW/MEDIUM/HIGH) is treated as rank 0, i.e. never
// triggers a failure on its own.
func shouldFailOn(confidence, threshold string) bool {
	if threshold == "" {
		return false
	}
	return confidenceBandRank[strings.ToUpper(confidence)] >= confidenceBandRank[strings.ToUpper(threshold)]
}

// diffRiskReports compares a freshly computed report against a
// previously saved one (see --diff).
func diffRiskReports(previous riskReportJSON, entities []risk.Entity, score risk.Score) riskReportDiff {
	seenEntities := map[string]bool{}
	for _, e := range previous.Entities {
		seenEntities[e.Label()] = true
	}
	var newEntities []risk.Entity
	for _, e := range entities {
		if !seenEntities[e.Label()] {
			newEntities = append(newEntities, e)
		}
	}

	seenIndicators := map[string]bool{}
	for _, ind := range previous.Score.Indicators {
		seenIndicators[indicatorIdentity(ind)] = true
	}
	var newIndicators []risk.Indicator
	for _, ind := range score.Indicators {
		if !seenIndicators[indicatorIdentity(ind)] {
			newIndicators = append(newIndicators, ind)
		}
	}

	return riskReportDiff{
		NewEntities:   newEntities,
		NewIndicators: newIndicators,
		ScoreBefore:   previous.Score.Total,
		ScoreAfter:    score.Total,
	}
}

// progressReporter writes short "[+12.3s] source: message" progress
// lines to stderr as a long risk scan runs -- never to stdout/
// --output, so it can never corrupt a --json report or a file being
// written. Safe for concurrent use across every phase 1/2 goroutine
// (mutex-protected, since multiple sources report at once once
// runRisk's queries are parallelized). A nil *progressReporter is a
// deliberate no-op (see report below), so every call site can call
// progress.report(...) unconditionally -- no "if progress != nil"
// scattered through every gather/screen function -- and --quiet is
// implemented simply by never constructing one.
type progressReporter struct {
	mu    sync.Mutex
	w     io.Writer
	start time.Time
}

func newProgressReporter(w io.Writer) *progressReporter {
	return &progressReporter{w: w, start: time.Now()}
}

func (p *progressReporter) report(source, format string, a ...any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "[+%5.1fs] %s: %s\n", time.Since(p.start).Seconds(), source, fmt.Sprintf(format, a...))
}

// riskSummaryJSON is --summary --json's compact alternative to the
// full riskReportJSON -- just the headline numbers, for scripting/
// dashboards where the full indicator-by-indicator report is too
// verbose.
type riskSummaryJSON struct {
	Queries            []string        `json:"queries"`
	Total              int             `json:"total"`
	Confidence         string          `json:"confidence"`
	ConfidenceReason   string          `json:"confidenceReason"`
	EntityCount        int             `json:"entityCount"`
	IndicatorCount     int             `json:"indicatorCount"`
	HiddenIndicators   int             `json:"hiddenIndicators,omitempty"`
	ExcludedIndicators int             `json:"excludedIndicators,omitempty"`
	Diff               *riskReportDiff `json:"diff,omitempty"`
}

// writeSummary implements --summary: a compact one-line (text) or
// single small object (--json) alternative to the full report.
func writeSummary(w io.Writer, report riskReportJSON, diff *riskReportDiff, asJSON, colorOn bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(riskSummaryJSON{
			Queries:            report.Queries,
			Total:              report.Score.Total,
			Confidence:         report.Score.Confidence,
			ConfidenceReason:   report.Score.ConfidenceReason,
			EntityCount:        len(report.Entities),
			IndicatorCount:     len(report.Score.Indicators),
			HiddenIndicators:   report.HiddenIndicators,
			ExcludedIndicators: report.ExcludedIndicators,
			Diff:               diff,
		})
		return
	}

	coloredConfidence := colorize(report.Score.Confidence, confidenceColor(report.Score.Confidence), colorOn)
	line := fmt.Sprintf("Score: %d (%s -- %s) -- %d indicator(s), %d entit(ies)", report.Score.Total, coloredConfidence, report.Score.ConfidenceReason, len(report.Score.Indicators), len(report.Entities))
	var extras []string
	if report.HiddenIndicators > 0 {
		extras = append(extras, fmt.Sprintf("%d hidden", report.HiddenIndicators))
	}
	if report.ExcludedIndicators > 0 {
		extras = append(extras, fmt.Sprintf("%d excluded", report.ExcludedIndicators))
	}
	if diff != nil {
		extras = append(extras, fmt.Sprintf("vs baseline: %d->%d, %d new indicator(s)", diff.ScoreBefore, diff.ScoreAfter, len(diff.NewIndicators)))
	}
	if len(extras) > 0 {
		line += " (" + strings.Join(extras, "; ") + ")"
	}
	fmt.Fprintln(w, line)
}

// summaryFromReport builds a riskSummaryJSON from a full report --
// used by --webhook so it works whether or not --summary was also
// passed (the two are independent flags).
func summaryFromReport(report riskReportJSON) riskSummaryJSON {
	return riskSummaryJSON{
		Queries:            report.Queries,
		Total:              report.Score.Total,
		Confidence:         report.Score.Confidence,
		ConfidenceReason:   report.Score.ConfidenceReason,
		EntityCount:        len(report.Entities),
		IndicatorCount:     len(report.Score.Indicators),
		HiddenIndicators:   report.HiddenIndicators,
		ExcludedIndicators: report.ExcludedIndicators,
	}
}

// webhookMessage renders a compact, human-readable one-line message
// for --webhook -- the same information as --summary's text line.
func webhookMessage(s riskSummaryJSON) string {
	return fmt.Sprintf("paper-trail risk alert: score %d (%s -- %s) -- %d indicator(s), %d entit(ies). Queries: %s",
		s.Total, s.Confidence, s.ConfidenceReason, s.IndicatorCount, s.EntityCount, strings.Join(s.Queries, ", "))
}

// sendWebhookAlert posts a --fail-on alert to url. A hooks.slack.com or
// discord.com/api/webhooks (or discordapp.com/api/webhooks) URL gets
// that platform's own minimal payload shape (confirmed live against
// each platform's current docs: Slack wants {"text": ...}, Discord
// wants {"content": ...}); anything else gets the full riskSummaryJSON
// as-is, for a custom integration to parse. A 10s timeout and no
// retries -- this is a best-effort notification on top of --fail-on's
// own exit code, not something worth blocking or retrying a finished
// scan over.
func sendWebhookAlert(url string, summary riskSummaryJSON) error {
	message := webhookMessage(summary)

	var payload any
	switch {
	case strings.Contains(url, "hooks.slack.com"):
		payload = map[string]string{"text": message}
	case strings.Contains(url, "discord.com/api/webhooks"), strings.Contains(url, "discordapp.com/api/webhooks"):
		payload = map[string]string{"content": message}
	default:
		payload = summary
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("building webhook payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

const (
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiReset  = "\x1b[0m"
)

// colorDisabledByFlagOrEnv reports whether --no-color or the NO_COLOR
// env var (https://no-color.org -- any non-empty value disables,
// regardless of its content) rules out color, independent of whether
// the output is actually a terminal. Split out from colorEnabled below
// so this half is unit-testable without needing a real terminal to
// exercise the flag/env-var logic specifically.
func colorDisabledByFlagOrEnv(noColorFlag bool) bool {
	return noColorFlag || os.Getenv("NO_COLOR") != ""
}

// colorEnabled decides whether the text report should emit ANSI color:
// disabled by an explicit --no-color, by the NO_COLOR env var, or when
// w isn't an interactive terminal (redirected to a file, piped to
// another program, or a real file opened via --output) -- escape
// codes in a file or another program's input are noise, not
// information, the same reasoning --quiet already applies to progress
// output.
func colorEnabled(w io.Writer, noColorFlag bool) bool {
	if colorDisabledByFlagOrEnv(noColorFlag) {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// colorize wraps s in ansiCode/ansiReset when enabled is true,
// otherwise returns s unchanged.
func colorize(s, ansiCode string, enabled bool) string {
	if !enabled {
		return s
	}
	return ansiCode + s + ansiReset
}

// confidenceColor maps a confidence band to the same red/yellow/green
// scale a reader would expect from any traffic-light-style status.
func confidenceColor(band string) string {
	switch band {
	case "HIGH":
		return ansiRed
	case "MEDIUM":
		return ansiYellow
	default:
		return ansiGreen
	}
}

// weightColor uses the same weight thresholds internal/risk's own
// confidenceBand does (5+ high, 3+ moderate), so an indicator's color
// in the report matches the same scale that produced the confidence
// band above it.
func weightColor(weight int) string {
	switch {
	case weight >= 5:
		return ansiRed
	case weight >= 3:
		return ansiYellow
	default:
		return ansiGreen
	}
}

// configFilePath returns the default config file location
// (~/.paper-trailrc), or an error if the home directory can't be
// determined.
func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".paper-trailrc"), nil
}

// parseConfigFileLines parses "key = value" pairs out of a config
// file's contents, one per line (blank lines and #-prefixed comments
// ignored, same format as --input-file/--exclude-file). Returns the
// parsed pairs plus a warning for each malformed line (missing "="),
// rather than failing the whole file over one bad line.
func parseConfigFileLines(data string) (values map[string]string, warnings []string) {
	values = map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			warnings = append(warnings, fmt.Sprintf("ignoring malformed line %q (want key = value)", line))
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values, warnings
}

// applyConfigFileDefaults reads path (a missing file is not an error --
// a config file is optional) and, for every "key = value" pair found,
// sets that flag in fs to value UNLESS the user explicitly passed that
// flag on the command line (explicitlySet) -- an explicit CLI flag
// always wins over the config file. Returns warnings for a malformed
// line, a key that doesn't name a real flag, or a value the flag
// itself rejects; the caller decides how to report them, rather than
// any of this being treated as fatal.
func applyConfigFileDefaults(fs *flag.FlagSet, explicitlySet map[string]bool, path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	values, warnings := parseConfigFileLines(string(data))
	for key, value := range values {
		if explicitlySet[key] {
			continue
		}
		f := fs.Lookup(key)
		if f == nil {
			warnings = append(warnings, fmt.Sprintf("%q is not a recognized flag, ignoring", key))
			continue
		}
		if err := fs.Set(key, value); err != nil {
			warnings = append(warnings, fmt.Sprintf("%q: %v", key, err))
		}
	}
	return warnings
}

// runRisk queries every configured source for candidates matching
// query, normalizes whatever address/officer data each source exposes
// into risk.Entity values, and runs the structural heuristics over the
// combined set. Every source is best-effort: a missing credential or a
// failed/empty lookup is recorded as a note and skipped, never fatal --
// a partial report across whichever sources are configured is more
// useful than an all-or-nothing failure.
func runRisk(args []string) {
	fs := flag.NewFlagSet("risk", flag.ExitOnError)
	limit := fs.Int("limit", 5, "max candidates to pull per source, per query term")
	asJSON := fs.Bool("json", false, "print raw JSON")
	output := fs.String("output", "", "write results to this file instead of stdout")
	graphPath := fs.String("graph", "", "additionally write a node/edge graph JSON (entities as nodes, indicators as edges) to this path")
	htmlPath := fs.String("html", "", "additionally write a self-contained, offline-viewable HTML graph (same nodes/edges as --graph) to this path")
	csvPath := fs.String("graph-csv", "", "additionally write the graph (same nodes/edges as --graph) as a denormalized edge-list CSV, for spreadsheets or import into Gephi/yEd")
	entitiesCSVPath := fs.String("entities-csv", "", "additionally write a flat CSV of every entity found (source, id, name, addresses, people, phones, emails, websites, chargees, beneficial owners) to this path -- unlike --graph-csv, this is the entity list itself, not the indicator relationships between them")
	graphMLPath := fs.String("graph-graphml", "", "additionally write the graph (same nodes/edges as --graph) as GraphML, for import into Gephi/yEd or other graph-analysis tools")
	cacheTTLFlag := fs.String("cache-ttl", "", "cache entities per source/query/limit on disk for this long (e.g. 24h) and reuse them within that window instead of re-fetching; unset disables caching entirely (always live, the default)")
	inputFile := fs.String("input-file", "", "read additional query terms from this file, one per line (blank lines and lines starting with # are ignored) -- combined with any <query> arguments given directly; pass - to read from stdin instead of a file")
	diffPath := fs.String("diff", "", "compare this run against a previously saved --output --json report, showing newly appeared entities/indicators and the score change")
	quiet := fs.Bool("quiet", false, "suppress progress output (written to stderr as the scan runs; never affects --json or --output)")
	noColor := fs.Bool("no-color", false, "disable ANSI color in the text report, even on a terminal -- color is already auto-disabled when the NO_COLOR env var is set or output isn't a terminal (redirected to a file or another program)")
	top := fs.Int("top", 0, "show only the N highest-weight indicators (0 shows all, the default) -- Total still reflects every indicator found; only which ones are listed is limited, for a large scan's report to lead with what matters most without scrolling a long flat list")
	minWeight := fs.Int("min-weight", 0, "show only indicators with weight >= this (0 shows all, the default) -- Total still reflects every indicator found")
	indicatorFilter := fs.String("indicator", "", "show only indicators matching these comma-separated codes, e.g. disqualified_director,sanctions_match (empty shows all, the default) -- Total still reflects every indicator found")
	minCorroboration := fs.Int("min-corroboration", 0, "show only corroborated pairs matched on at least this many distinct indicator codes (0 shows all, the default) -- Corroborations never contribute to Total, so this only limits which corroborated pairs are listed")
	excludeFlag := fs.String("exclude", "", "comma-separated terms -- any indicator whose evidence or entity labels contain one of these (case-insensitive) is permanently removed from the report, including Total/Confidence, not just hidden from display -- for dismissing leads you've already reviewed and cleared")
	excludeFile := fs.String("exclude-file", "", "read additional --exclude terms from this file too, one per line (blank lines and lines starting with # are ignored)")
	failOn := fs.String("fail-on", "", "exit with a non-zero status if the final confidence band reaches this level or higher (LOW, MEDIUM, or HIGH) -- lets a scan act as a gate in CI/cron/pre-merge automation instead of requiring someone to read the output")
	summary := fs.Bool("summary", false, "print (or, with --json, encode) a compact one-line/one-object summary -- score, confidence, and entity/indicator counts -- instead of the full report, for scripting/dashboards/monitoring where the full indicator-by-indicator report is too verbose")
	webhookURL := fs.String("webhook", "", "POST a JSON alert to this URL when --fail-on's threshold is met (requires --fail-on) -- a hooks.slack.com or discord.com/api/webhooks URL gets that platform's own minimal message format, anything else gets the full compact summary as JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	// Config file defaults apply only to flags the user didn't
	// explicitly pass -- fs.Visit (unlike fs.VisitAll) only calls back
	// for flags actually set on the command line, so this has to run
	// after Parse above, not before.
	explicitlySet := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicitlySet[f.Name] = true })
	if path, err := configFilePath(); err == nil {
		for _, w := range applyConfigFileDefaults(fs, explicitlySet, path) {
			fmt.Fprintf(os.Stderr, "Warning: %s: %s\n", path, w)
		}
	}

	const usage = "usage: paper-trail risk [<query> ...] [--input-file <path>] [--limit <n>] [--output <path>] [--graph <path>] [--html <path>] [--graph-csv <path>] [--entities-csv <path>] [--graph-graphml <path>] [--cache-ttl <duration>] [--diff <path>] [--top <n>] [--min-weight <n>] [--indicator <codes>] [--min-corroboration <n>] [--exclude <terms>] [--exclude-file <path>] [--fail-on <band>] [--webhook <url>] [--summary] [--no-color] [--quiet] [--json]"
	queries := positional
	if *inputFile != "" {
		fromFile, err := readQueryTermsFile(*inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: --input-file %q: %v\n", *inputFile, err)
			os.Exit(1)
		}
		queries = append(queries, fromFile...)
	}
	if len(queries) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	// Loaded and validated up front (before any live source is
	// queried) so a bad --diff path fails fast instead of after every
	// API call has already run.
	var previousReport *riskReportJSON
	if *diffPath != "" {
		data, err := os.ReadFile(*diffPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: --diff %q: %v\n", *diffPath, err)
			os.Exit(1)
		}
		var r riskReportJSON
		if err := json.Unmarshal(data, &r); err != nil {
			fmt.Fprintf(os.Stderr, "Error: --diff %q is not a valid risk --json report: %v\n", *diffPath, err)
			os.Exit(1)
		}
		previousReport = &r
	}

	excludeTerms, err := parseExcludeTerms(*excludeFlag, *excludeFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: --exclude-file %q: %v\n", *excludeFile, err)
		os.Exit(1)
	}

	if err := validateFailOn(*failOn); err != nil {
		fmt.Fprintf(os.Stderr, "Error: --fail-on %v\n", err)
		os.Exit(1)
	}
	if *webhookURL != "" && *failOn == "" {
		fmt.Fprintln(os.Stderr, "Error: --webhook requires --fail-on to be set too -- otherwise there's no threshold to alert on")
		os.Exit(1)
	}

	// Caching is opt-in: this tool's whole point is checking *current*
	// public registry state, so silently reusing stale data by default
	// would work against that. cacheTTL stays zero (disabled) unless
	// --cache-ttl was set; Get/Set on a Cache with an empty Dir are
	// already no-ops, but skipping cache.New() entirely when it's not
	// wanted avoids even trying to touch the OS cache directory.
	cache := &riskcache.Cache{}
	var cacheTTL time.Duration
	if *cacheTTLFlag != "" {
		d, err := time.ParseDuration(*cacheTTLFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: --cache-ttl %q: %v\n", *cacheTTLFlag, err)
			os.Exit(1)
		}
		cacheTTL = d
		cache = riskcache.New()
	}

	var progress *progressReporter
	if !*quiet {
		progress = newProgressReporter(os.Stderr)
	}

	var entities []risk.Entity
	var extra []risk.Indicator
	var notes []string
	note := func(source, format string, a ...any) {
		notes = append(notes, fmt.Sprintf("%s: %s", source, fmt.Sprintf(format, a...)))
	}

	// SEC EDGAR -- one client shared across every query term (and
	// reused below for the full-text mentions step), so a missing
	// credential is reported once, not once per term.
	var edgarClient *edgar.Client
	if c, err := edgar.NewClient(""); err != nil {
		note("SEC EDGAR", "skipped (%v)", err)
	} else {
		edgarClient = c
	}

	// UK Companies House -- one client shared across every charity
	// below, so a missing credential is reported once, not once per
	// charity. Adds real director data for UK charities that are also
	// registered companies, alongside their Charity Commission
	// trustees: ukcharity alone only exposes trustees, so a company's
	// directors would otherwise be invisible to the shared_person check.
	chClient, chErr := companieshouse.NewClient("")
	if chErr != nil {
		note("Companies House", "skipped (%v)", chErr)
		chClient = nil
	}

	// Phase 1: every source below resolves query terms into entities
	// independently of the others -- EDGAR, IRS Form 990, ACNC, and UK
	// Charity Commission (with its nested Companies House lookups) each
	// hit entirely separate APIs with their own client-level throttling,
	// so running them concurrently is safe and cuts wall-clock time
	// substantially on a large multi-term scan (confirmed live: a
	// 25-term run that previously needed several minutes sequential).
	// Each gathers into its own local slices, not the shared ones above,
	// so there's nothing to protect with a mutex -- they're merged in a
	// fixed order below, after every goroutine finishes, so output stays
	// deterministic regardless of which source happens to finish first.
	var edgarEntities, npEntities, acncEntities, ukEntities []risk.Entity
	var edgarExtra, npExtra, ukExtra []risk.Indicator
	var edgarNotes, npNotes, acncNotes, ukNotes []string
	var wg sync.WaitGroup

	if edgarClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			edgarEntities, edgarExtra, edgarNotes = gatherEDGAREntities(edgarClient, queries, *limit, cache, cacheTTL, progress)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		npEntities, npExtra, npNotes = gatherNonprofitEntities(queries, *limit, cache, cacheTTL, progress)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		acncEntities, acncNotes = gatherACNCEntities(queries, *limit, cache, cacheTTL, progress)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ukEntities, ukExtra, ukNotes = gatherUKCharityEntities(chClient, queries, *limit, cache, cacheTTL, progress)
	}()
	wg.Wait()

	entities = append(entities, edgarEntities...)
	entities = append(entities, npEntities...)
	entities = append(entities, acncEntities...)
	entities = append(entities, ukEntities...)
	extra = append(extra, edgarExtra...)
	extra = append(extra, npExtra...)
	extra = append(extra, ukExtra...)
	notes = append(notes, edgarNotes...)
	notes = append(notes, npNotes...)
	notes = append(notes, acncNotes...)
	notes = append(notes, ukNotes...)

	// Phase 2: every check below only reads the now-final entities pool
	// (built above) -- it doesn't add to it -- so, like phase 1, these
	// five are independent of each other and safe to run concurrently.
	// US sanctions, UK sanctions, ICIJ Offshore Leaks, and the
	// disqualified-directors check each screen every query term plus
	// every distinct person name found; EDGAR full-text mentions
	// screens query terms only (see its own comment below for why).
	// Merged in the same fixed order as before so output stays
	// deterministic.
	var usExtra, ukSanctionsExtra, dqExtra, ftExtra, icijExtra []risk.Indicator
	var usNotes, ukSanctionsNotes, dqNotes, ftNotes, icijNotes []string
	var wg2 sync.WaitGroup

	wg2.Add(1)
	go func() {
		defer wg2.Done()
		usExtra, usNotes = screenUSSanctions(queries, entities, progress)
	}()
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		ukSanctionsExtra, ukSanctionsNotes = screenUKSanctions(queries, entities, progress)
	}()
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		dqExtra, dqNotes = screenDisqualifiedDirectors(chClient, entities, progress)
	}()
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		ftExtra, ftNotes = screenEDGARFullTextMentions(edgarClient, queries, entities, *limit, progress)
	}()
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		icijExtra, icijNotes = screenICIJOffshoreLeaks(queries, entities, progress)
	}()
	wg2.Wait()

	extra = append(extra, usExtra...)
	extra = append(extra, ukSanctionsExtra...)
	extra = append(extra, dqExtra...)
	extra = append(extra, ftExtra...)
	extra = append(extra, icijExtra...)
	notes = append(notes, usNotes...)
	notes = append(notes, ukSanctionsNotes...)
	notes = append(notes, dqNotes...)
	notes = append(notes, ftNotes...)
	notes = append(notes, icijNotes...)

	// Cross-referencing runs once over the combined pool from every
	// query term -- this is the whole point of taking multiple terms:
	// an officer/trustee or address shared between, say, a "Narconon
	// UK" result and a "Criminon UK" result only surfaces if both are
	// in the same Assess() call.
	cache.Save() // no-op if --cache-ttl wasn't set

	score := risk.Assess(entities, extra)

	// --exclude/--exclude-file apply before everything else below,
	// including --diff: unlike --top/--min-weight/--indicator, which
	// only limit what's *shown*, an excluded indicator is treated as
	// not a real finding at all -- so it should never resurface as
	// "new" in a diff either, and Total/Confidence are recomputed to
	// no longer reflect it.
	score, excludedCount := excludeIndicators(score, excludeTerms)

	// --diff always compares the (post-exclude) full indicator set,
	// before any --top truncation below -- otherwise an indicator that
	// just fell outside --top's cutoff in an earlier run could
	// misleadingly look "new".
	var diff *riskReportDiff
	if previousReport != nil {
		d := diffRiskReports(*previousReport, entities, score)
		diff = &d
	}

	// --min-weight, --indicator, and --top all only limit which
	// indicators are *shown*, after diffing -- Total (and the confidence
	// band, already computed) still reflect every indicator found.
	// --min-weight/--indicator apply first (relevance), --top second
	// (count), so e.g. --indicator sanctions_match --top 3 means "the 3
	// highest-weight sanctions matches", not "of the top 3 overall,
	// whichever happen to be sanctions matches".
	score, hiddenByFilter := filterIndicators(score, *minWeight, parseIndicatorCodes(*indicatorFilter))
	score, hiddenByTop := truncateIndicators(score, *top)
	hiddenIndicators := hiddenByFilter + hiddenByTop
	score, hiddenCorroborations := filterCorroborations(score, *minCorroboration)

	report := riskReportJSON{Queries: queries, Entities: entities, Notes: notes, Score: score, HiddenIndicators: hiddenIndicators, ExcludedIndicators: excludedCount, HiddenCorroborations: hiddenCorroborations}

	var w io.Writer = os.Stdout
	if *output != "" {
		f, err := os.Create(*output)
		exitOnErr(err)
		defer f.Close()
		w = f
	}

	if *summary {
		writeSummary(w, report, diff, *asJSON, colorEnabled(w, *noColor))
	} else if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if diff != nil {
			enc.Encode(struct {
				riskReportJSON
				Diff *riskReportDiff `json:"diff"`
			}{report, diff})
		} else {
			enc.Encode(report)
		}
	} else {
		quoted := make([]string, len(queries))
		for i, q := range queries {
			quoted[i] = fmt.Sprintf("%q", q)
		}
		fmt.Fprintf(w, "Risk assessment for %s\n\n", strings.Join(quoted, ", "))
		fmt.Fprintf(w, "%d entit(ies) found:\n", len(entities))
		for _, e := range entities {
			fmt.Fprintf(w, "  %s\n", e.Label())
		}
		if len(notes) > 0 {
			fmt.Fprintln(w, "\nNotes:")
			for _, n := range notes {
				fmt.Fprintf(w, "  - %s\n", n)
			}
		}

		if excludedCount > 0 {
			fmt.Fprintf(w, "\n%d indicator(s) permanently excluded (--exclude/--exclude-file) -- not counted in the score below at all.\n", excludedCount)
		}
		colorOn := colorEnabled(w, *noColor)
		coloredConfidence := colorize(score.Confidence, confidenceColor(score.Confidence), colorOn)
		fmt.Fprintf(w, "\nRisk score: %d (confidence: %s -- %s)\n\n", score.Total, coloredConfidence, score.ConfidenceReason)
		if len(score.Indicators) == 0 {
			fmt.Fprintln(w, "No structural indicators found among the entities located.")
		}
		for _, ind := range score.Indicators {
			weightStr := colorize(fmt.Sprintf("+%d", ind.Weight), weightColor(ind.Weight), colorOn)
			fmt.Fprintf(w, "%s  %s\n", weightStr, ind.Description)
			fmt.Fprintf(w, "     Entities: %s\n", strings.Join(ind.Entities, "; "))
			fmt.Fprintf(w, "     Evidence: %s\n\n", ind.Evidence)
		}
		if hiddenIndicators > 0 {
			var reasons []string
			if hiddenByFilter > 0 {
				if *minWeight > 0 {
					reasons = append(reasons, fmt.Sprintf("--min-weight %d", *minWeight))
				}
				if *indicatorFilter != "" {
					reasons = append(reasons, fmt.Sprintf("--indicator %q", *indicatorFilter))
				}
			}
			if hiddenByTop > 0 {
				reasons = append(reasons, fmt.Sprintf("--top %d", *top))
			}
			fmt.Fprintf(w, "... and %d more indicator(s) not shown (%s) -- the score above still reflects all of them.\n\n", hiddenIndicators, strings.Join(reasons, ", "))
		}
		if len(score.Corroborations) > 0 {
			fmt.Fprintln(w, "Corroborated pairs (matched on 2+ independent kinds of evidence -- stronger than any single indicator above):")
			for _, c := range score.Corroborations {
				fmt.Fprintf(w, "  %s\n", strings.Join(c.Entities, "  <->  "))
				fmt.Fprintf(w, "    matched on: %s\n\n", strings.Join(c.Codes, ", "))
			}
		}
		if hiddenCorroborations > 0 {
			fmt.Fprintf(w, "... and %d more corroborated pair(s) not shown (--min-corroboration %d).\n\n", hiddenCorroborations, *minCorroboration)
		}
		fmt.Fprintln(w, "This is a lead-generation report, not a finding -- verify every indicator by hand before drawing any conclusion. It is not a determination of money laundering, tax evasion, terrorism financing, or any other wrongdoing.")

		if diff != nil {
			fmt.Fprintf(w, "\nDiff against %s:\n", *diffPath)
			fmt.Fprintf(w, "  Score: %d -> %d (%+d)\n", diff.ScoreBefore, diff.ScoreAfter, diff.ScoreAfter-diff.ScoreBefore)
			fmt.Fprintf(w, "  %d new entit(ies):\n", len(diff.NewEntities))
			for _, e := range diff.NewEntities {
				fmt.Fprintf(w, "    %s\n", e.Label())
			}
			fmt.Fprintf(w, "  %d new indicator(s):\n", len(diff.NewIndicators))
			for _, ind := range diff.NewIndicators {
				fmt.Fprintf(w, "    +%d  %s\n", ind.Weight, ind.Description)
				fmt.Fprintf(w, "         Entities: %s\n", strings.Join(ind.Entities, "; "))
				fmt.Fprintf(w, "         Evidence: %s\n", ind.Evidence)
			}
		}
	}

	if *output != "" {
		fmt.Printf("Wrote risk assessment (%d entities, score %d) to %s\n", len(entities), score.Total, *output)
	}

	if *entitiesCSVPath != "" {
		exitOnErr(risk.WriteEntitiesCSV(entities, *entitiesCSVPath))
		fmt.Printf("Wrote entity list CSV (%d entities) to %s\n", len(entities), *entitiesCSVPath)
	}

	if *graphPath != "" || *htmlPath != "" || *csvPath != "" || *graphMLPath != "" {
		g := graph.BuildFromRisk(entities, score)
		if *graphPath != "" {
			exitOnErr(graph.WriteJSON(g, *graphPath))
			fmt.Printf("Wrote graph (%d nodes, %d edges) to %s\n", len(g.Nodes), len(g.Edges), *graphPath)
		}
		if *htmlPath != "" {
			exitOnErr(graph.WriteHTML(g, *htmlPath))
			fmt.Printf("Wrote HTML graph viewer (%d nodes, %d edges) to %s -- open it directly in a browser\n", len(g.Nodes), len(g.Edges), *htmlPath)
		}
		if *csvPath != "" {
			exitOnErr(graph.WriteCSV(g, *csvPath))
			fmt.Printf("Wrote graph edge-list CSV (%d nodes, %d edges) to %s\n", len(g.Nodes), len(g.Edges), *csvPath)
		}
		if *graphMLPath != "" {
			exitOnErr(graph.WriteGraphML(g, *graphMLPath))
			fmt.Printf("Wrote GraphML (%d nodes, %d edges) to %s\n", len(g.Nodes), len(g.Edges), *graphMLPath)
		}
	}

	// Checked last, after every output/graph file has already been
	// written -- --fail-on signals failure via exit status, it doesn't
	// suppress the report itself, so a CI system that captures the
	// output artifact on failure still gets one.
	if shouldFailOn(score.Confidence, *failOn) {
		if *webhookURL != "" {
			if err := sendWebhookAlert(*webhookURL, summaryFromReport(report)); err != nil {
				// Deliberately non-fatal: --fail-on's exit code below
				// already communicates the failure state to whatever's
				// watching this process's exit status; a webhook
				// delivery problem shouldn't additionally obscure that
				// with a different failure mode.
				fmt.Fprintf(os.Stderr, "Warning: --webhook alert failed to send: %v\n", err)
			}
		}
		os.Exit(1)
	}
}

// The functions below each gather or screen for exactly one source,
// extracted out of runRisk so its phase 1 (entity gathering: EDGAR,
// IRS Form 990, ACNC, UK Charity Commission) and phase 2 (cross-checks
// against the now-final entity pool: US/UK sanctions, disqualified
// directors, EDGAR full-text mentions) can each run as four
// independent goroutines instead of eight fully sequential loops. Each
// function only touches its own local return values during execution
// -- nothing shared/mutable -- so there's no data race to guard
// against; runRisk merges every function's results in a fixed order
// once all of a phase's goroutines finish, so output stays
// deterministic regardless of which one happens to finish first.
