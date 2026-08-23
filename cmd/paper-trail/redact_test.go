package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func redactFixture() riskReportJSON {
	e := risk.NewEntity("companieshouse", "1", "Alpha Ltd",
		[]string{"1 Main Street, London, EC1A 1AA"}, []string{"BEACH, Gillian Taylor"})
	e.PersonDetails = []risk.Person{{
		Name: "BEACH, Gillian Taylor", BirthMonth: 1, BirthYear: 1969,
		Address: "124 Baker Street, London, W1U 6TY",
	}}
	return riskReportJSON{
		Queries:  []string{"Alpha"},
		Entities: []risk.Entity{e},
		Notes:    []string{"Companies House: checked BEACH, Gillian Taylor"},
		Score: risk.Score{
			Total: 3,
			Indicators: []risk.Indicator{{
				Code: "shared_person", Weight: 3,
				Entities: []string{"companieshouse: Alpha Ltd (1)"},
				Evidence: "BEACH, Gillian Taylor",
			}},
			Corroborations: []risk.Corroboration{{
				Entities: []string{"companieshouse: Alpha Ltd (1)"}, Codes: []string{"a", "b"},
			}},
		},
	}
}

// TestRedactRemovesEveryTraceOfPersonalData is the test that matters:
// serialize the whole redacted report and assert none of the personal
// data survives ANYWHERE -- not in entities, not in person details, and
// not in free-text evidence or notes.
func TestRedactRemovesEveryTraceOfPersonalData(t *testing.T) {
	out, n := redactReport(redactFixture())
	if n != 1 {
		t.Errorf("redacted count = %d, want 1", n)
	}
	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(blob)

	for _, leak := range []string{"Gillian", "Taylor", "BEACH, Gillian", "Baker Street", "1969"} {
		if strings.Contains(s, leak) {
			t.Errorf("personal data %q survived redaction: %s", leak, s)
		}
	}
	if !strings.Contains(s, "B.G.T.") {
		t.Errorf("expected initials to replace the name, got: %s", s)
	}
}

// TestRedactPreservesStructuralFindings is the other half of the
// contract: redaction must never change WHAT was found.
func TestRedactPreservesStructuralFindings(t *testing.T) {
	in := redactFixture()
	out, _ := redactReport(in)

	if out.Score.Total != in.Score.Total {
		t.Errorf("score changed: %d -> %d", in.Score.Total, out.Score.Total)
	}
	if len(out.Score.Indicators) != len(in.Score.Indicators) {
		t.Errorf("indicator count changed: %d -> %d", len(in.Score.Indicators), len(out.Score.Indicators))
	}
	if out.Score.Indicators[0].Code != "shared_person" {
		t.Error("indicator code must survive redaction")
	}
	if len(out.Entities) != len(in.Entities) {
		t.Error("entity count changed")
	}
	// Company identity is a public register fact and must be preserved,
	// otherwise there is nothing left to verify against.
	if out.Entities[0].Name != "Alpha Ltd" || out.Entities[0].ID != "1" {
		t.Errorf("company identity was redacted, but shouldn't be: %+v", out.Entities[0])
	}
	if len(out.Entities[0].Addresses) != 1 || out.Entities[0].Addresses[0] != "1 Main Street, London, EC1A 1AA" {
		t.Error("the company's own registered office is a public fact and should survive")
	}
}

// TestRedactDoesNotMutateOriginal guards the single-render-point wiring:
// a caller must still be able to write an unredacted copy in the same run.
func TestRedactDoesNotMutateOriginal(t *testing.T) {
	in := redactFixture()
	redactReport(in)
	if in.Entities[0].People[0] != "BEACH, Gillian Taylor" {
		t.Error("redactReport mutated its input")
	}
	if in.Entities[0].PersonDetails[0].BirthYear != 1969 {
		t.Error("redactReport mutated the input's person details")
	}
}

func TestRedactAddressKeepsPostcodeDistrictOnly(t *testing.T) {
	cases := map[string]string{
		"124 Baker Street, London, W1U 6TY":          "W1U area",
		"1 Main Street, Leeds, LS1 4AP":              "LS1 area",
		"592 Sheppard Avenue West, Toronto, Ontario": "Ontario (area only)",
		"": "",
	}
	for in, want := range cases {
		if got := redactAddress(in); got != want {
			t.Errorf("redactAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactNameProducesStableInitials(t *testing.T) {
	cases := map[string]string{
		"BEACH, Gillian Taylor": "B.G.T.",
		"Jane Doe":              "J.D.",
		"Jean-Luc Picard":       "J.L.P.",
	}
	for in, want := range cases {
		if got := redactName(in); got != want {
			t.Errorf("redactName(%q) = %q, want %q", in, got, want)
		}
	}
	// The same person must redact identically everywhere, or a
	// shared-officer finding becomes unreadable.
	if redactName("Jane Doe") != redactName("Jane Doe") {
		t.Error("redactName is not stable")
	}
}
