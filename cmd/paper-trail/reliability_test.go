package main

import "testing"

func TestCircuitBreakerTripsAfterThreshold(t *testing.T) {
	var cb circuitBreaker
	err := &ClientErrorStub{}
	for i := 0; i < circuitBreakerThreshold-1; i++ {
		if justTripped := cb.Record(err); justTripped {
			t.Fatalf("Record tripped early on failure %d, want threshold %d", i+1, circuitBreakerThreshold)
		}
		if cb.Skip() {
			t.Fatalf("Skip() = true before the breaker should have tripped (failure %d)", i+1)
		}
	}
	if justTripped := cb.Record(err); !justTripped {
		t.Fatalf("Record did not report tripping on the %dth consecutive failure", circuitBreakerThreshold)
	}
	if !cb.Skip() {
		t.Error("Skip() = false after the breaker tripped")
	}
}

func TestCircuitBreakerOnlyReportsJustTrippedOnce(t *testing.T) {
	var cb circuitBreaker
	err := &ClientErrorStub{}
	for i := 0; i < circuitBreakerThreshold; i++ {
		cb.Record(err)
	}
	if !cb.Skip() {
		t.Fatal("breaker should be tripped after threshold failures")
	}
	if justTripped := cb.Record(err); justTripped {
		t.Error("Record reported tripping again on a subsequent failure -- should only fire once")
	}
}

func TestCircuitBreakerResetsOnSuccess(t *testing.T) {
	var cb circuitBreaker
	err := &ClientErrorStub{}
	cb.Record(err)
	cb.Record(err)
	cb.Record(nil) // a success partway through resets the streak
	cb.Record(err)
	if cb.Skip() {
		t.Error("breaker tripped after only 2 consecutive failures (streak should have reset on the intervening success)")
	}
}

func TestCircuitBreakerNilIsSafeNoOp(t *testing.T) {
	var cb *circuitBreaker
	if cb.Skip() {
		t.Error("nil breaker Skip() = true, want false (never blocks)")
	}
	if justTripped := cb.Record(&ClientErrorStub{}); justTripped {
		t.Error("nil breaker Record() reported tripping, want false always")
	}
}

// ClientErrorStub is a minimal error implementation for these tests --
// the breaker only cares whether err is nil, not its type or message.
type ClientErrorStub struct{}

func (e *ClientErrorStub) Error() string { return "stub error" }

func TestTripNoteFormat(t *testing.T) {
	got := tripNote("OpenFEC")
	if got != "OpenFEC: too many consecutive failures (3) -- skipping remaining checks against this source for this run" {
		t.Errorf("tripNote(%q) = %q", "OpenFEC", got)
	}
}

func TestParseSourceHealthSeparatesDegradedFromSkipped(t *testing.T) {
	notes := []string{
		`OpenFEC: "SLOAN TIMOTHY J": OpenFEC API returned HTTP 429 for https://...`,
		tripNote("OpenFEC"),
		`SAM.gov Exclusions: skipped (the SAM.gov Exclusions API requires a free API key. Register at https://sam.gov...)`,
		`ACNC (Australia): no match for "Example Corp"`,
		tripNote("ICIJ Offshore Leaks Database"),
	}
	h := parseSourceHealth(notes)
	if len(h.Degraded) != 2 || h.Degraded[0] != "ICIJ Offshore Leaks Database" || h.Degraded[1] != "OpenFEC" {
		t.Errorf("Degraded = %v, want [ICIJ Offshore Leaks Database, OpenFEC]", h.Degraded)
	}
	if len(h.Skipped) != 1 || h.Skipped[0] != "SAM.gov Exclusions" {
		t.Errorf("Skipped = %v, want [SAM.gov Exclusions]", h.Skipped)
	}
	if h.Clean() {
		t.Error("Clean() = true, want false with degraded/skipped sources present")
	}
}

func TestParseSourceHealthCleanWhenNothingWrong(t *testing.T) {
	notes := []string{
		`ACNC (Australia): no match for "Example Corp"`,
		`LittleSis: no organization match for "Example Corp"`,
	}
	h := parseSourceHealth(notes)
	if !h.Clean() {
		t.Errorf("Clean() = false, want true: %+v", h)
	}
}

func TestParseSourceHealthDeduplicates(t *testing.T) {
	notes := []string{tripNote("OpenFEC"), tripNote("OpenFEC")}
	h := parseSourceHealth(notes)
	if len(h.Degraded) != 1 {
		t.Errorf("Degraded = %v, want exactly one deduplicated entry", h.Degraded)
	}
}

func TestSourceFilterNilAllowsEverything(t *testing.T) {
	var f *sourceFilter
	if !f.allows("OpenFEC") {
		t.Error("nil filter should allow every source")
	}
}

func TestSourceFilterSkipList(t *testing.T) {
	f := &sourceFilter{skip: fastModeSkipSources}
	if f.allows("OpenFEC") {
		t.Error("OpenFEC should be excluded by the fast-mode skip list")
	}
	if !f.allows("GLEIF") {
		t.Error("GLEIF isn't in the fast-mode skip list and should still be allowed")
	}
}

func TestSourceFilterOnlyList(t *testing.T) {
	f := &sourceFilter{only: map[string]bool{"ICIJ Offshore Leaks Database": true}}
	if !f.allows("ICIJ Offshore Leaks Database") {
		t.Error("the one source in the only-list should be allowed")
	}
	if f.allows("GLEIF") {
		t.Error("a source not in the only-list should be excluded")
	}
}

