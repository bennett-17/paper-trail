package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// circuitBreakerThreshold is how many consecutive failures from one
// source within a single scan trip the breaker. Calibrated against two
// real, live-observed failure modes: OpenFEC's shared DEMO_KEY pool
// exhausted partway through a real multi-person scan, 429ing on every
// remaining name; ICIJ Offshore Leaks 500ing on every single query
// during a live outage. Once a source is clearly down or rate-limited
// for the rest of a run, paying its full retry-with-backoff cost on
// every remaining name wastes minutes of wall-clock time for zero
// additional signal -- the breaker exists to stop that, not to give up
// on a source after one ordinary transient failure.
const circuitBreakerThreshold = 3

// circuitBreakerNoteSuffix is appended to the note emitted the moment a
// breaker trips. It's a fixed, greppable marker deliberately -- see
// parseSourceHealth below, which scans the final notes list for this
// exact suffix to build a "source health" summary without threading a
// separate health-tracking parameter through every screen function's
// signature. A pragmatic choice, not the "correct" structured design
// (a typed health-event channel would be cleaner) -- but every screen
// in this project already communicates exclusively through the notes
// slice, and reusing that channel here means zero signature changes
// across a dozen call sites for a diagnostic feature that's secondary
// to the indicator itself.
const circuitBreakerNoteSuffix = "-- skipping remaining checks against this source for this run"

// circuitBreaker stops one screen or gather function from continuing
// to call an already-failing source for the rest of a single scan,
// after circuitBreakerThreshold consecutive failures. Not shared
// across scans, and not shared across different sources within one
// scan -- each function that makes its own per-item network calls owns
// exactly one. Safe for concurrent use: a few call sites (e.g.
// LittleSis's gatherer, which fans out one goroutine per query term via
// runConcurrentQueries) share one breaker across goroutines, unlike the
// sequential per-name loops most screen functions use.
type circuitBreaker struct {
	mu                  sync.Mutex
	consecutiveFailures int
	tripped             bool
}

