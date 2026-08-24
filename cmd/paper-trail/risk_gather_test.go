package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bennett-17/paper-trail/internal/companieshouse"
	"github.com/bennett-17/paper-trail/internal/nonprofit"
	"github.com/bennett-17/paper-trail/internal/nzbn"
	"github.com/bennett-17/paper-trail/internal/ofsi"
	"github.com/bennett-17/paper-trail/internal/risk"
	"github.com/bennett-17/paper-trail/internal/riskcache"
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

// skipOFSISubScreens disables officerFanOut's and
// pscSanctionsAdjacentChange's own OFSI screens -- neither has a fake
// server to point at (ofsi.NewClient always dials the real, live OFSI
// endpoint, unlike companieshouse.Client's injectable BaseURL), so
// every test exercising a fixture with named officers/PSCs needs this
// passed in place of a nil filter to stay offline. See
// TestOfficerFanOutOFSIScreenSkippedUnderFastMode for the dedicated
// test of the skip mechanism itself.
var skipOFSISubScreens = &sourceFilter{skip: map[string]bool{
	"UK sanctions screen (officer fan-out)": true,
	"UK sanctions screen (PSC)":             true,
}}

func newChainTestClient(t *testing.T, srv *httptest.Server) *companieshouse.Client {
	t.Helper()
	c, err := companieshouse.NewClient("test-api-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.BaseURL = srv.URL
	return c
}

// TestOfficerFanOutDiscoversHop1AndHop2Companies models the exact
// real-world pattern this function exists to catch: a root company's
// officer (Jane Smith) also directs a second company (hop 1), which in
// turn has its own officer (John Doe) directing a third, otherwise
// entirely unconnected company (hop 2) -- the same two-hop chain that
// found the real Narconon Trust / UK Buildings and Land Ltd / Church
// of Scientology (England and Wales) network via a single shared
// director.
func TestOfficerFanOutDiscoversHop1AndHop2Companies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/off1/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2020-01-01","appointed_to":{"company_name":"HOP ONE LTD","company_number":"00000020"}}],"total_results":1}`)
	})
	mux.HandleFunc("/company/00000020/officers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"name":"John Doe","officer_role":"director","appointed_on":"2019-01-01","links":{"officer":{"appointments":"/officers/off2/appointments"}}}],"total_results":1}`)
	})
	mux.HandleFunc("/officers/off2/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2018-01-01","appointed_to":{"company_name":"HOP TWO LTD","company_number":"00000030"}}],"total_results":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newChainTestClient(t, srv)

	root := []companieshouse.Officer{{Name: "Jane Smith", OfficerID: "off1"}}
	fanned, extra := officerFanOut(c, "00000010", root, 0, "companieshouse: Root Ltd (00000010)", skipOFSISubScreens, newOfficerLookupCache(), func(string, ...any) {})

	if len(fanned) != 2 {
		t.Fatalf("got %d fanned-out entities, want 2 (hop 1 and hop 2): %+v", len(fanned), fanned)
	}
	var gotNumbers []string
	for _, e := range fanned {
		gotNumbers = append(gotNumbers, e.ID)
	}
	if gotNumbers[0] != "00000020" || gotNumbers[1] != "00000030" {
		t.Errorf("fanned-out company numbers = %v, want [00000020, 00000030]", gotNumbers)
	}
	if extra != nil {
		t.Errorf("extra = %v, want nil (no appointment/resignation burst pattern in this fixture)", extra)
	}
}

func TestOfficerFanOutExcludesRootCompanyItself(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/off1/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2020-01-01","appointed_to":{"company_name":"ROOT LTD","company_number":"00000010"}}],"total_results":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newChainTestClient(t, srv)

	root := []companieshouse.Officer{{Name: "Jane Smith", OfficerID: "off1"}}
	fanned, _ := officerFanOut(c, "00000010", root, 0, "companieshouse: Root Ltd (00000010)", skipOFSISubScreens, newOfficerLookupCache(), func(string, ...any) {})
	if len(fanned) != 0 {
		t.Errorf("got %d fanned-out entities, want 0 (the only appointment is the root company itself)", len(fanned))
	}
}

func TestOfficerFanOutIgnoresResignedAppointments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/off1/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2010-01-01","resigned_on":"2015-01-01","appointed_to":{"company_name":"FORMER LTD","company_number":"00000099"}}],"total_results":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newChainTestClient(t, srv)

	root := []companieshouse.Officer{{Name: "Jane Smith", OfficerID: "off1"}}
	fanned, _ := officerFanOut(c, "00000010", root, 0, "companieshouse: Root Ltd (00000010)", skipOFSISubScreens, newOfficerLookupCache(), func(string, ...any) {})
	if len(fanned) != 0 {
		t.Errorf("got %d fanned-out entities, want 0 (a resigned/former appointment shouldn't fan out)", len(fanned))
	}
}

// TestOfficerLookupCacheGetAppointmentsOnlyFetchesOnce is the direct
// test of the redundancy officerLookupCache exists to avoid: the same
// OfficerID requested twice (simulating the same nominee recurring as
// a current officer of two different root companies within one
// gatherer call) must hit the fixture server's appointments endpoint
// once, not twice.
func TestOfficerLookupCacheGetAppointmentsOnlyFetchesOnce(t *testing.T) {
	var requestCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/off1/appointments", func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2020-01-01","appointed_to":{"company_name":"A LTD","company_number":"1"}}],"total_results":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newChainTestClient(t, srv)

	cache := newOfficerLookupCache()
	first, firstTotal, err := cache.getAppointments(c, "off1", 0)
	if err != nil {
		t.Fatalf("first getAppointments: %v", err)
	}
	second, secondTotal, err := cache.getAppointments(c, "off1", 0)
	if err != nil {
		t.Fatalf("second getAppointments: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("requestCount = %d, want 1 (second call should be served from cache)", requestCount)
	}
	if len(first) != 1 || len(second) != 1 || first[0].CompanyNumber != second[0].CompanyNumber {
		t.Errorf("first = %+v, second = %+v, want identical cached results", first, second)
	}
	if firstTotal != 1 || secondTotal != 1 {
		t.Errorf("firstTotal = %d, secondTotal = %d, want 1 both times", firstTotal, secondTotal)
	}
}

// TestOfficerLookupCacheGetAppointmentsCachesErrorsToo confirms a
// failed lookup isn't retried on a second request for the same
// OfficerID -- once we know it fails this scan, asking again wastes
// another round-trip for the same answer.
func TestOfficerLookupCacheGetAppointmentsCachesErrorsToo(t *testing.T) {
	var requestCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/broken/appointments", func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newChainTestClient(t, srv)

	cache := newOfficerLookupCache()
	if _, _, err := cache.getAppointments(c, "broken", 0); err == nil {
		t.Fatal("first getAppointments: got nil error, want one from the 500 response")
	}
	if _, _, err := cache.getAppointments(c, "broken", 0); err == nil {
		t.Fatal("second getAppointments: got nil error, want the cached error")
	}
	if requestCount != 1 {
		t.Errorf("requestCount = %d, want 1 (the cached error shouldn't be retried)", requestCount)
	}
}

