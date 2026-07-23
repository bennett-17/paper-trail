package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
)

// pscChainFixture serves a fixed one-item PSC response for a given
// company number, modeled on the same shape confirmed live in
// internal/companieshouse -- just enough fields for followPSCChain to
// parse a single corporate PSC (or none at all, for an empty items
// list terminating the chain).
func pscChainFixture(t *testing.T, byCompanyNumber map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for number, body := range byCompanyNumber {
		body := body
		mux.HandleFunc("/company/"+number+"/persons-with-significant-control", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func corporatePSCJSON(name, country, regNumber string) string {
	return fmt.Sprintf(`{
		"items_per_page": 25,
		"items": [{
			"name": %q,
			"kind": "corporate-entity-person-with-significant-control",
			"natures_of_control": ["ownership-of-shares-75-to-100-percent"],
			"notified_on": "2016-04-06",
			"identification": {
				"country_registered": %q,
				"registration_number": %q
			}
		}],
		"start_index": 0,
		"total_results": 1,
		"active_count": 1,
		"ceased_count": 0
	}`, name, country, regNumber)
}

const emptyPSCJSON = `{"items_per_page": 25, "items": [], "start_index": 0, "total_results": 0, "active_count": 0, "ceased_count": 0}`

func newChainTestClient(t *testing.T, srv *httptest.Server) *companieshouse.Client {
	t.Helper()
	c, err := companieshouse.NewClient("test-api-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.BaseURL = srv.URL
	return c
}

func TestFollowPSCChainSameCountryDoesNotCrossJurisdictions(t *testing.T) {
	// Modeled on the real, live-verified Tesco corporate group: Tesco
	// Holdings Limited (start) -> Tesco Plc (00445790, England) -> no
	// PSCs at all (Tesco Plc, an exchange-listed public company, is
	// exempt from PSC reporting). Every hop is England, so this must
	// NOT be reported as crossing jurisdictions.
	srv := pscChainFixture(t, map[string]string{
		"00445790": emptyPSCJSON,
	})
	c := newChainTestClient(t, srv)

	start := companieshouse.PSC{
		Name:                        "Tesco Holdings Limited",
		Kind:                        "corporate-entity-person-with-significant-control",
		CorporateCountryRegistered:  "England",
		CorporateRegistrationNumber: "00445790",
	}
	countries, loopedBack := followPSCChain(c, "00000099", start, 0)
	if len(countries) != 1 || countries[0] != "England" {
		t.Fatalf("countries = %v, want a single England entry (no cross-jurisdiction hop)", countries)
	}
	if loopedBack {
		t.Error("loopedBack = true, want false: the chain never returns to the root company")
	}
}

func TestFollowPSCChainCrossesJurisdictions(t *testing.T) {
	// A chain that layers ownership across three distinct
	// jurisdictions: England -> Jersey -> British Virgin Islands.
	srv := pscChainFixture(t, map[string]string{
		"00222222": corporatePSCJSON("BVI Holdco Limited", "British Virgin Islands", "00333333"),
		"00333333": emptyPSCJSON,
	})
	c := newChainTestClient(t, srv)

	start := companieshouse.PSC{
		Name:                        "Jersey Holdco Limited",
		Kind:                        "corporate-entity-person-with-significant-control",
		CorporateCountryRegistered:  "Jersey",
		CorporateRegistrationNumber: "00222222",
	}
	countries, loopedBack := followPSCChain(c, "00000099", start, 0)
	want := []string{"Jersey", "British Virgin Islands"}
	if len(countries) != len(want) || countries[0] != want[0] || countries[1] != want[1] {
		t.Fatalf("countries = %v, want %v", countries, want)
	}
	if loopedBack {
		t.Error("loopedBack = true, want false: the chain never returns to the root company")
	}
}

func TestFollowPSCChainStopsOnCycle(t *testing.T) {
	// A (contrived, not observed live) ownership cycle -- company A's
	// PSC is company B, and company B's PSC is company A again. The
	// visited-registration-number guard must break out rather than
	// looping until pscChainMaxDepth, and the resulting country list
	// must not contain a duplicate.
	srv := pscChainFixture(t, map[string]string{
		"00000001": corporatePSCJSON("Company A", "England", "00000002"),
		"00000002": corporatePSCJSON("Company B", "England", "00000001"),
	})
	c := newChainTestClient(t, srv)

	start := companieshouse.PSC{
		Name:                        "Company B",
		Kind:                        "corporate-entity-person-with-significant-control",
		CorporateCountryRegistered:  "England",
		CorporateRegistrationNumber: "00000001",
	}
	countries, loopedBack := followPSCChain(c, "00000099", start, 0)
	if len(countries) != 1 || countries[0] != "England" {
		t.Fatalf("countries = %v, want a single deduplicated England entry", countries)
	}
	if loopedBack {
		t.Error("loopedBack = true, want false: this cycle never involves the root company")
	}
}

func TestAppointmentBurstFlagsRealCorporateNomineeDirectorPattern(t *testing.T) {
	// Modeled directly on real, live-confirmed appointment history for
	// Companies House officer ID nEggfu04XePBqnRERobPjXjmHGk
	// ("Corporate Directors Limited"): three separate companies all
	// gained this same corporate director on 2014-12-09 alone.
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "00000001", CompanyName: "DRONSDALE LTD.", AppointedOn: "2014-12-09"},
		{CompanyNumber: "00000002", CompanyName: "ROUNDSTONE NETWORK LTD.", AppointedOn: "2014-12-09"},
		{CompanyNumber: "00000003", CompanyName: "DRUMMAND LTD", AppointedOn: "2014-12-09"},
		{CompanyNumber: "00000004", CompanyName: "EASTBROOKE DEVELOPMENT LIMITED", AppointedOn: "2014-11-17"},
	}
	desc := appointmentBurst(appointments)
	if desc == "" {
		t.Fatal("got no flag, want a burst flagged for 3 companies on the same day")
	}
	if !strings.Contains(desc, "3 companies") {
		t.Errorf("desc = %q, want it to report 3 companies", desc)
	}
}

func TestAppointmentBurstIgnoresOrdinaryMultiDirectorshipsSpreadOverYears(t *testing.T) {
	// A real board member of several unrelated companies, but spread
	// over years rather than clustered in a week -- must not be
	// flagged, since holding several legitimate directorships over a
	// career is completely normal.
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "00000001", CompanyName: "FIRST COMPANY LTD", AppointedOn: "2010-01-01"},
		{CompanyNumber: "00000002", CompanyName: "SECOND COMPANY LTD", AppointedOn: "2014-06-15"},
		{CompanyNumber: "00000003", CompanyName: "THIRD COMPANY LTD", AppointedOn: "2019-11-30"},
	}
	if desc := appointmentBurst(appointments); desc != "" {
		t.Errorf("got %q, want no flag for directorships spread over years", desc)
	}
}