// Skip reports whether the breaker has already tripped. Callers check
// this before even attempting the next call, not just after --
// avoiding the network round-trip entirely once a source is known to
// be down, rather than just suppressing the resulting note.
func (cb *circuitBreaker) Skip() bool {
	if cb == nil {
		return false
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripped
}

// Record updates the breaker with one call's outcome (nil err resets
// the failure streak; a non-nil err extends it). Returns true exactly
// once, the moment this call trips the breaker, so the caller can emit
// a single explanatory note instead of one per subsequently-skipped
// item.
func (cb *circuitBreaker) Record(err error) (justTripped bool) {
	if cb == nil {
		return false
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if err == nil {
		cb.consecutiveFailures = 0
		return false
	}
	cb.consecutiveFailures++
	if cb.consecutiveFailures >= circuitBreakerThreshold && !cb.tripped {
		cb.tripped = true
		return true
	}
	return false
}

// tripNote formats the note a screen function emits the moment its
// breaker trips (see circuitBreakerNoteSuffix). sourceLabel matches
// the same name that function's progress.report calls already use
// (e.g. "OpenFEC", "ICIJ Offshore Leaks Database"), so a source
// appears under one consistent name across progress output, notes, and
// the source-health summary alike.
func tripNote(sourceLabel string) string {
	return fmt.Sprintf("%s: too many consecutive failures (%d) %s", sourceLabel, circuitBreakerThreshold, circuitBreakerNoteSuffix)
}

// sourceHealth is a plain-language summary of which sources didn't
// come back clean during a scan, split into two different reasons --
// conflating them would be misleading in opposite directions. A
// skipped source (no credential configured) is routine and expected
// for many users; a degraded source (repeatedly failing mid-scan, most
// often a live outage or rate limit) means whatever that source didn't
// find this run is a real gap, not a genuine "nothing there" result,
// and re-running later is worth considering.
type sourceHealth struct {
	Degraded []string `json:"degraded,omitempty"` // circuit-breaker tripped: this source likely has a live outage or is rate-limited
	Skipped  []string `json:"skipped,omitempty"`  // never ran at all: missing API key/credential
}

// Clean reports whether every configured source came back without
// tripping its breaker or being skipped for a missing credential.
func (h sourceHealth) Clean() bool {
	return len(h.Degraded) == 0 && len(h.Skipped) == 0
}

// parseSourceHealth scans a finished scan's notes for the two
// recognizable patterns worth summarizing: a circuit-breaker trip (see
// tripNote) and a "skipped (" note every credential-gated source in
// this project already emits when its API key isn't configured (e.g.
// `SAM.gov Exclusions: skipped (the SAM.gov Exclusions API requires...`).
// Both lists are deduplicated and sorted, since the same source can log
// more than one matching note in a single run (a screen scoped to both
// query terms and person names can, in principle, trip more than once
// if Record's internal state were ever reset -- it currently can't be,
// but parsing defensively costs nothing).
func parseSourceHealth(notes []string) sourceHealth {
	degraded := map[string]bool{}
	skipped := map[string]bool{}
	for _, n := range notes {
		source, rest, ok := strings.Cut(n, ": ")
		if !ok {
			continue
		}
		switch {
		case strings.Contains(rest, circuitBreakerNoteSuffix):
			degraded[source] = true
		case strings.HasPrefix(rest, "skipped ("):
			skipped[source] = true
		}
	}
	h := sourceHealth{
		Degraded: make([]string, 0, len(degraded)),
		Skipped:  make([]string, 0, len(skipped)),
	}
	for s := range degraded {
		h.Degraded = append(h.Degraded, s)
	}
	for s := range skipped {
		h.Skipped = append(h.Skipped, s)
	}
	sort.Strings(h.Degraded)
	sort.Strings(h.Skipped)
	return h
}

// screenCoverage is one screen's own account of what it checked and
// how much of it came back clean -- the negative result, which every
// report before this one silently discarded.
//
// This matters for the same reason a lab reports its controls: a
// report that only ever accumulates suspicion is biased by
// construction. "14 names screened against the UK Sanctions List, 0
// matched" is a real finding about those 14 names, and its absence
// left readers unable to tell "checked and clean" apart from "never
// checked at all" -- two situations the source-health summary above
// only partly separates (it knows a source was skipped or degraded,
// not how much work a healthy source actually did).
type screenCoverage struct {
	Source   string `json:"source"`
	Screened int    `json:"screened"` // distinct names/terms actually sent to the source
	Matched  int    `json:"matched"`  // how many of those produced at least one hit
}

// Clean reports whether this screen checked something and found
// nothing -- the case worth stating positively in a report.
func (c screenCoverage) Clean() bool { return c.Screened > 0 && c.Matched == 0 }

// coverageNote encodes a screen's coverage into the same notes channel
// tripNote already uses, so no screen needs a wider signature just to
// report a count. Deliberately readable as prose too: it appears
// verbatim in the notes list, where it reads as an ordinary line.
//
// Only the "adjudicated list" screens emit one -- sanctions, exclusion,
// disqualification, PEP, and offshore-leaks checks -- because those are
// the screens where a clean result actually means something ("this name
// is not on that list"). The mention-style screens (news, filings,
// litigation dockets) deliberately do NOT: "no news article mentions
// this entity" is not exculpatory in any comparable way, and reporting
// it in the same breath would overstate what a null result there proves.
func coverageNote(source string, screened, matched int) string {
	return fmt.Sprintf("%s: screened %d name(s) against this source, %d matched", source, screened, matched)
}

var coverageNoteRE = regexp.MustCompile(`^screened (\d+) name\(s\) against this source, (\d+) matched$`)

// parseScreenCoverage pulls every coverageNote back out of a finished
// scan's notes, deduplicated by source (last one wins) and sorted, so
// the report can state what was ruled out. Notes that aren't coverage
// notes are ignored, same defensive posture as parseSourceHealth.
func parseScreenCoverage(notes []string) []screenCoverage {
	bySource := map[string]screenCoverage{}
	for _, n := range notes {
		source, rest, ok := strings.Cut(n, ": ")
		if !ok {
			continue
		}
		m := coverageNoteRE.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		screened, err1 := strconv.Atoi(m[1])
		matched, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			continue
		}
		bySource[source] = screenCoverage{Source: source, Screened: screened, Matched: matched}
	}
	out := make([]screenCoverage, 0, len(bySource))
	for _, c := range bySource {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// sourceFilter, when non-nil, restricts gatherAndScore to a subset of
// its usual sources -- backing both --fast (a fixed skip-list of
// known-slow sources) and --retry-failed-sources (a computed
// only-list built from a previous run's source health). Source names
// must match the same label each gatherer/screen already passes to
// progress.report, so one canonical name works across progress
// output, notes, health parsing, and filtering alike.
type sourceFilter struct {
	only map[string]bool // if non-nil, ONLY these sources run
	skip map[string]bool // if non-nil, these sources are excluded (checked after "only")
}

// allows reports whether source should run under this filter. A nil
// *sourceFilter allows everything -- the default, zero-configuration
// case every existing caller keeps getting.
func (f *sourceFilter) allows(source string) bool {
	if f == nil {
		return true
	}
	if f.only != nil && !f.only[source] {
		return false
	}
	if f.skip != nil && f.skip[source] {
		return false
	}
	return true
}

// fastModeSkipSources are the sources confirmed to meaningfully slow
// down a multi-term scan: GDELT (a hard 5-second per-request rate
// limit), CourtListener (5 requests/minute even authenticated), OpenFEC
// (a shared, easily-exhausted DEMO_KEY pool absent a personal key), and
// the two OFSI sub-screens officerFanOut/pscSanctionsAdjacentChange add
// on top of the entity-level UK sanctions screen -- each one call per
// officer/PSC discovered, which can add up fast on a company with many
// current or historical appointments. --fast skips exactly these,
// nothing else -- every other source in this project responds in at
// most a few seconds per query.
var fastModeSkipSources = map[string]bool{
	"GDELT":                                 true,
	"CourtListener":                         true,
	"OpenFEC":                               true,
	"UK sanctions screen (officer fan-out)": true,
	"UK sanctions screen (PSC)":             true,
	"Companies House filing history":        true,
	// One extra request per company each. Exemptions is additionally
	// self-limiting -- it only runs on a company that already has
	// outstanding charges and a "no PSC identified" statement -- but
	// --fast means fewest requests, so it is skipped there too.
	"Companies House UK establishments": true,
	"Companies House exemptions":        true,
}