// TestOfficerLookupCacheGetAppointmentsKeepsDistinctOfficersSeparate
// guards against a cache keyed incorrectly (e.g. by limit or by
// nothing at all) silently returning one officer's data for another.
func TestOfficerLookupCacheGetAppointmentsKeepsDistinctOfficersSeparate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/off1/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2020-01-01","appointed_to":{"company_name":"A LTD","company_number":"1"}}],"total_results":1}`)
	})
	mux.HandleFunc("/officers/off2/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2021-01-01","appointed_to":{"company_name":"B LTD","company_number":"2"}}],"total_results":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newChainTestClient(t, srv)

	cache := newOfficerLookupCache()
	a, _, _ := cache.getAppointments(c, "off1", 0)
	b, _, _ := cache.getAppointments(c, "off2", 0)
	if len(a) != 1 || a[0].CompanyNumber != "1" {
		t.Errorf("off1's cached appointments = %+v, want company 1", a)
	}
	if len(b) != 1 || b[0].CompanyNumber != "2" {
		t.Errorf("off2's cached appointments = %+v, want company 2", b)
	}
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
	// This 3-company cluster deliberately no longer fires. The threshold
	// was 3, reasoned from exactly this case; measuring 1,202 officers
	// showed burst sizes of 2-4 are the ordinary tail of normal
	// behaviour (78% of officers sit at 1) and firing at 3 flagged 13.8%
	// of companies. The trade-off is real and accepted: this officer has
	// 540 register-wide appointments and is caught by
	// mass_nominee_officer regardless, so nothing about the reference
	// case goes unreported -- see appointmentBurstThreshold's comment.
	if desc, _ := appointmentBurst(appointments); desc != "" {
		t.Errorf("desc = %q, want no burst -- 3 companies is inside the ordinary range at the measured threshold", desc)
	}

	// The same officer's real history also contains larger clusters,
	// which are what the indicator now exists to catch.
	bigger := append(appointments,
		companieshouse.Appointment{CompanyNumber: "00000005", CompanyName: "FIFTH LTD", AppointedOn: "2014-12-09"},
		companieshouse.Appointment{CompanyNumber: "00000006", CompanyName: "SIXTH LTD", AppointedOn: "2014-12-10"},
		companieshouse.Appointment{CompanyNumber: "00000007", CompanyName: "SEVENTH LTD", AppointedOn: "2014-12-11"},
	)
	desc, _ := appointmentBurst(bigger)
	if desc == "" {
		t.Fatal("got no flag, want a burst for 6 companies inside one week")
	}
	if !strings.Contains(desc, "6 companies") {
		t.Errorf("desc = %q, want it to report 6 companies", desc)
	}
}

// TestAppointmentBurstSizeMatchesThreshold guards that the measured
// quantity and the threshold that cuts it stay in agreement -- they are
// computed by different functions (largestBurst vs burstDescription)
// and a drift between them would silently invalidate any future
// calibration of this threshold.
func TestAppointmentBurstSizeMatchesThreshold(t *testing.T) {
	mk := func(n int) []companieshouse.Appointment {
		var out []companieshouse.Appointment
		for i := 0; i < n; i++ {
			out = append(out, companieshouse.Appointment{
				CompanyNumber: fmt.Sprintf("%08d", i+1),
				CompanyName:   fmt.Sprintf("CO %d LTD", i+1),
				AppointedOn:   "2014-12-09",
			})
		}
		return out
	}
	for n := 1; n <= 8; n++ {
		size := appointmentBurstSize(mk(n))
		if size != n {
			t.Errorf("appointmentBurstSize(%d same-day) = %d, want %d", n, size, n)
		}
		desc, _ := appointmentBurst(mk(n))
		fired := desc != ""
		if want := n >= appointmentBurstThreshold; fired != want {
			t.Errorf("n=%d: fired=%v, want %v (threshold %d)", n, fired, want, appointmentBurstThreshold)
		}
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
	if desc, _ := appointmentBurst(appointments); desc != "" {
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
	if desc, _ := appointmentBurst(appointments); desc != "" {
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
	if desc, _ := appointmentBurst(appointments); desc != "" {
		t.Errorf("got %q, want no flag when only 1 of 3 appointments has a parseable date", desc)
	}
}

// TestResignationBurstFlagsRealCorporateNomineeDirectorPattern is
// modeled directly on real, live-confirmed resignation history for the
// same Companies House officer ID nEggfu04XePBqnRERobPjXjmHGk
// ("Corporate Directors Limited") appointmentBurst's own tests cite --
// its resignations cluster even more tightly than its appointments do:
// four separate companies (Burndell Limited, Coldbrook Services
// Limited, Courtwick Services Ltd, Ventmor Ltd) all had this same
// corporate director resign on 2016-04-27 alone, part of a wider wave
// of 8 distinct companies within that single week.
func TestResignationBurstFlagsRealCorporateNomineeDirectorPattern(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "09483243", CompanyName: "OLDWICK SERVICES LIMITED", AppointedOn: "2015-03-11", ResignedOn: "2016-04-25"},
		{CompanyNumber: "09747142", CompanyName: "PULLAST CONSULTING LTD", AppointedOn: "2015-08-25", ResignedOn: "2016-04-26"},
		{CompanyNumber: "09347433", CompanyName: "ROUNDSTONE NETWORK LTD.", AppointedOn: "2014-12-09", ResignedOn: "2016-04-26"},
		{CompanyNumber: "09615136", CompanyName: "BURNDELL LIMITED", AppointedOn: "2015-05-29", ResignedOn: "2016-04-27"},
		{CompanyNumber: "09157902", CompanyName: "COLDBROOK SERVICES LIMITED", AppointedOn: "2014-08-01", ResignedOn: "2016-04-27"},
		{CompanyNumber: "09463602", CompanyName: "COURTWICK SERVICES LTD", AppointedOn: "2015-02-27", ResignedOn: "2016-04-27"},
		{CompanyNumber: "09262162", CompanyName: "VENTMOR LTD", AppointedOn: "2014-10-14", ResignedOn: "2016-04-27"},
		{CompanyNumber: "09461143", CompanyName: "VERITLEX SERVICES LIMITED", AppointedOn: "2015-02-26", ResignedOn: "2016-04-28"},
		// Still active -- no resignation date, must not contribute.
		{CompanyNumber: "00000099", CompanyName: "STILL ACTIVE LTD", AppointedOn: "2014-01-01"},
	}
	desc, _ := resignationBurst(appointments)
	if desc == "" {
		t.Fatal("got no flag, want a resignation burst flagged for the April 2016 cluster")
	}
	if !strings.Contains(desc, "resigned from") {
		t.Errorf("desc = %q, want it to describe a resignation, not an appointment", desc)
	}
	if !strings.Contains(desc, "8 companies") {
		t.Errorf("desc = %q, want it to report all 8 companies within the 7-day window", desc)
	}
}

func TestResignationBurstIgnoresStillActiveAppointments(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "00000001", CompanyName: "A LTD", AppointedOn: "2020-01-01"},
		{CompanyNumber: "00000002", CompanyName: "B LTD", AppointedOn: "2020-01-01"},
		{CompanyNumber: "00000003", CompanyName: "C LTD", AppointedOn: "2020-01-01"},
	}
	// None of these have a ResignedOn at all -- a burst of new
	// appointments must not be mistaken for a resignation burst.
	if desc, _ := resignationBurst(appointments); desc != "" {
		t.Errorf("got %q, want no flag: nothing here has actually resigned", desc)
	}
}

