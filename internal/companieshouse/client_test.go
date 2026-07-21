package companieshouse

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func mustReadFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(data)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient("test-api-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.RetryBaseDelay = time.Millisecond
	c.BaseURL = srv.URL
	return c
}

func TestNewClientRequiresAKey(t *testing.T) {
	os.Unsetenv("COMPANIES_HOUSE_API_KEY")
	if _, err := NewClient(""); err == nil {
		t.Fatal("expected error when no key is configured")
	}
}

func TestNewClientFallsBackToEnvVar(t *testing.T) {
	os.Setenv("COMPANIES_HOUSE_API_KEY", "env-key")
	t.Cleanup(func() { os.Unsetenv("COMPANIES_HOUSE_API_KEY") })

	c, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want env-key", c.APIKey)
	}
}

func TestSearchCompaniesUsesBasicAuth(t *testing.T) {
	var gotAuth, gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/companies", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchCompanies("Example", 0)
	if err != nil {
		t.Fatalf("SearchCompanies: %v", err)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-api-key:"))
	if gotAuth != wantAuth {
		t.Errorf("Authorization header = %q, want %q (key as Basic Auth username, blank password)", gotAuth, wantAuth)
	}
	if gotQuery != "Example" {
		t.Errorf("q param = %q, want Example", gotQuery)
	}
	if result.Total != 1 || len(result.Companies) != 1 {
		t.Fatalf("result = %+v, want 1 company", result)
	}
	if result.Companies[0].CompanyNumber != "04325234" {
		t.Errorf("CompanyNumber = %q, want 04325234", result.Companies[0].CompanyNumber)
	}
	if result.Companies[0].Address.Locality != "London" {
		t.Errorf("Address.Locality = %q, want London", result.Companies[0].Address.Locality)
	}
}

func TestGetCompanyParsesProfile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_company.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	company, err := c.GetCompany("04325234")
	if err != nil {
		t.Fatalf("GetCompany: %v", err)
	}
	if company.Name != "EXAMPLE CHARITABLE COMPANY" {
		t.Errorf("Name = %q", company.Name)
	}
	if company.RegisteredOffice.PostalCode != "E20 1JQ" {
		t.Errorf("RegisteredOffice.PostalCode = %q", company.RegisteredOffice.PostalCode)
	}
	if len(company.SICCodes) != 1 || company.SICCodes[0] != "86900" {
		t.Errorf("SICCodes = %v", company.SICCodes)
	}
}

// TestGetCompanyZeroPadsNumber guards against a real bug found live:
// the UK Charity Commission's CompaniesHouseNumber field returns
// company numbers without leading zeros (e.g. "4325234"), but this API
// 404s on anything shorter than the full 8-character form.
func TestGetCompanyZeroPadsNumber(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_company.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.GetCompany("4325234"); err != nil {
		t.Fatalf("GetCompany: %v", err)
	}
	if gotPath != "/company/04325234" {
		t.Errorf("request path = %q, want zero-padded /company/04325234", gotPath)
	}
}

func TestGetOfficersZeroPadsNumber(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234/officers", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_officers.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.GetOfficers("4325234", 0); err != nil {
		t.Fatalf("GetOfficers: %v", err)
	}
	if gotPath != "/company/04325234/officers" {
		t.Errorf("request path = %q, want zero-padded /company/04325234/officers", gotPath)
	}
}

func TestGetCompanyNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/company/99999999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 NOT_FOUND"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.GetCompany("99999999"); err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}

