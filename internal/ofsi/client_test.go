package ofsi

import (
	"encoding/json"
	"fmt"
	"io"
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
	c.SearchURL = srv.URL + "/search"
	return c
}

// TestRetriesOn429ThenSucceeds mirrors internal/companieshouse,
// internal/sanctions, internal/edgar, internal/nonprofit,
// internal/aucharity, and internal/ukcharity's retry behavior.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "ofsi_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchDesignations("Example", 25)
	if err != nil {
		t.Fatalf("SearchDesignations: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
}

func TestSearchDesignationsSendsExpectedRequestBody(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody searchRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("unmarshaling request body: %v", err)
		}
		fmt.Fprint(w, mustReadFixture(t, "ofsi_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchDesignations("Example", 25)
	if err != nil {
		t.Fatalf("SearchDesignations: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST (confirmed live, this API takes a POST with a JSON body, not a GET with query params)", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody.SearchValue != "Example" {
		t.Errorf("searchValue = %q, want Example", gotBody.SearchValue)
	}
	if gotBody.PageSize != 25 {
		t.Errorf("pageSize = %d, want 25", gotBody.PageSize)
	}

	if result.Total != 2 || len(result.Hits) != 2 {
		t.Fatalf("result = %+v, want 2 hits", result)
	}
	if result.Hits[0].Name != "EXAMPLE Sergei Sergeevich" || result.Hits[0].Regime != "Russia" {
		t.Errorf("Hits[0] = %+v", result.Hits[0])
	}
	if result.Hits[1].Name != "EXAMPLE ENTITY LTD" || result.Hits[1].EntityType != "Entity" {
		t.Errorf("Hits[1] = %+v", result.Hits[1])
	}
}

func TestSearchDesignationsDefaultsPageSizeTo50(t *testing.T) {
	var gotBody searchRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		fmt.Fprint(w, `{"TotalCount":0,"Hits":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchDesignations("Example", 0); err != nil {
		t.Fatalf("SearchDesignations: %v", err)
	}
	if gotBody.PageSize != 50 {
		t.Errorf("pageSize = %d, want 50 (the live tool's own default when limit <= 0)", gotBody.PageSize)
	}
}

func TestZeroResultSearchReturnsCleanEmptyResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"TotalCount":0,"Hits":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchDesignations("no such person anywhere", 0)
	if err != nil {
		t.Fatalf("SearchDesignations: %v", err)
	}
	if result.Total != 0 || len(result.Hits) != 0 {
		t.Errorf("result = %+v, want a clean empty result", result)
	}
}

func TestSearchDesignationsNonOKStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"internal error"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchDesignations("Example", 0); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