func TestResignationBurstIgnoresOrdinaryResignationsSpreadOverYears(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "00000001", CompanyName: "FIRST LTD", AppointedOn: "2005-01-01", ResignedOn: "2010-01-01"},
		{CompanyNumber: "00000002", CompanyName: "SECOND LTD", AppointedOn: "2010-01-01", ResignedOn: "2014-06-15"},
		{CompanyNumber: "00000003", CompanyName: "THIRD LTD", AppointedOn: "2014-06-15", ResignedOn: "2019-11-30"},
	}
	if desc, _ := resignationBurst(appointments); desc != "" {
		t.Errorf("got %q, want no flag for resignations spread over years", desc)
	}
}

func TestMassNomineeOfficerFiresAtThreshold(t *testing.T) {
	if count := massNomineeOfficer(massNomineeOfficerThreshold); count != massNomineeOfficerThreshold {
		t.Errorf("massNomineeOfficer(%d) = %d, want %d (threshold itself should fire)", massNomineeOfficerThreshold, count, massNomineeOfficerThreshold)
	}
}

func TestMassNomineeOfficerIgnoresBelowThreshold(t *testing.T) {
	if count := massNomineeOfficer(massNomineeOfficerThreshold - 1); count != 0 {
		t.Errorf("massNomineeOfficer(%d) = %d, want 0 (one below threshold)", massNomineeOfficerThreshold-1, count)
	}
}

func TestMassNomineeOfficerReturnsRealisticMassNomineeCount(t *testing.T) {
	// Modeled on this project's own real reference case, "Corporate
	// Directors Limited" (see massNomineeOfficerThreshold's doc
	// comment): 540 appointments register-wide.
	if count := massNomineeOfficer(540); count != 540 {
		t.Errorf("massNomineeOfficer(540) = %d, want 540", count)
	}
}

func TestMassNomineeOfficerIgnoresOrdinaryFewDirectorships(t *testing.T) {
	if count := massNomineeOfficer(3); count != 0 {
		t.Errorf("massNomineeOfficer(3) = %d, want 0: a handful of directorships is completely normal", count)
	}
}

func TestSanctionsAdjacentChangeFlagsAppointmentNearDesignation(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "1", CompanyName: "A LTD", AppointedOn: "2019-01-01"},
		{CompanyNumber: "2", CompanyName: "B LTD", AppointedOn: "2022-05-01"}, // 25 days after designation
	}
	hit := ofsi.Hit{Name: "Jane Doe", Regime: "Russia", DateDesignated: "2022-04-06"}
	desc, _ := sanctionsAdjacentChange(appointmentsToDatedRecords(appointments), hit)
	if desc == "" {
		t.Fatal("got no flag, want one: an appointment fell 25 days after the designation date")
	}
	if !strings.Contains(desc, "B LTD") || !strings.Contains(desc, "days after") {
		t.Errorf("desc = %q, want it to name B LTD and describe the gap as days after", desc)
	}
}

func TestSanctionsAdjacentChangeFlagsResignationBeforeDesignation(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "1", CompanyName: "A LTD", AppointedOn: "2018-01-01", ResignedOn: "2022-03-01"}, // 36 days before designation
	}
	hit := ofsi.Hit{Name: "Jane Doe", Regime: "Russia", DateDesignated: "2022-04-06"}
	desc, _ := sanctionsAdjacentChange(appointmentsToDatedRecords(appointments), hit)
	if desc == "" {
		t.Fatal("got no flag, want one: a resignation fell 36 days before the designation date")
	}
	if !strings.Contains(desc, "resigned from A LTD") || !strings.Contains(desc, "days before") {
		t.Errorf("desc = %q, want it to describe a resignation before the designation", desc)
	}
}

func TestSanctionsAdjacentChangeIgnoresDatesOutsideWindow(t *testing.T) {
	appointments := []companieshouse.Appointment{
		{CompanyNumber: "1", CompanyName: "A LTD", AppointedOn: "2015-01-01"},
		{CompanyNumber: "2", CompanyName: "B LTD", AppointedOn: "2022-12-01", ResignedOn: "2023-06-01"},
	}
	hit := ofsi.Hit{Name: "Jane Doe", Regime: "Russia", DateDesignated: "2022-04-06"}
	if desc, _ := sanctionsAdjacentChange(appointmentsToDatedRecords(appointments), hit); desc != "" {
		t.Errorf("got %q, want no flag: nothing falls within 90 days of the designation date", desc)
	}
}

func TestSanctionsAdjacentChangeExactBoundary(t *testing.T) {
	// Exactly 90 days after should still count (<=, not <); 91 should not.
	hit := ofsi.Hit{Name: "Jane Doe", Regime: "Russia", DateDesignated: "2022-01-01"}
	within := []companieshouse.Appointment{{CompanyNumber: "1", CompanyName: "A LTD", AppointedOn: "2022-04-01"}} // exactly 90 days
	if desc, _ := sanctionsAdjacentChange(appointmentsToDatedRecords(within), hit); desc == "" {
		t.Error("got no flag at exactly 90 days, want one (boundary is inclusive)")
	}
	outside := []companieshouse.Appointment{{CompanyNumber: "1", CompanyName: "A LTD", AppointedOn: "2022-04-02"}} // 91 days
	if desc, _ := sanctionsAdjacentChange(appointmentsToDatedRecords(outside), hit); desc != "" {
		t.Errorf("got %q at 91 days, want no flag", desc)
	}
}

func TestSanctionsAdjacentChangeSkipsUnparseableDesignationDate(t *testing.T) {
	appointments := []companieshouse.Appointment{{CompanyNumber: "1", CompanyName: "A LTD", AppointedOn: "2022-04-01"}}
	hit := ofsi.Hit{Name: "Jane Doe", DateDesignated: "not-a-date"}
	if desc, _ := sanctionsAdjacentChange(appointmentsToDatedRecords(appointments), hit); desc != "" {
		t.Errorf("got %q, want no flag for an unparseable designation date", desc)
	}
}

func TestSanctionsAdjacentChangeFlagsPSCNotifiedNearDesignation(t *testing.T) {
	// The PSC (beneficial-ownership) case: a single company's own PSC
	// record, notified 12 days before their OFSI designation.
	records := []datedRecord{{CompanyName: "SHELL LTD", StartOn: "2022-03-25", StartVerb: "notified as a PSC of", EndOn: "", EndVerb: "ceased being a PSC of"}}
	hit := ofsi.Hit{Name: "Jane Doe", Regime: "Russia", DateDesignated: "2022-04-06"}
	desc, _ := sanctionsAdjacentChange(records, hit)
	if desc == "" {
		t.Fatal("got no flag, want one: notified 12 days before the designation date")
	}
	if !strings.Contains(desc, "notified as a PSC of SHELL LTD") || !strings.Contains(desc, "days before") {
		t.Errorf("desc = %q, want PSC-specific wording", desc)
	}
}

