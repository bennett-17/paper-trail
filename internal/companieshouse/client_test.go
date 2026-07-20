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
