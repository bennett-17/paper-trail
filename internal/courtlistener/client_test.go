package courtlistener

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient()
	c.MinInterval = 0
	c.RetryBaseDelay = time.Millisecond
	c.BaseURL = srv.URL + "/"
	c.SiteURL = "https://www.courtlistener.com"
	return c
}

// This fixture is modeled on a real, live-verified CourtListener v4
// search response for a party_name query, trimmed to the fields this
// package actually parses.
const realShapeResponse = `{
  "count": 39413,
  "document_count": 1161663,
  "next": "https://www.courtlistener.com/api/rest/v4/search/?cursor=abc",
  "previous": null,
  "results": [
    {
      "caseName": "Armony v. Wells Fargo Bank, N.A.",
      "court": "District Court, C.D. California",
      "court_citation_string": "C.D. Cal.",
      "dateFiled": "2026-07-31",
      "dateTerminated": null,
      "docketNumber": "2:26-cv-08504",
      "docket_id": 73707637,
      "docket_absolute_url": "/docket/73707637/armony-v-wells-fargo-bank-na/",
      "cause": "",
      "party": ["Armony", "Wells Fargo Bank, N.A."]
    },
    {
      "caseName": "Second Case",
      "court": "District Court, W.D. Louisiana",
      "court_citation_string": "W.D. La.",
      "dateFiled": "2020-02-28",
      "dateTerminated": "2020-02-28",
      "docketNumber": "5:20-mc-00013",
      "docket_id": 16908781,
      "docket_absolute_url": "/docket/16908781/wells-fargo/",
      "cause": "28:0754 Receiver of Property in Different Districts",
      "party": ["Wells Fargo National Association", "Capital One Trust 2016-USB 11"]
    }
  ]
}`

func TestSearchPartiesParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("party_name"); got != "Wells Fargo" {
			t.Errorf("party_name param = %q, want Wells Fargo", got)
		}
		if got := r.URL.Query().Get("type"); got != "r" {
			t.Errorf("type param = %q, want r", got)
		}
		fmt.Fprint(w, realShapeResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.SearchParties("Wells Fargo", 10)
	if err != nil {
		t.Fatalf("SearchParties: %v", err)
	}
	if result.Total != 39413 {
		t.Errorf("Total = %d, want 39413", result.Total)
	}
	if len(result.Dockets) != 2 {
		t.Fatalf("got %d dockets, want 2", len(result.Dockets))
	}
	d := result.Dockets[0]
	if d.CaseName != "Armony v. Wells Fargo Bank, N.A." {
		t.Errorf("CaseName = %q", d.CaseName)
	}
	if d.DocketURL != "https://www.courtlistener.com/docket/73707637/armony-v-wells-fargo-bank-na/" {
		t.Errorf("DocketURL = %q", d.DocketURL)
	}
	if len(d.Parties) != 2 || d.Parties[1] != "Wells Fargo Bank, N.A." {
		t.Errorf("Parties = %v", d.Parties)
	}
	if d.DateTerminated != "" {
		t.Errorf("DateTerminated = %q, want empty for an open case", d.DateTerminated)
	}
}

func TestSearchPartiesRespectsLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, realShapeResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.SearchParties("Wells Fargo", 1)
	if err != nil {
		t.Fatalf("SearchParties: %v", err)
	}
	if len(result.Dockets) != 1 {
		t.Fatalf("got %d dockets, want 1 (limit)", len(result.Dockets))
	}
	if result.Total != 39413 {
		t.Errorf("Total should still reflect the full match count (%d), got %d", 39413, result.Total)
	}
}

func TestSearchPartiesRetriesOn429(t *testing.T) {
	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SearchParties("Nobody", 10)
	if err != nil {
		t.Fatalf("SearchParties: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures + 1 success)", attempts)
	}
}

func TestSearchPartiesNoMatchReturnsEmptyNotError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.SearchParties("Nobody At All Zzz", 10)
	if err != nil {
		t.Fatalf("SearchParties: %v", err)
	}
	if len(result.Dockets) != 0 {
		t.Errorf("got %+v, want empty", result.Dockets)
	}
}