func TestSanctionsAdjacentChangeFlagsPSCCeasedNearDesignation(t *testing.T) {
	records := []datedRecord{{CompanyName: "SHELL LTD", StartOn: "2010-01-01", StartVerb: "notified as a PSC of", EndOn: "2022-04-20", EndVerb: "ceased being a PSC of"}} // 14 days after
	hit := ofsi.Hit{Name: "Jane Doe", Regime: "Russia", DateDesignated: "2022-04-06"}
	desc, _ := sanctionsAdjacentChange(records, hit)
	if desc == "" {
		t.Fatal("got no flag, want one: ceased 14 days after the designation date")
	}
	if !strings.Contains(desc, "ceased being a PSC of SHELL LTD") || !strings.Contains(desc, "days after") {
		t.Errorf("desc = %q, want PSC-specific ceased wording", desc)
	}
}

func TestDaysRelativeFormatsSignAndZero(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{12 * 24 * time.Hour, "12 days after"},
		{-40 * 24 * time.Hour, "40 days before"},
		{0, "the same day as"},
	}
	for _, c := range cases {
		if got := daysRelative(c.d); got != c.want {
			t.Errorf("daysRelative(%v) = %q, want %q", c.d, got, c.want)
		}
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

// TestTrustControlledNaturesFlagsOrdinaryDomesticCode is modeled on
// Companies House's real, published PSC nature-of-control codelist for
// an ordinary UK-incorporated company's PSC.
func TestForeignCallingCodeFlagsRecognizedNonUKCode(t *testing.T) {
	country, ok := foreignCallingCode("+1 212 555 0100")
	if !ok || country != "US/Canada" {
		t.Errorf("got (%q, %v), want (US/Canada, true)", country, ok)
	}
}

func TestForeignCallingCodeAccepts00Prefix(t *testing.T) {
	country, ok := foreignCallingCode("00353 1 234 5678")
	if !ok || country != "Ireland" {
		t.Errorf("got (%q, %v), want (Ireland, true)", country, ok)
	}
}

func TestForeignCallingCodeIgnoresUKNumber(t *testing.T) {
	if _, ok := foreignCallingCode("+44 20 7946 0991"); ok {
		t.Error("got ok=true for a UK number, want false")
	}
}

func TestForeignCallingCodeIgnoresNationalFormat(t *testing.T) {
	// The overwhelming common case: a UK charity's own number listed
	// in plain national format with no international prefix at all --
	// ambiguous, not evidence of anything foreign.
	if _, ok := foreignCallingCode("020 7946 0991"); ok {
		t.Error("got ok=true for a plain national-format number, want false")
	}
}

func TestForeignCallingCodeIgnoresUnrecognizedCode(t *testing.T) {
	// A well-formed international number whose calling code just
	// isn't in the hand-maintained table -- deliberately not guessed
	// at.
	if _, ok := foreignCallingCode("+999 1 234 5678"); ok {
		t.Error("got ok=true for an unrecognized calling code, want false")
	}
}

func TestTrustControlledNaturesFlagsOrdinaryDomesticCode(t *testing.T) {
	natures := []string{"ownership-of-shares-25-to-50-percent-as-trust"}
	matched := trustControlledNatures(natures)
	if len(matched) != 1 || matched[0] != natures[0] {
		t.Errorf("got %v, want %v flagged", matched, natures)
	}
}

// TestTrustControlledNaturesFlagsOverseasEntityCode is modeled on the
// real, live-verified Mulberry Investments Limited (OE007240)
// beneficial-owner record, whose nature-of-control codes carry the
// Register of Overseas Entities' own "-registered-overseas-entity"
// suffix in addition to "-as-trust".
func TestTrustControlledNaturesFlagsOverseasEntityCode(t *testing.T) {
	natures := []string{"ownership-of-shares-more-than-25-percent-as-trust-registered-overseas-entity"}
	if matched := trustControlledNatures(natures); len(matched) != 1 {
		t.Errorf("got %v, want the ROE trust code flagged", matched)
	}
}

// TestTrustControlledNaturesIgnoresDirectOwnership guards the common
// case -- a PSC controlling a company directly (no trust involved at
// all) must not be flagged.
func TestTrustControlledNaturesIgnoresDirectOwnership(t *testing.T) {
	natures := []string{"ownership-of-shares-75-to-100-percent", "voting-rights-75-to-100-percent"}
	if matched := trustControlledNatures(natures); len(matched) != 0 {
		t.Errorf("got %v, want none flagged (no trust involvement)", matched)
	}
}

// TestTrustControlledNaturesIgnoresNegativeTrustStatement guards
// against a plain "trust" substring check false-positiving on
// Companies House's separate annual-statement field, which explicitly
// says trust was NOT involved.
func TestTrustControlledNaturesIgnoresNegativeTrustStatement(t *testing.T) {
	natures := []string{"no-trust-involved-relevant-period"}
	if matched := trustControlledNatures(natures); len(matched) != 0 {
		t.Errorf("got %v, want none flagged (this is a negative statement, not a nature-of-control code)", matched)
	}
}

// TestPSCOpacityIndicatorsFlagsActiveStatementWithCharges is modeled
// on a real, live-verified example: Northern Ireland Association of
// Citizens Advice Bureaux Limited (NI017574) carries exactly this
// statement alongside outstanding mortgage charges.
func TestPSCOpacityIndicatorsFlagsActiveStatementWithCharges(t *testing.T) {
	statements := []companieshouse.PSCStatement{
		{Statement: companieshouse.NoSignificantControlStatement, NotifiedOn: "2017-01-14"},
	}
	got := pscOpacityIndicators(statements, 4, "companieshouse: Example Ltd (NI017574)")
	if len(got) != 1 {
		t.Fatalf("got %d indicators, want 1: %+v", len(got), got)
	}
	if got[0].Code != "psc_opacity_with_active_charges" {
		t.Errorf("Code = %q", got[0].Code)
	}
	if !strings.Contains(got[0].Evidence, "4 outstanding charge") {
		t.Errorf("Evidence = %q, want it to cite the charge count", got[0].Evidence)
	}
}

func TestPSCOpacityIndicatorsRequiresOutstandingCharges(t *testing.T) {
	statements := []companieshouse.PSCStatement{
		{Statement: companieshouse.NoSignificantControlStatement, NotifiedOn: "2017-01-14"},
	}
	if got := pscOpacityIndicators(statements, 0, "companieshouse: Example Ltd (1)"); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (no outstanding charges at all)", len(got))
	}
}

