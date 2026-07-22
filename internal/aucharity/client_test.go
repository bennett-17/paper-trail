package aucharity

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
	c.RetryBaseDelay = 0
	c.SearchURL = srv.URL + "/datastore_search"
	c.ResourceID = "test-resource-id"
	return c
}

// TestRetriesOn429ThenSucceeds mirrors internal/companieshouse,
// internal/sanctions, internal/edgar, and internal/nonprofit's retry
// behavior.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "aucharity_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchCharities("Example", 0, 0)
	if err != nil {
		t.Fatalf("SearchCharities: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if result.Total != 9 {
		t.Errorf("Total = %d, want 9", result.Total)
	}
}

func TestSearchCharitiesParsesResults(t *testing.T) {
	var gotQuery, gotResourceID string
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotResourceID = r.URL.Query().Get("resource_id")
		fmt.Fprint(w, mustReadFixture(t, "aucharity_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchCharities("Example", 0, 0)
	if err != nil {
		t.Fatalf("SearchCharities: %v", err)
	}
	if gotQuery != "Example" {
		t.Errorf("q param = %q, want Example", gotQuery)
	}
	if gotResourceID != "test-resource-id" {
		t.Errorf("resource_id param = %q, want test-resource-id", gotResourceID)
	}
	if result.Total != 9 {
		t.Errorf("Total = %d, want 9", result.Total)
	}
	if len(result.Charities) != 2 {
		t.Fatalf("got %d charities, want 2", len(result.Charities))
	}

	first := result.Charities[0]
	if first.ABN != "13172090453" {
		t.Errorf("first.ABN = %q, want 13172090453", first.ABN)
	}
	if first.City != "Sampleville" || first.State != "NSW" {
		t.Errorf("first location = %s/%s, want Sampleville/NSW", first.City, first.State)
	}
	if first.Size != "Medium" {
		t.Errorf("first.Size = %q, want Medium", first.Size)
	}
}

func TestSearchCharitiesPaginationParams(t *testing.T) {
	var gotOffset, gotLimit string
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		gotOffset = r.URL.Query().Get("offset")
		gotLimit = r.URL.Query().Get("limit")
		fmt.Fprint(w, mustReadFixture(t, "aucharity_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchCharities("Example", 25, 10); err != nil {
		t.Fatalf("SearchCharities: %v", err)
	}
	if gotOffset != "25" {
		t.Errorf("offset param = %q, want 25", gotOffset)
	}
	if gotLimit != "10" {
		t.Errorf("limit param = %q, want 10", gotLimit)
	}
}

func TestGetCharityByABNUsesExactFilter(t *testing.T) {
	var gotFilters string
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		gotFilters = r.URL.Query().Get("filters")
		fmt.Fprint(w, mustReadFixture(t, "aucharity_get_by_abn.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	charity, err := c.GetCharityByABN("13 172 090 453")
	if err != nil {
		t.Fatalf("GetCharityByABN: %v", err)
	}
	if gotFilters != `{"ABN":"13172090453"}` {
		t.Errorf("filters param = %q, want exact-match ABN filter with spaces stripped", gotFilters)
	}
	if charity.LegalName != "The Trustee For Example Foundation Building Fund" {
		t.Errorf("LegalName = %q", charity.LegalName)
	}
}

func TestGetCharityByABNNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"result":{"total":0,"records":[]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.GetCharityByABN("00000000000"); err == nil {
		t.Error("GetCharityByABN with no matching records: got nil error, want an error")
	}
}