func TestAppointmentBurstDedupesRepeatAppointmentsToSameCompany(t *testing.T) {
	// The same company number appearing twice within the window (e.g.
	// a resign-then-reappoint) must count once, not twice, toward the
	// threshold.
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "00000001", CompanyName: "SAME COMPANY LTD", AppointedOn: "2020-01-01", ResignedOn: "2020-01-02"},
		{CompanyNumber: "00000001", CompanyName: "SAME COMPANY LTD", AppointedOn: "2020-01-03"},
		{CompanyNumber: "00000002", CompanyName: "OTHER COMPANY LTD", AppointedOn: "2020-01-02"},
	}
	if desc := appointmentBurst(appointments); desc != "" {
		t.Errorf("got %q, want no flag: only 2 distinct companies, below the threshold of 3", desc)
	}
}

func TestAppointmentBurstSkipsUnparseableDates(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "00000001", CompanyName: "A LTD", AppointedOn: "not-a-date"},
		{CompanyNumber: "00000002", CompanyName: "B LTD", AppointedOn: "also-not-a-date"},
		{CompanyNumber: "00000003", CompanyName: "C LTD", AppointedOn: "2020-01-01"},
	}
	// Must not panic, and the two unparseable entries can't contribute
	// to any window, so this falls well short of the threshold.
	if desc := appointmentBurst(appointments); desc != "" {
		t.Errorf("got %q, want no flag when only 1 of 3 appointments has a parseable date", desc)
	}
}