func TestPSCOpacityIndicatorsIgnoresCeasedStatement(t *testing.T) {
	statements := []companieshouse.PSCStatement{
		{Statement: companieshouse.NoSignificantControlStatement, NotifiedOn: "2017-01-14", CeasedOn: "2020-01-01"},
	}
	if got := pscOpacityIndicators(statements, 4, "companieshouse: Example Ltd (1)"); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (statement is no longer active)", len(got))
	}
}

func TestPSCOpacityIndicatorsIgnoresOtherStatementTypes(t *testing.T) {
	statements := []companieshouse.PSCStatement{
		{Statement: "psc-exists-but-not-identified", NotifiedOn: "2017-01-14"},
	}
	if got := pscOpacityIndicators(statements, 4, "companieshouse: Example Ltd (1)"); len(got) != 0 {
		t.Errorf("got %d indicators, want 0 (a different statement type, not \"no PSC at all\")", len(got))
	}
}

// TestDormantSICWithChargesIndicatorFlagsNonTradingCode is modeled on
// a real, live-verified example: ALCALI LTD (SC312375) declares SIC
// code 74990 (non-trading), is active, and carries one outstanding
// "Standard security" charge from 2008.
func TestDormantSICWithChargesIndicatorFlagsNonTradingCode(t *testing.T) {
	ind := dormantSICWithChargesIndicator([]string{"74990"}, 1, "companieshouse: Alcali Ltd (SC312375)")
	if ind == nil {
		t.Fatal("expected an indicator, got nil")
	}
	if ind.Code != "dormant_sic_with_charges" {
		t.Errorf("Code = %q", ind.Code)
	}
	if !strings.Contains(ind.Evidence, "74990") {
		t.Errorf("Evidence = %q, want it to cite the SIC code", ind.Evidence)
	}
}

func TestDormantSICWithChargesIndicatorFlagsDormantCode(t *testing.T) {
	ind := dormantSICWithChargesIndicator([]string{"99999"}, 2, "companieshouse: Example Ltd (1)")
	if ind == nil {
		t.Fatal("expected an indicator, got nil")
	}
}

func TestDormantSICWithChargesIndicatorRequiresOutstandingCharges(t *testing.T) {
	if ind := dormantSICWithChargesIndicator([]string{"99999"}, 0, "companieshouse: Example Ltd (1)"); ind != nil {
		t.Errorf("got %+v, want nil (no outstanding charges at all)", ind)
	}
}

func TestDormantSICWithChargesIndicatorIgnoresOrdinaryIndustryCode(t *testing.T) {
	if ind := dormantSICWithChargesIndicator([]string{"62012"}, 2, "companieshouse: Example Ltd (1)"); ind != nil {
		t.Errorf("got %+v, want nil (an ordinary industry code, not dormant/non-trading)", ind)
	}
}

// emptyCompaniesHouseSubResources registers 200-empty handlers for
// every per-company endpoint gatherCompaniesHouseEntities calls
// (officers, PSCs, charges, profile) for the given company number --
// a test fixture helper so a test only needs to override the specific
// endpoint(s) it cares about.
func emptyCompaniesHouseSubResources(mux *http.ServeMux, number string) {
	mux.HandleFunc("/company/"+number+"/officers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"total_results":0}`)
	})
	mux.HandleFunc("/company/"+number+"/persons-with-significant-control", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"total_results":0}`)
	})
	mux.HandleFunc("/company/"+number+"/charges", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total_count":0,"unfiltered_count":0,"satisfied_count":0,"part_satisfied_count":0,"items":[]}`)
	})
	mux.HandleFunc("/company/"+number, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"company_name":"PLACEHOLDER","company_number":%q,"company_status":"active","type":"ltd"}`, number)
	})
}

// TestGatherCompaniesHouseEntitiesProcessesEveryCompanyType is modeled
// on the real, live-verified shape of a Register of Overseas Entities
// hit (Mulberry Investments Limited, OE007240, Jersey) alongside an
// ordinary company -- confirmed live against a real investigation
// (Narconon Trust's own property-holding sibling, UK Buildings and
// Land Ltd) that an ordinary company hit must NOT be skipped the way
// this function's predecessor (gatherOverseasEntities) used to: a
// shared director on an otherwise-unremarkable company is exactly how
// that real network was found.
func TestGatherCompaniesHouseEntitiesProcessesEveryCompanyType(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/companies", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"items": [
				{"company_number": "12345678", "title": "ORDINARY LTD", "company_status": "active", "company_type": "ltd", "date_of_creation": "2020-01-01", "address": {"address_line_1": "1 High St", "locality": "London"}},
				{"company_number": "OE007240", "title": "OVERSEAS HOLDCO LIMITED", "company_status": "registered", "company_type": "registered-overseas-entity", "date_of_creation": "2022-12-09", "address": {"address_line_1": "Standard Bank House", "locality": "St. Helier", "country": "Jersey"}}
			],
			"total_results": 2
		}`)
	})
	emptyCompaniesHouseSubResources(mux, "12345678")
	mux.HandleFunc("/company/OE007240/persons-with-significant-control", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"items": [
				{"name": "Sanctioned Person", "kind": "individual-beneficial-owner", "notified_on": "2023-01-01", "is_sanctioned": true},
				{"name": "Clean Person", "kind": "individual-beneficial-owner", "notified_on": "2023-01-01", "is_sanctioned": false}
			],
			"total_results": 2
		}`)
	})
	mux.HandleFunc("/company/OE007240/officers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"total_results":0}`)
	})
	mux.HandleFunc("/company/OE007240/charges", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total_count":0,"unfiltered_count":0,"satisfied_count":0,"part_satisfied_count":0,"items":[]}`)
	})
	mux.HandleFunc("/company/OE007240", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"company_name": "OVERSEAS HOLDCO LIMITED",
			"company_number": "OE007240",
			"company_status": "registered",
			"type": "registered-overseas-entity",
			"date_of_creation": "2022-12-09",
			"foreign_company_details": {"originating_registry": {"name": "Jersey Financial Services Commission,Jersey", "country": "JERSEY"}}
		}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	chClient := newChainTestClient(t, srv)

	entities, extra, _ := gatherCompaniesHouseEntities(chClient, []string{"Overseas Holdco"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard), skipOFSISubScreens)

	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2 (both the ordinary and the ROE hit should be processed): %+v", len(entities), entities)
	}
	var ordinary, overseasEntity *risk.Entity
	for i := range entities {
		switch entities[i].ID {
		case "12345678":
			ordinary = &entities[i]
		case "OE007240":
			overseasEntity = &entities[i]
		}
	}
	if ordinary == nil {
		t.Fatal("the ordinary (non-ROE) company hit is missing entirely -- it must be processed, not skipped")
	}
	if overseasEntity == nil || len(overseasEntity.People) != 2 {
		t.Fatalf("overseas entity = %+v, want both beneficial owners", overseasEntity)
	}

	var overseas, sanctioned []risk.Indicator
	for _, ind := range extra {
		switch ind.Code {
		case "overseas_entity":
			overseas = append(overseas, ind)
		case "roe_beneficial_owner_sanctioned":
			sanctioned = append(sanctioned, ind)
		}
	}
	if len(overseas) != 1 {
		t.Fatalf("got %d overseas_entity indicators, want 1 -- only the ROE-type hit, not the ordinary company: %+v", len(overseas), extra)
	}
	if !strings.Contains(overseas[0].Evidence, "Jersey") {
		t.Errorf("Evidence = %q, want it to cite the Jersey originating registry", overseas[0].Evidence)
	}
	if len(sanctioned) != 1 {
		t.Fatalf("got %d roe_beneficial_owner_sanctioned indicators, want 1 (only the sanctioned owner, not the clean one): %+v", len(sanctioned), extra)
	}
	if !strings.Contains(sanctioned[0].Evidence, "Sanctioned Person") {
		t.Errorf("Evidence = %q, want it to name Sanctioned Person", sanctioned[0].Evidence)
	}
}