func TestSourceFilterOnlyAndSkipCombine(t *testing.T) {
	f := &sourceFilter{
		only: map[string]bool{"OpenFEC": true, "GLEIF": true},
		skip: map[string]bool{"OpenFEC": true},
	}
	if f.allows("OpenFEC") {
		t.Error("OpenFEC is in only but also skip -- skip should win")
	}
	if !f.allows("GLEIF") {
		t.Error("GLEIF is in only and not in skip -- should be allowed")
	}
}

// TestFastModeSkipsBothOFSISubScreens guards --fast covering the two
// per-officer/per-PSC OFSI screens officerFanOut and
// pscSanctionsAdjacentChange add on top of the entity-level UK
// sanctions screen -- easy to forget to add here, since neither is a
// top-level gatherer/screen function the way every other
// fastModeSkipSources entry is (both are nested sub-calls inside an
// already-allowed gatherer). Can't verify network calls are actually
// skipped in a unit test (ofsi.NewClient always dials the real OFSI
// endpoint, unlike companieshouse.Client's injectable BaseURL), but
// this at least guards the skip *list* itself, and
// TestOfficerFanOutDiscoversHop1AndHop2Companies etc. (risk_gather_test.go)
// confirm the rest of officerFanOut's own indicators still fire when
// these two are skipped -- i.e. the skip is scoped to just the OFSI
// sub-block, not the whole function.
func TestFastModeSkipsBothOFSISubScreens(t *testing.T) {
	for _, source := range []string{"UK sanctions screen (officer fan-out)", "UK sanctions screen (PSC)"} {
		if !fastModeSkipSources[source] {
			t.Errorf("fastModeSkipSources missing %q", source)
		}
	}
}

func TestCoverageNoteRoundTrips(t *testing.T) {
	notes := []string{
		coverageNote("UK sanctions screen", 14, 0),
		coverageNote("SAM.gov Exclusions", 3, 2),
	}
	got := parseScreenCoverage(notes)
	if len(got) != 2 {
		t.Fatalf("got %d coverage entries, want 2: %+v", len(got), got)
	}
	// Sorted by source, so SAM.gov comes first.
	if got[0].Source != "SAM.gov Exclusions" || got[0].Screened != 3 || got[0].Matched != 2 {
		t.Errorf("got[0] = %+v, want SAM.gov Exclusions 3/2", got[0])
	}
	if got[1].Source != "UK sanctions screen" || got[1].Screened != 14 || got[1].Matched != 0 {
		t.Errorf("got[1] = %+v, want UK sanctions screen 14/0", got[1])
	}
	if !got[1].Clean() {
		t.Error("14 screened / 0 matched should report Clean()")
	}
	if got[0].Clean() {
		t.Error("3 screened / 2 matched must not report Clean()")
	}
}

func TestScreenCoverageCleanRequiresActualWork(t *testing.T) {
	// A screen that never ran (0 screened) is NOT "clean" -- that's the
	// exact conflation this whole feature exists to prevent.
	if (screenCoverage{Source: "x", Screened: 0, Matched: 0}).Clean() {
		t.Error("a screen that checked nothing must not report Clean()")
	}
}

func TestParseScreenCoverageIgnoresOtherNotes(t *testing.T) {
	notes := []string{
		"Companies House: some ordinary error note",
		tripNote("OpenFEC"),
		"SAM.gov Exclusions: skipped (no API key configured)",
		coverageNote("UN sanctions screen", 7, 1),
		"not even a prefixed note",
	}
	got := parseScreenCoverage(notes)
	if len(got) != 1 || got[0].Source != "UN sanctions screen" {
		t.Errorf("got %+v, want only the one real coverage note", got)
	}
}

// TestCoverageNoteDoesNotDisturbSourceHealth guards the shared notes
// channel: coverage notes and health notes travel together, and a
// coverage note must never be misread as a degraded or skipped source.
func TestCoverageNoteDoesNotDisturbSourceHealth(t *testing.T) {
	notes := []string{
		coverageNote("UK sanctions screen", 14, 0),
		coverageNote("Sanctions screen", 14, 0),
	}
	h := parseSourceHealth(notes)
	if !h.Clean() {
		t.Errorf("coverage notes alone produced source health %+v, want clean", h)
	}
}

// TestCoverageCountsOnlySuccessfulChecks documents the bug this
// feature nearly shipped with: a screen whose requests all FAILED was
// reporting "3 names screened, no matches", which reads as an
// all-clear when nothing was actually checked. Coverage counts only
// names whose request came back successfully, so a degraded source
// reports 0 screened rather than a false negative result.
func TestCoverageCountsOnlySuccessfulChecks(t *testing.T) {
	// A screen that dequeued 3 names but had every request fail should
	// emit 0 covered -- and 0 covered is not Clean(), so nothing in the
	// report can read it as "checked and found nothing".
	c := parseScreenCoverage([]string{coverageNote("INTERPOL Red Notices", 0, 0)})
	if len(c) != 1 {
		t.Fatalf("got %+v, want one entry", c)
	}
	if c[0].Clean() {
		t.Error("a screen that completed 0 successful checks must not report Clean() -- that's a false all-clear")
	}
}