func TestFollowPSCChainStopsWhenNoCorporatePSCFound(t *testing.T) {
	// The next hop's own PSC is an individual, not another corporate
	// entity -- the chain must stop there rather than erroring, since
	// an individual PSC is a normal, legitimate chain ending.
	srv := pscChainFixture(t, map[string]string{
		"00444444": `{
			"items_per_page": 25,
			"items": [{
				"name": "Mrs Jane Example",
				"kind": "individual-person-with-significant-control",
				"natures_of_control": ["ownership-of-shares-75-to-100-percent"],
				"notified_on": "2016-04-06"
			}],
			"start_index": 0,
			"total_results": 1,
			"active_count": 1,
			"ceased_count": 0
		}`,
	})
	c := newChainTestClient(t, srv)

	start := companieshouse.PSC{
		Name:                        "Holdco Limited",
		Kind:                        "corporate-entity-person-with-significant-control",
		CorporateCountryRegistered:  "England",
		CorporateRegistrationNumber: "00444444",
	}
	countries, loopedBack := followPSCChain(c, "00000099", start, 0)
	if len(countries) != 1 || countries[0] != "England" {
		t.Fatalf("countries = %v, want a single England entry (chain ends at an individual PSC)", countries)
	}
	if loopedBack {
		t.Error("loopedBack = true, want false: the chain ends at an individual, never reaching the root company")
	}
}

func TestFollowPSCChainDetectsDirectLoopBackToRoot(t *testing.T) {
	// The starting PSC's own registration number IS the root company
	// (e.g. rootNumber's direct corporate PSC turns out to itself carry
	// rootNumber -- a 1-hop loop). No server calls should even be
	// needed since this is caught before the first fetch.
	srv := pscChainFixture(t, map[string]string{})
	c := newChainTestClient(t, srv)

	start := companieshouse.PSC{
		Name:                        "Root Company Limited",
		Kind:                        "corporate-entity-person-with-significant-control",
		CorporateCountryRegistered:  "England",
		CorporateRegistrationNumber: "00000001",
	}
	countries, loopedBack := followPSCChain(c, "00000001", start, 0)
	if !loopedBack {
		t.Error("loopedBack = false, want true: the starting PSC's own registration number is the root company")
	}
	if len(countries) != 1 || countries[0] != "England" {
		t.Fatalf("countries = %v, want a single England entry", countries)
	}
}

func TestFollowPSCChainDetectsIndirectLoopBackToRoot(t *testing.T) {
	// A two-hop loop: root's PSC is Company A, Company A's PSC is the
	// root company itself under a different registration-number
	// casing/padding -- sameCompanyNumber must ignore that padding
	// difference (confirmed live elsewhere that some sources return
	// company numbers unpadded).
	srv := pscChainFixture(t, map[string]string{
		"00000002": corporatePSCJSON("Root Company Limited", "England", "1"),
	})
	c := newChainTestClient(t, srv)

	start := companieshouse.PSC{
		Name:                        "Company A Limited",
		Kind:                        "corporate-entity-person-with-significant-control",
		CorporateCountryRegistered:  "England",
		CorporateRegistrationNumber: "00000002",
	}
	countries, loopedBack := followPSCChain(c, "00000001", start, 0)
	if !loopedBack {
		t.Error("loopedBack = false, want true: Company A's own PSC is the root company (registration number 1 vs 00000001)")
	}
	if len(countries) != 1 || countries[0] != "England" {
		t.Fatalf("countries = %v, want a single England entry", countries)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestFinancialAnomalyFindsLargestSwing(t *testing.T) {
	// Newest first, matching ProPublica's own ordering. The
	// 2019->2020 jump (5x) is well above financialAnomalyRatio (5.0);
	// the 2018->2019 change (1.5x) is not.
	filings := []nonprofit.Filing{
		{TaxYear: 2020, TotalRevenue: int64Ptr(500_000)},
		{TaxYear: 2019, TotalRevenue: int64Ptr(100_000)},
		{TaxYear: 2018, TotalRevenue: int64Ptr(150_000)},
	}
	desc := financialAnomaly(filings)
	if desc == "" {
		t.Fatal("expected an anomaly description, got none")
	}
	if !strings.Contains(desc, "5.0x increase") {
		t.Errorf("description = %q, want it to mention the 5.0x increase", desc)
	}
}

func TestFinancialAnomalyDetectsDecrease(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2022, TotalRevenue: int64Ptr(100_000)},
		{TaxYear: 2021, TotalRevenue: int64Ptr(700_000)},
	}
	desc := financialAnomaly(filings)
	if !strings.Contains(desc, "decrease") {
		t.Errorf("description = %q, want it to mention a decrease", desc)
	}
}

