package nonprofit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
	c := NewClient()
	c.MinInterval = 0
	c.SearchURL = srv.URL + "/search.json"
	c.OrganizationURL = srv.URL + "/organizations/%s.json"
	return c
}

func TestSearchOrganizationsParsesResults(t *testing.T) {
	var gotQuery, gotPage string
	mux := http.NewServeMux()
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotPage = r.URL.Query().Get("page")
		fmt.Fprint(w, mustReadFixture(t, "nonprofit_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchOrganizations("Church of Scientology", 1)
	if err != nil {
		t.Fatalf("SearchOrganizations: %v", err)
	}
	if gotQuery != "Church of Scientology" {
		t.Errorf("q param = %q, want %q", gotQuery, "Church of Scientology")
	}
	if gotPage != "" {
		t.Errorf(`page=1 should omit the "page" param entirely, got %q`, gotPage)
	}
	if result.TotalResults != 108 {
		t.Errorf("TotalResults = %d, want 108", result.TotalResults)
	}
	if result.NumPages != 5 {
		t.Errorf("NumPages = %d, want 5", result.NumPages)
	}
	if len(result.Organizations) != 2 {
		t.Fatalf("got %d organizations, want 2", len(result.Organizations))
	}

	first := result.Organizations[0]
	if first.EIN != "43-2050079" {
		t.Errorf("first.EIN = %q, want 43-2050079", first.EIN)
	}
	if first.City != "Greenwich" || first.State != "NY" {
		t.Errorf("first location = %s/%s, want Greenwich/NY", first.City, first.State)
	}
}

func TestSearchOrganizationsSecondPage(t *testing.T) {
	var gotPage string
	mux := http.NewServeMux()
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		gotPage = r.URL.Query().Get("page")
		fmt.Fprint(w, mustReadFixture(t, "nonprofit_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchOrganizations("Church of Scientology", 3); err != nil {
		t.Fatalf("SearchOrganizations: %v", err)
	}
	if gotPage != "3" {
		t.Errorf("page param = %q, want 3", gotPage)
	}
}

// TestSearchOrganizationsToleratesZeroResult404 guards against a real
// bug found live: ProPublica's search endpoint returns HTTP 404 (not
// 200), with a normal JSON body reporting zero results, for a query
// that matches nothing. A naive "any non-2xx is an error" check
// misreports that as a request failure instead of an empty result.
func TestSearchOrganizationsToleratesZeroResult404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"total_results":0,"organizations":[],"num_pages":0,"cur_page":0,"search_query":"no such organization"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchOrganizations("no such organization", 1)
	if err != nil {
		t.Fatalf("SearchOrganizations: %v, want nil error for a zero-result 404", err)
	}
	if result.TotalResults != 0 {
		t.Errorf("TotalResults = %d, want 0", result.TotalResults)
	}
	if len(result.Organizations) != 0 {
		t.Errorf("got %d organizations, want 0", len(result.Organizations))
	}
	if result.Page != 1 {
		t.Errorf("Page = %d, want 1 (should floor cur_page=0 to 1)", result.Page)
	}
}

func TestGetOrganizationParsesFilings(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/530196605.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "nonprofit_organization_red_cross.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	profile, err := c.GetOrganization("53-0196605")
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if profile.Organization.EIN != "53-0196605" {
		t.Errorf("EIN = %q, want 53-0196605", profile.Organization.EIN)
	}
	if profile.Organization.Name != "American National Red Cross" {
		t.Errorf("Name = %q", profile.Organization.Name)
	}
	if len(profile.Filings) != 2 {
		t.Fatalf("got %d filings, want 2 (1 with data + 1 without)", len(profile.Filings))
	}

	withData := profile.Filings[0]
	if !withData.HasFinancials {
		t.Errorf("filings_with_data entry should have HasFinancials=true")
	}
	if withData.FormType != "990" {
		t.Errorf("withData.FormType = %q, want 990", withData.FormType)
	}
	if withData.TotalRevenue == nil || *withData.TotalRevenue != 3217077611 {
		t.Errorf("withData.TotalRevenue = %v, want 3217077611", withData.TotalRevenue)
	}

	withoutData := profile.Filings[1]
	if withoutData.HasFinancials {
		t.Errorf("filings_without_data entry should have HasFinancials=false")
	}
	if withoutData.TotalRevenue != nil {
		t.Errorf("withoutData.TotalRevenue = %v, want nil", withoutData.TotalRevenue)
	}
	if withoutData.TaxYear != 2010 {
		t.Errorf("withoutData.TaxYear = %d, want 2010", withoutData.TaxYear)
	}
}

func TestGetOrganizationAcceptsEINWithOrWithoutHyphen(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/530196605.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "nonprofit_organization_red_cross.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.GetOrganization("530196605"); err != nil {
		t.Errorf("GetOrganization without hyphen: %v", err)
	}
	if _, err := c.GetOrganization("53-0196605"); err != nil {
		t.Errorf("GetOrganization with hyphen: %v", err)
	}
}

func TestFormatEINPadsLeadingZeros(t *testing.T) {
	cases := map[int64]string{
		530196605: "53-0196605", // ordinary case
		10000001:  "01-0000001", // single-digit prefix must still get 2 digits
		1:         "00-0000001", // both halves need padding
	}
	for ein, want := range cases {
		if got := formatEIN(ein); got != want {
			t.Errorf("formatEIN(%d) = %q, want %q", ein, got, want)
		}
	}
}
