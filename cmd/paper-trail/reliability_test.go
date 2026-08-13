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