func TestFinancialAnomalyIgnoresOrdinaryFluctuation(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2022, TotalRevenue: int64Ptr(110_000)},
		{TaxYear: 2021, TotalRevenue: int64Ptr(100_000)},
	}
	if desc := financialAnomaly(filings); desc != "" {
		t.Errorf("got %q, want no anomaly for a 1.1x change", desc)
	}
}

func TestFinancialAnomalySkipsMissingFigures(t *testing.T) {
	// Neither year has a published revenue figure -- shouldn't be
	// treated as a swing to/from zero.
	filings := []nonprofit.Filing{
		{TaxYear: 2022, TotalRevenue: nil},
		{TaxYear: 2021, TotalRevenue: nil},
	}
	if desc := financialAnomaly(filings); desc != "" {
		t.Errorf("got %q, want no anomaly when figures are missing, not zero", desc)
	}
}

func TestFinancialAnomalyChecksAssetsToo(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2015, TotalAssets: int64Ptr(573_391)},
		{TaxYear: 2014, TotalAssets: int64Ptr(2_777)},
	}
	desc := financialAnomaly(filings)
	if !strings.Contains(desc, "Total assets") {
		t.Errorf("description = %q, want it to check assets as well as revenue", desc)
	}
}

func TestFinancialAnomalyWithFewerThanTwoFilingsIsEmpty(t *testing.T) {
	if desc := financialAnomaly([]nonprofit.Filing{{TaxYear: 2022, TotalRevenue: int64Ptr(100_000)}}); desc != "" {
		t.Errorf("got %q, want no anomaly with only one filing to compare", desc)
	}
	if desc := financialAnomaly(nil); desc != "" {
		t.Errorf("got %q, want no anomaly with no filings at all", desc)
	}
}

// TestHighOfficerCompensationRealLargeNonprofitsAreNotFlagged reproduces
// two live examples that shaped this heuristic: the Wikimedia
// Foundation (2023: $4.1M officer comp / $168.3M total expenses, 2.5%)
// and MSF USA (2023: $3.1M / $856.5M, 0.4%) -- both well-run,
// well-known large nonprofits, both far below highOfficerCompensationRatio.
func TestHighOfficerCompensationRealLargeNonprofitsAreNotFlagged(t *testing.T) {
	wikimedia := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(4_145_477), TotalExpenses: int64Ptr(168_305_333)},
	}
	if desc := highOfficerCompensation(wikimedia); desc != "" {
		t.Errorf("got %q, want no flag for a 2.5%% ratio", desc)
	}

	msf := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(3_105_482), TotalExpenses: int64Ptr(856_531_073)},
	}
	if desc := highOfficerCompensation(msf); desc != "" {
		t.Errorf("got %q, want no flag for a 0.4%% ratio", desc)
	}
}

func TestHighOfficerCompensationFlagsRatioAboveThreshold(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(2_000_000), TotalExpenses: int64Ptr(5_000_000)}, // 40%
	}
	desc := highOfficerCompensation(filings)
	if desc == "" {
		t.Fatal("expected a flag for a 40% ratio above the 30% threshold")
	}
	if !strings.Contains(desc, "40%") {
		t.Errorf("description = %q, want it to mention the 40%% ratio", desc)
	}
}

func TestHighOfficerCompensationSkipsBelowExpenseFloor(t *testing.T) {
	// A single paid founder can legitimately be ~100% of a tiny
	// budget -- the expense floor exists specifically so a small or
	// all-volunteer organization isn't flagged for this.
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(45_000), TotalExpenses: int64Ptr(50_000)}, // 90%, but tiny
	}
	if desc := highOfficerCompensation(filings); desc != "" {
		t.Errorf("got %q, want no flag below the expense floor regardless of ratio", desc)
	}
}