func TestGetOfficersParsesCurrentAndFormer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234/officers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_officers.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	officers, err := c.GetOfficers("04325234", 0)
	if err != nil {
		t.Fatalf("GetOfficers: %v", err)
	}
	if len(officers) != 2 {
		t.Fatalf("got %d officers, want 2", len(officers))
	}
	if officers[0].Name != "EXAMPLE, Jane" || officers[0].Role != "director" || officers[0].ResignedOn != "" {
		t.Errorf("officers[0] = %+v", officers[0])
	}
	if officers[1].Name != "SAMPLE, John" || officers[1].ResignedOn != "2023-06-15" {
		t.Errorf("officers[1] = %+v, want a resigned_on set (a former officer)", officers[1])
	}
	if officers[0].OfficerID != "exampleJaneOfficerId" {
		t.Errorf("officers[0].OfficerID = %q, want it parsed out of links.officer.appointments", officers[0].OfficerID)
	}
	if officers[1].OfficerID != "exampleJohnOfficerId" {
		t.Errorf("officers[1].OfficerID = %q, want it parsed out of links.officer.appointments", officers[1].OfficerID)
	}
}

func TestOfficerIDFromAppointmentsLink(t *testing.T) {
	cases := map[string]string{
		"/officers/z_rLf8JlTMd8wKrovebh6e8B17c/appointments": "z_rLf8JlTMd8wKrovebh6e8B17c",
		"":                        "",
		"/something/else":         "",
		"/officers//appointments": "",
	}
	for in, want := range cases {
		if got := officerIDFromAppointmentsLink(in); got != want {
			t.Errorf("officerIDFromAppointmentsLink(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGetOfficerAppointmentsFansOutAcrossCompanies guards the actual
// point of this endpoint: given one officer ID, it returns every
// company appointment for that person register-wide, not just the
// company used to discover them -- confirmed live against a real
// director on the Companies House register with appointments at
// multiple companies, including a dissolved one.
func TestGetOfficerAppointmentsFansOutAcrossCompanies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/officers/exampleOfficerId/appointments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_appointments.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	appointments, err := c.GetOfficerAppointments("exampleOfficerId", 0)
	if err != nil {
		t.Fatalf("GetOfficerAppointments: %v", err)
	}
	if len(appointments) != 2 {
		t.Fatalf("got %d appointments, want 2", len(appointments))
	}
	if appointments[0].CompanyNumber != "05833630" || appointments[0].CompanyStatus != "active" || appointments[0].ResignedOn != "" {
		t.Errorf("appointments[0] = %+v", appointments[0])
	}
	if appointments[1].CompanyNumber != "05397121" || appointments[1].CompanyStatus != "dissolved" || appointments[1].ResignedOn != "2019-03-31" {
		t.Errorf("appointments[1] = %+v, want a resigned_on set", appointments[1])
	}
}

func TestGetPersonsWithSignificantControlParsesCurrentAndFormer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234/persons-with-significant-control", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_psc.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	pscs, err := c.GetPersonsWithSignificantControl("04325234", 0)
	if err != nil {
		t.Fatalf("GetPersonsWithSignificantControl: %v", err)
	}
	// The fixture has 3 items; the third is a "statement" entry with no
	// name and must be dropped, not returned as a blank-named PSC.
	if len(pscs) != 2 {
		t.Fatalf("got %d PSCs, want 2 (the nameless statement entry should be dropped): %+v", len(pscs), pscs)
	}
	if pscs[0].Name != "Mrs Jane Example" || pscs[0].CeasedOn != "" {
		t.Errorf("pscs[0] = %+v", pscs[0])
	}
	if pscs[1].Name != "Mr John Sample" || pscs[1].CeasedOn != "2018-07-17" {
		t.Errorf("pscs[1] = %+v, want a ceased_on set (a former PSC)", pscs[1])
	}
}

func TestSearchDisqualifiedOfficersParsesNaturalAndCorporate(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/disqualified-officers", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_disqualified_officers.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	hits, err := c.SearchDisqualifiedOfficers("Example Sample", 5)
	if err != nil {
		t.Fatalf("SearchDisqualifiedOfficers: %v", err)
	}
	if gotQuery != "Example Sample" {
		t.Errorf("q param = %q, want Example Sample", gotQuery)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Name != "Example Jane SAMPLE" || hits[0].Description != "Born on 18 October 1987 - Disqualified" {
		t.Errorf("hits[0] = %+v", hits[0])
	}
	if hits[1].Name != "EXAMPLE CORPORATE OFFICER LTD" {
		t.Errorf("hits[1] = %+v, want the corporate-officer hit too (same search covers both)", hits[1])
	}
}

func TestSearchDisqualifiedOfficersZeroResultsIsClean(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/disqualified-officers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"total_results":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	hits, err := c.SearchDisqualifiedOfficers("no such person anywhere", 0)
	if err != nil {
		t.Fatalf("SearchDisqualifiedOfficers: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %+v, want a clean empty result", hits)
	}
}

func TestGetChargesParsesOutstandingAndSatisfied(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234/charges", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_charges.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	charges, err := c.GetCharges("4325234", 0)
	if err != nil {
		t.Fatalf("GetCharges: %v", err)
	}
	if len(charges) != 2 {
		t.Fatalf("got %d charges, want 2", len(charges))
	}
	if charges[0].Status != "outstanding" || charges[0].SatisfiedOn != "" {
		t.Errorf("charges[0] = %+v", charges[0])
	}
	if len(charges[0].PersonsEntitled) != 1 || charges[0].PersonsEntitled[0] != "Example Private Lender Ltd" {
		t.Errorf("charges[0].PersonsEntitled = %v", charges[0].PersonsEntitled)
	}
	if charges[1].Status != "fully-satisfied" || charges[1].SatisfiedOn != "2012-09-12" {
		t.Errorf("charges[1] = %+v, want a satisfied_on set", charges[1])
	}
}

func TestCountCompaniesAtLocationParsesHits(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/advanced-search/companies", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("location")
		fmt.Fprint(w, `{"hits":190797,"items":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	count, err := c.CountCompaniesAtLocation("WC2H 9JQ")
	if err != nil {
		t.Fatalf("CountCompaniesAtLocation: %v", err)
	}
	if gotQuery != "WC2H 9JQ" {
		t.Errorf("location param = %q, want WC2H 9JQ", gotQuery)
	}
	if count != 190797 {
		t.Errorf("count = %d, want 190797", count)
	}
}

// TestCountCompaniesAtLocationTreats404AsZero guards a real quirk
// found live: unlike /search/companies (which returns a clean
// zero-result 200), this endpoint 404s for a location with no
// matches -- that's a legitimate "no companies here", not an error.
func TestCountCompaniesAtLocationTreats404AsZero(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/advanced-search/companies", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	count, err := c.CountCompaniesAtLocation("ZZ99 9ZZ")
	if err != nil {
		t.Fatalf("CountCompaniesAtLocation: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestGet401ReturnsActionableError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Invalid Authorization"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.GetCompany("04325234")
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	if !strings.Contains(err.Error(), "COMPANIES_HOUSE_API_KEY") {
		t.Errorf("error %q should mention COMPANIES_HOUSE_API_KEY so the user knows how to fix it", err.Error())
	}
}

// TestRetriesOn429ThenSucceeds mirrors internal/sanctions's retry
// behavior -- Companies House's documented limit (600 requests per
// 5-minute window) is tight enough that a busy risk scan hitting many
// companies' officers could plausibly trip it.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/company/04325234", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"Rate limit exceeded"}`)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "companieshouse_company.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	company, err := c.GetCompany("04325234")
	if err != nil {
		t.Fatalf("GetCompany: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if company.Name != "EXAMPLE CHARITABLE COMPANY" {
		t.Errorf("Name = %q", company.Name)
	}
}

func TestZeroResultSearchReturnsCleanEmptyResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/companies", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"total_results":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchCompanies("no such organization anywhere", 0)
	if err != nil {
		t.Fatalf("SearchCompanies: %v", err)
	}
	if result.Total != 0 || len(result.Companies) != 0 {
		t.Errorf("result = %+v, want a clean empty result", result)
	}
}