// TestGatherCompaniesHouseEntitiesFansOutSharedDirector is modeled on
// the real network this feature exists to catch: a directly-found
// company's own current officer also directs a second, otherwise
// entirely unrelated company that no query term named.
func TestGatherCompaniesHouseEntitiesFansOutSharedDirector(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/companies", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"company_number": "00000010", "title": "FOUND LTD", "company_status": "active", "company_type": "ltd", "date_of_creation": "2015-01-01"}],"total_results":1}`)
	})
	mux.HandleFunc("/company/00000010/officers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"name":"Jane Smith","officer_role":"director","appointed_on":"2015-01-01","links":{"officer":{"appointments":"/officers/off1/appointments"}}}],"total_results":1}`)
	})
	mux.HandleFunc("/company/00000010/persons-with-significant-control", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"total_results":0}`)
	})
	mux.HandleFunc("/company/00000010/charges", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total_count":0,"unfiltered_count":0,"satisfied_count":0,"part_satisfied_count":0,"items":[]}`)
	})
	mux.HandleFunc("/company/00000010", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"company_name":"FOUND LTD","company_number":"00000010","company_status":"active","type":"ltd"}`)
	})
	mux.HandleFunc("/officers/off1/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"officer_role":"director","appointed_on":"2018-01-01","appointed_to":{"company_name":"UNRELATED-LOOKING LTD","company_number":"00000099"}}],"total_results":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	chClient := newChainTestClient(t, srv)

	entities, _, _ := gatherCompaniesHouseEntities(chClient, []string{"Found Ltd"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard), skipOFSISubScreens)

	var foundFannedOut bool
	for _, e := range entities {
		if e.ID == "00000099" {
			foundFannedOut = true
		}
	}
	if !foundFannedOut {
		t.Fatalf("fanned-out company (00000099, reached via a shared director) missing from entities: %+v", entities)
	}
}

func TestGatherCompaniesHouseEntitiesNilClientReturnsNothing(t *testing.T) {
	entities, extra, notes := gatherCompaniesHouseEntities(nil, []string{"anything"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard), nil)
	if entities != nil || extra != nil || notes != nil {
		t.Errorf("got (%v, %v, %v), want all nil when chClient is nil", entities, extra, notes)
	}
}

func newNZBNTestClient(t *testing.T, srv *httptest.Server) *nzbn.Client {
	t.Helper()
	c, err := nzbn.NewClient("test-key")
	if err != nil {
		t.Fatalf("nzbn.NewClient: %v", err)
	}
	c.MinInterval = 0
	c.RetryBaseDelay = time.Millisecond
	c.BaseURL = srv.URL
	c.EntityRoleBaseURL = srv.URL
	return c
}

func TestGatherNZBNEntitiesFindsEntityAndFlagsInsolvencyStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalItems":1,"items":[{"nzbn":"9429041782718","entityName":"GRIZZLY LIMITED","entityStatusCode":"InLiquidation","entityStatusDescription":"In Liquidation","registrationDate":"2010-01-01"}]}`)
	})
	mux.HandleFunc("/entities/9429041782718", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"nzbn": "9429041782718",
			"entityName": "GRIZZLY LIMITED",
			"entityStatusCode": "InLiquidation",
			"entityStatusDescription": "In Liquidation",
			"addresses": {"addressList": [{"address1": "1 Queen Street", "postCode": "1010", "countryCode": "NZ", "endDate": ""}]},
			"roles": [{"roleType": "Director", "startDate": "2010-01-01", "endDate": "", "rolePerson": {"firstName": "Belinda", "lastName": "Smith"}}]
		}`)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalResults":0,"roles":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	nzClient := newNZBNTestClient(t, srv)

	entities, extra, _ := gatherNZBNEntities(nzClient, []string{"Grizzly"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard))

	if len(entities) != 1 || entities[0].ID != "9429041782718" || len(entities[0].People) != 1 || entities[0].People[0] != "Belinda Smith" {
		t.Fatalf("entities = %+v, want 1 entity with director Belinda Smith", entities)
	}
	var found bool
	for _, ind := range extra {
		if ind.Code == "nzbn_insolvency_status" {
			found = true
			if !strings.Contains(ind.Evidence, "In Liquidation") {
				t.Errorf("Evidence = %q, want it to cite the liquidation status", ind.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("expected nzbn_insolvency_status indicator, got %+v", extra)
	}
}

// TestGatherNZBNEntitiesFansOutSharedDirector is modeled on the real
// network this feature exists to catch, the NZ analogue of
// TestGatherCompaniesHouseEntitiesFansOutSharedDirector: a directly-
// found entity's own current director also directs a second,
// otherwise entirely unrelated entity that no query term named.
func TestGatherNZBNEntitiesFansOutSharedDirector(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalItems":1,"items":[{"nzbn":"111","entityName":"FOUND LIMITED","entityStatusCode":"Registered"}]}`)
	})
	mux.HandleFunc("/entities/111", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"nzbn": "111",
			"entityName": "FOUND LIMITED",
			"entityStatusCode": "Registered",
			"roles": [{"roleType": "Director", "endDate": "", "rolePerson": {"firstName": "Jane", "lastName": "Smith"}}]
		}`)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalResults":1,"roles":[{"firstName":"Jane","lastName":"Smith","roleType":"Director","associatedCompanyName":"UNRELATED-LOOKING LIMITED","associatedCompanyNzbn":222}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	nzClient := newNZBNTestClient(t, srv)

	entities, _, _ := gatherNZBNEntities(nzClient, []string{"Found Limited"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard))

	var foundFannedOut bool
	for _, e := range entities {
		if e.ID == "222" {
			foundFannedOut = true
		}
	}
	if !foundFannedOut {
		t.Fatalf("fanned-out entity (222, reached via a shared director) missing from entities: %+v", entities)
	}
}

// TestGatherNZBNEntitiesFanOutRejectsNameCollision confirms the
// exact-normalized-name gate documented on gatherNZBNEntities: a role
// search result for a different person who merely shares a name isn't
// accepted, since there's no stable per-person ID in this API to
// disambiguate the way Companies House's officer ID does.
func TestGatherNZBNEntitiesFanOutRejectsNameCollision(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalItems":1,"items":[{"nzbn":"111","entityName":"FOUND LIMITED","entityStatusCode":"Registered"}]}`)
	})
	mux.HandleFunc("/entities/111", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"nzbn": "111",
			"entityName": "FOUND LIMITED",
			"entityStatusCode": "Registered",
			"roles": [{"roleType": "Director", "endDate": "", "rolePerson": {"firstName": "Jane", "lastName": "Smith"}}]
		}`)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"totalResults":1,"roles":[{"firstName":"Janet","lastName":"Smithson","roleType":"Director","associatedCompanyName":"COINCIDENTAL NAMESAKE LIMITED","associatedCompanyNzbn":333}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	nzClient := newNZBNTestClient(t, srv)

	entities, _, _ := gatherNZBNEntities(nzClient, []string{"Found Limited"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard))

	for _, e := range entities {
		if e.ID == "333" {
			t.Fatalf("a differently-named role search hit must not be fanned out as the same person: %+v", entities)
		}
	}
}

func TestGatherNZBNEntitiesNilClientReturnsNothing(t *testing.T) {
	entities, extra, notes := gatherNZBNEntities(nil, []string{"anything"}, 10, riskcache.New(), time.Hour, newProgressReporter(io.Discard))
	if entities != nil || extra != nil || notes != nil {
		t.Errorf("got (%v, %v, %v), want all nil when nzClient is nil", entities, extra, notes)
	}
}

func TestWithVerifiedAtStampsCopyWithoutMutatingOriginal(t *testing.T) {
	original := []risk.Entity{risk.NewEntity("ireland", "1", "Example Ltd", nil, nil)}
	cachedAt := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

	stamped := withVerifiedAt(original, cachedAt)

	if len(stamped) != 1 || stamped[0].VerifiedAt != cachedAt.Format(time.RFC3339) {
		t.Fatalf("stamped = %+v, want VerifiedAt = %s", stamped, cachedAt.Format(time.RFC3339))
	}
	// The original slice/struct riskcache.Cache.Get returned must be
	// left untouched -- it's the same underlying data every other
	// caller sharing that cache key (including concurrently, from a
	// different query term or source) sees.
	if original[0].VerifiedAt != "" {
		t.Errorf("original entity was mutated: VerifiedAt = %q, want empty", original[0].VerifiedAt)
	}
}

// TestGatherIrelandEntitiesPopulatesVerifiedAtOnCacheHit pre-seeds the
// cache directly (Set) rather than going through a live/httptest
// SearchByName call -- gatherIrelandEntities checks the cache before
// ever touching its client, so this exercises the real cache-hit code
// path with no network involved, offline-safe like every other test
// in this file.
func TestGatherIrelandEntitiesPopulatesVerifiedAtOnCacheHit(t *testing.T) {
	cache := &riskcache.Cache{Dir: t.TempDir()}
	cache.Set(riskcache.Key("ireland", "Example Ltd", 10), []risk.Entity{
		risk.NewEntity("ireland", "12345", "EXAMPLE LTD", nil, nil),
	})

	entities, _ := gatherIrelandEntities([]string{"Example Ltd"}, 10, cache, time.Hour, newProgressReporter(io.Discard))

	if len(entities) != 1 {
		t.Fatalf("got %d entities, want 1 from the cache hit: %+v", len(entities), entities)
	}
	if entities[0].VerifiedAt == "" {
		t.Error("expected VerifiedAt to be populated for an entity served from the cache")
	}
	if _, err := time.Parse(time.RFC3339, entities[0].VerifiedAt); err != nil {
		t.Errorf("VerifiedAt = %q is not valid RFC3339: %v", entities[0].VerifiedAt, err)
	}
}

// TestNewEntityLeavesVerifiedAtEmpty documents the complementary half
// of the cache-hit test above: every gatherer's non-cached/live-fetch
// path builds entities via risk.NewEntity (see e.g.
// gatherIrelandEntities, gatherACNCEntities), which never touches
// VerifiedAt at all -- so a live-fetched entity's freshness is only
// ever implied by the report's own GeneratedAt, never a stale-looking
// blank timestamp.
func TestNewEntityLeavesVerifiedAtEmpty(t *testing.T) {
	e := risk.NewEntity("ireland", "1", "Example Ltd", nil, nil)
	if e.VerifiedAt != "" {
		t.Errorf("VerifiedAt = %q, want empty for a freshly-constructed (non-cached) entity", e.VerifiedAt)
	}
}

func TestDormantReactivatedFiresOnTransition(t *testing.T) {
	// API order is newest-first; the function must sort before deciding.
	filings := []companieshouse.Filing{
		{Date: "2024-06-01", Category: "accounts", Description: "accounts-with-accounts-type-small"},
		{Date: "2022-06-01", Category: "accounts", Description: "accounts-with-accounts-type-dormant"},
		{Date: "2021-06-01", Category: "accounts", Description: "accounts-with-accounts-type-dormant"},
	}
	got := dormantReactivated(filings, "companieshouse: Alpha Ltd (1)")
	if len(got) != 1 || got[0].Code != "dormant_reactivated" {
		t.Fatalf("got %+v, want one dormant_reactivated", got)
	}
	if got[0].Date != "2024-06-01" {
		t.Errorf("Date = %q, want the reactivation date so it lands on the timeline", got[0].Date)
	}
}

func TestDormantReactivatedIgnoresAlwaysTrading(t *testing.T) {
	filings := []companieshouse.Filing{
		{Date: "2024-06-01", Category: "accounts", Description: "accounts-with-accounts-type-small"},
		{Date: "2023-06-01", Category: "accounts", Description: "accounts-with-accounts-type-small"},
	}
	if got := dormantReactivated(filings, "x"); len(got) != 0 {
		t.Errorf("got %+v, want nothing -- never dormant", got)
	}
}

// TestDormantReactivatedIgnoresStillDormant guards the direction: a
// company that went trading THEN dormant has not been reactivated.
func TestDormantReactivatedIgnoresStillDormant(t *testing.T) {
	filings := []companieshouse.Filing{
		{Date: "2024-06-01", Category: "accounts", Description: "accounts-with-accounts-type-dormant"},
		{Date: "2022-06-01", Category: "accounts", Description: "accounts-with-accounts-type-small"},
	}
	if got := dormantReactivated(filings, "x"); len(got) != 0 {
		t.Errorf("got %+v, want nothing -- it went dormant, it didn't reactivate", got)
	}
}

func TestDormantReactivatedIgnoresNonAccountsFilings(t *testing.T) {
	filings := []companieshouse.Filing{
		{Date: "2024-06-01", Category: "officers", Description: "appoint-person-director-company"},
		{Date: "2023-06-01", Category: "mortgage", Description: "mortgage-create-with-deed"},
	}
	if got := dormantReactivated(filings, "x"); len(got) != 0 {
		t.Errorf("got %+v, want nothing from non-accounts filings", got)
	}
}

func TestMassNomineeOfficerTiers(t *testing.T) {
	cases := []struct {
		name       string
		total      int
		wantFires  bool
		wantWeight int
		wantScale  string
	}{
		{"below signal threshold", 149, false, 0, ""},
		{"at signal threshold", 150, true, massNomineeWeight, "professional nominee scale"},
		{"mid professional range", 764, true, massNomineeWeight, "professional nominee scale"},
		{"just below industrial", 999, true, massNomineeWeight, "professional nominee scale"},
		{"at industrial threshold", 1000, true, industrialNomineeWeight, "industrial nominee scale"},
		{"real observed maximum", 12587, true, industrialNomineeWeight, "industrial nominee scale"},
	}
	for _, c := range cases {
		got := officerAppointmentIndicators("Test Officer", nil, c.total, "companieshouse: X (1)")
		var found *risk.Indicator
		for i := range got {
			if got[i].Code == "mass_nominee_officer" {
				found = &got[i]
			}
		}
		if !c.wantFires {
			if found != nil {
				t.Errorf("%s (%d): fired when it should not: %+v", c.name, c.total, found)
			}
			continue
		}
		if found == nil {
			t.Errorf("%s (%d): expected mass_nominee_officer, got none", c.name, c.total)
			continue
		}
		if found.Weight != c.wantWeight {
			t.Errorf("%s (%d): Weight = %d, want %d", c.name, c.total, found.Weight, c.wantWeight)
		}
		if !strings.Contains(found.Evidence, c.wantScale) {
			t.Errorf("%s (%d): Evidence = %q, want it to name %q", c.name, c.total, found.Evidence, c.wantScale)
		}
		// The count itself is what a reader should judge on, so it must
		// always be stated regardless of tier.
		if !strings.Contains(found.Evidence, fmt.Sprintf("%d appointments", c.total)) {
			t.Errorf("%s (%d): Evidence = %q, want the raw count stated", c.name, c.total, found.Evidence)
		}
	}
}

// TestNomineeTierWeightsStayBelowAdjudicatedBand guards the deliberate
// ceiling: running a nominee service at any scale is lawful, and this
// is a structural inference, not a regulator's finding.
func TestNomineeTierWeightsStayBelowAdjudicatedBand(t *testing.T) {
	if industrialNomineeWeight >= confirmedFactWeight {
		t.Errorf("industrialNomineeWeight = %d reaches the adjudicated-fact band (%d); scale is not adjudication",
			industrialNomineeWeight, confirmedFactWeight)
	}
	if massNomineeWeight >= industrialNomineeWeight {
		t.Errorf("tiers are not ordered: %d >= %d", massNomineeWeight, industrialNomineeWeight)
	}
}

func TestMailDropTiers(t *testing.T) {
	cases := []struct {
		count      int
		wantFires  bool
		wantWeight int
		wantScale  string
	}{
		{19999, false, 0, ""},
		{20000, true, mailDropWeight, "large shared address"},
		{99999, true, mailDropWeight, "large shared address"},
		{100000, true, industrialMailDropWeight, "industrial mail-drop scale"},
		{192358, true, industrialMailDropWeight, "industrial mail-drop scale"},
	}
	for _, c := range cases {
		got := mailDropIndicator(c.count, "EC1A 1AA", "companieshouse: X (1)")
		if !c.wantFires {
			if len(got) != 0 {
				t.Errorf("count %d: fired when it should not: %+v", c.count, got)
			}
			continue
		}
		if len(got) != 1 {
			t.Errorf("count %d: got %d indicators, want 1", c.count, len(got))
			continue
		}
		if got[0].Weight != c.wantWeight {
			t.Errorf("count %d: Weight = %d, want %d", c.count, got[0].Weight, c.wantWeight)
		}
		if !strings.Contains(got[0].Evidence, c.wantScale) {
			t.Errorf("count %d: Evidence = %q, want it to name %q", c.count, got[0].Evidence, c.wantScale)
		}
		// The raw count is what a reader judges on and must always show.
		if !strings.Contains(got[0].Evidence, fmt.Sprintf("%d companies", c.count)) {
			t.Errorf("count %d: Evidence = %q, want the raw count stated", c.count, got[0].Evidence)
		}
	}
}

// TestMailDropTierWeightsStayBelowAdjudicatedBand mirrors the nominee
// guard: running a registered-office service is lawful at any scale.
func TestMailDropTierWeightsStayBelowAdjudicatedBand(t *testing.T) {
	if industrialMailDropWeight >= confirmedFactWeight {
		t.Errorf("industrialMailDropWeight = %d reaches the adjudicated-fact band (%d)",
			industrialMailDropWeight, confirmedFactWeight)
	}
	if mailDropWeight >= industrialMailDropWeight {
		t.Errorf("tiers are not ordered: %d >= %d", mailDropWeight, industrialMailDropWeight)
	}
}

// TestFannedOutEntityCarriesTenure guards the fix that makes --as-of
// meaningful on a real scan. Fan-out entities -- companies discovered
// through an officer's appointment history -- are the bulk of a large
// scan, and they were being built with no dates at all. A real
// reconstruction reported 396 of 411 person-links as undated because of
// it, which triggered the weak-evidence warning: the feature worked but
// had almost nothing to work with.
//
// The appointment's own dates ARE the tenure of the link between that
// officer and that company, and they are already in hand, so carrying
// them costs nothing.
func TestFannedOutEntityCarriesTenure(t *testing.T) {
	appt := companieshouse.Appointment{
		CompanyNumber: "01234567",
		CompanyName:   "FANNED OUT LTD",
		AppointedOn:   "2014-03-01",
		ResignedOn:    "2018-09-30",
	}
	e := fannedOutEntity(appt, "Jane Doe")

	if len(e.PersonDetails) != 1 {
		t.Fatalf("PersonDetails = %+v, want one entry carrying the appointment tenure", e.PersonDetails)
	}
	p := e.PersonDetails[0]
	if p.AppointedOn != "2014-03-01" || p.ResignedOn != "2018-09-30" {
		t.Errorf("tenure = %s..%s, want 2014-03-01..2018-09-30", p.AppointedOn, p.ResignedOn)
	}
	if !p.HasTenure() {
		t.Error("HasTenure() = false -- this entity would count as undatable in an as-of reconstruction")
	}
	// The present-day People list is unchanged in shape.
	if len(e.People) != 1 || e.People[0] != "Jane Doe" {
		t.Errorf("People = %v, want the officer name", e.People)
	}

	// And it must actually reconstruct: present in 2016, absent in 2020.
	in, _ := risk.AsOf([]risk.Entity{e}, time.Date(2016, 6, 1, 0, 0, 0, 0, time.UTC))
	if len(in) != 1 || len(in[0].People) != 1 {
		t.Errorf("2016 reconstruction = %+v, want the officer present", in)
	}
	out, cov := risk.AsOf([]risk.Entity{e}, time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC))
	if len(out) == 1 && len(out[0].People) != 0 {
		t.Errorf("2020 reconstruction = %+v, want the officer gone (resigned 2018)", out[0].People)
	}
	if cov.PeopleUndatable != 0 {
		t.Errorf("PeopleUndatable = %d, want 0 -- the whole point is that this link is datable", cov.PeopleUndatable)
	}
}