func TestHighOfficerCompensationSkipsMissingFigures(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: nil, TotalExpenses: int64Ptr(5_000_000)},
		{TaxYear: 2022, OfficerCompensation: int64Ptr(2_000_000), TotalExpenses: nil},
	}
	if desc := highOfficerCompensation(filings); desc != "" {
		t.Errorf("got %q, want no flag when either figure is missing", desc)
	}
}

func TestHighOfficerCompensationUsesFirstQualifyingFilingNewestFirst(t *testing.T) {
	filings := []nonprofit.Filing{
		{TaxYear: 2023, OfficerCompensation: int64Ptr(500_000), TotalExpenses: int64Ptr(5_000_000)},   // 10%, no flag
		{TaxYear: 2022, OfficerCompensation: int64Ptr(2_000_000), TotalExpenses: int64Ptr(5_000_000)}, // 40%, would flag, but shouldn't be reached
	}
	if desc := highOfficerCompensation(filings); desc != "" {
		t.Errorf("got %q, want the newest (2023) filing's 10%% ratio to win, not the older 40%%", desc)
	}
}

// TestFrequentRenamingRealTescoHistoryIsNotFlagged reproduces the
// live example that shaped this heuristic: Tesco PLC's two recorded
// renames span 36 years (1947->1983), well outside
// frequentRenamingWindow, so a normal decades-apart rebrand history
// must not be flagged.
func TestFrequentRenamingRealTescoHistoryIsNotFlagged(t *testing.T) {
	tesco := []companieshouse.PreviousName{
		{Name: "TESCO STORES (HOLDINGS) PUBLIC LIMITED COMPANY", EffectiveFrom: "1981-12-14", CeasedOn: "1983-08-25"},
		{Name: "TESCO STORES (HOLDINGS) LIMITED", EffectiveFrom: "1947-11-27", CeasedOn: "1981-12-14"},
	}
	if desc := frequentRenaming(tesco); desc != "" {
		t.Errorf("got %q, want no flag for a 36-year rename history", desc)
	}
}

func TestFrequentRenamingFlagsFastRenamingPattern(t *testing.T) {
	fast := []companieshouse.PreviousName{
		{Name: "THIRD NAME LTD", EffectiveFrom: "2023-06-01", CeasedOn: "2024-01-01"},
		{Name: "SECOND NAME LTD", EffectiveFrom: "2022-12-01", CeasedOn: "2023-06-01"},
		{Name: "FIRST NAME LTD", EffectiveFrom: "2022-07-01", CeasedOn: "2022-12-01"},
	}
	desc := frequentRenaming(fast)
	if desc == "" {
		t.Fatal("expected a flag for 3 renames within 18 months")
	}
	if !strings.Contains(desc, "3 name changes") {
		t.Errorf("description = %q, want it to mention 3 name changes", desc)
	}
}

func TestFrequentRenamingRequiresAtLeastTwoPreviousNames(t *testing.T) {
	single := []companieshouse.PreviousName{
		{Name: "ONLY PREVIOUS NAME LTD", EffectiveFrom: "2023-01-01", CeasedOn: "2023-06-01"},
	}
	if desc := frequentRenaming(single); desc != "" {
		t.Errorf("got %q, want no flag for a single rename regardless of how recent", desc)
	}
}

func TestFrequentRenamingSkipsUnparseableDates(t *testing.T) {
	names := []companieshouse.PreviousName{
		{Name: "NAME A LTD", EffectiveFrom: "not-a-date", CeasedOn: "2023-06-01"},
		{Name: "NAME B LTD", EffectiveFrom: "2022-07-01", CeasedOn: "also-not-a-date"},
	}
	// Both entries have an unparseable date, so neither contributes to
	// the oldest/most-recent span -- this must not panic and must
	// return no flag rather than a false one built from zero times.
	if desc := frequentRenaming(names); desc != "" {
		t.Errorf("got %q, want no flag when no entry has both dates parseable", desc)
	}
}
