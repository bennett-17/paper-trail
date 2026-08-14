package risk

import (
	"strings"
	"testing"
)

func shortLived(id, name, formed, dissolved string) Entity {
	e := NewEntity("companieshouse", id, name, nil, nil)
	e.FormedOn = formed
	e.DissolvedOn = dissolved
	return e
}

func TestShortLivedCompaniesFiresOnCluster(t *testing.T) {
	entities := []Entity{
		shortLived("1", "Alpha Ltd", "2020-01-10", "2021-03-01"), // ~14 months
		shortLived("2", "Beta Ltd", "2020-02-01", "2021-06-15"),  // ~16 months
	}
	out := ShortLivedCompanies(entities)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(out), out)
	}
	if out[0].Code != "short_lived_company_cluster" {
		t.Errorf("Code = %q", out[0].Code)
	}
	if len(out[0].Entities) != 2 {
		t.Errorf("Entities = %v, want both members", out[0].Entities)
	}
	if out[0].Weight != shortLivedCompanyWeight {
		t.Errorf("Weight = %d, want %d", out[0].Weight, shortLivedCompanyWeight)
	}
	// Earliest dissolution anchors it on the report timeline.
	if out[0].Date != "2021-03-01" {
		t.Errorf("Date = %q, want the earliest dissolution 2021-03-01", out[0].Date)
	}
}

// TestShortLivedCompaniesIgnoresSingleCompany guards the core scoping
// decision: one short-lived company is an ordinary failed business,
// not a finding, and must never be reported as one.
func TestShortLivedCompaniesIgnoresSingleCompany(t *testing.T) {
	out := ShortLivedCompanies([]Entity{
		shortLived("1", "Alpha Ltd", "2020-01-10", "2021-03-01"),
	})
	if len(out) != 0 {
		t.Errorf("got %+v, want nothing for a lone short-lived company", out)
	}
}

func TestShortLivedCompaniesIgnoresLongLivedCompanies(t *testing.T) {
	out := ShortLivedCompanies([]Entity{
		shortLived("1", "Alpha Ltd", "2010-01-10", "2020-03-01"), // a decade
		shortLived("2", "Beta Ltd", "2011-02-01", "2019-06-15"),  // 8 years
	})
	if len(out) != 0 {
		t.Errorf("got %+v, want nothing -- neither company is short-lived", out)
	}
}

// TestShortLivedCompaniesBoundaryIsCalendarCorrect checks the 24-month
// cutoff is a real calendar anniversary, not a 30-day-months
// approximation that would drift by weeks over two years.
func TestShortLivedCompaniesBoundaryIsCalendarCorrect(t *testing.T) {
	// Dissolved one day BEFORE the 24-month anniversary: short-lived.
	inside := []Entity{
		shortLived("1", "Alpha Ltd", "2020-01-10", "2022-01-09"),
		shortLived("2", "Beta Ltd", "2020-01-10", "2022-01-09"),
	}
	if got := ShortLivedCompanies(inside); len(got) != 1 {
		t.Errorf("one day inside the 24-month window should count: got %+v", got)
	}
	// Dissolved exactly ON the anniversary: not short-lived.
	onBoundary := []Entity{
		shortLived("1", "Alpha Ltd", "2020-01-10", "2022-01-10"),
		shortLived("2", "Beta Ltd", "2020-01-10", "2022-01-10"),
	}
	if got := ShortLivedCompanies(onBoundary); len(got) != 0 {
		t.Errorf("exactly 24 months is not short-lived: got %+v", got)
	}
}

func TestShortLivedCompaniesIgnoresMissingOrIncoherentDates(t *testing.T) {
	stillLive := NewEntity("companieshouse", "1", "Live Ltd", nil, nil)
	stillLive.FormedOn = "2020-01-10" // no dissolution date at all

	noFormation := NewEntity("companieshouse", "2", "Odd Ltd", nil, nil)
	noFormation.DissolvedOn = "2021-01-10" // no formation date

	backwards := shortLived("3", "Backwards Ltd", "2021-06-01", "2020-01-01")
	unparseable := shortLived("4", "Junk Ltd", "not-a-date", "also-not-a-date")

	out := ShortLivedCompanies([]Entity{stillLive, noFormation, backwards, unparseable})
	if len(out) != 0 {
		t.Errorf("got %+v, want nothing -- none of these have a usable lifespan", out)
	}
}

func TestShortLivedCompaniesDeduplicatesSameEntity(t *testing.T) {
	// The same company resolved by two different query terms must not
	// count twice toward the cluster threshold.
	dup := shortLived("1", "Alpha Ltd", "2020-01-10", "2021-03-01")
	if got := ShortLivedCompanies([]Entity{dup, dup}); len(got) != 0 {
		t.Errorf("got %+v, want nothing -- that's one company listed twice, not a cluster", got)
	}
}

func TestShortLivedCompaniesEvidenceNamesOverflowExplicitly(t *testing.T) {
	var entities []Entity
	for _, n := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		entities = append(entities, shortLived(n, n+" Ltd", "2020-01-10", "2021-03-01"))
	}
	out := ShortLivedCompanies(entities)
	if len(out) != 1 {
		t.Fatalf("got %d indicators, want 1", len(out))
	}
	if !strings.Contains(out[0].Evidence, "8 short-lived entities") {
		t.Errorf("Evidence = %q, want the true total stated", out[0].Evidence)
	}
	// A truncated list must say so rather than silently reading as complete.
	if !strings.Contains(out[0].Evidence, "and 2 more") {
		t.Errorf("Evidence = %q, want an explicit overflow count", out[0].Evidence)
	}
	if len(out[0].Entities) != 8 {
		t.Errorf("Entities has %d members, want all 8 regardless of Evidence truncation", len(out[0].Entities))
	}
}
