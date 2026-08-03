package usaspending

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient()
	c.MinInterval = 0
	c.RetryBaseDelay = 0
	c.BaseURL = srv.URL
	return c
}

// This fixture is modeled directly on the real, live-verified
// USAspending spending_by_award response for a recipient_search_text
// query ("LOCKHEED MARTIN"), trimmed to the fields this package
// actually parses.
const realSpendingByAwardResponse = `{
  "results": [
    {
      "internal_id": 295476065,
      "Recipient Name": "LOCKHEED MARTIN CORP",
      "Award Amount": 48063737196.35,
      "Awarding Agency": "Department of Energy",
      "Award Type": null,
      "Start Date": "1993-10-15",
      "End Date": "2017-04-30",
      "Description": null,
      "generated_internal_id": "CONT_AWD_DEAC0494AL85000_8900_-NONE-_-NONE-"
    }
  ]
}`

// requestTypeCodes decodes a spending_by_award request body and
// returns its filters.award_type_codes, so a test mock can respond
// differently per award-type group the way the real API's own
// one-group-per-request rule requires (see federalAwardTypeGroups).
func requestTypeCodes(t *testing.T, r *http.Request) []string {
	t.Helper()
	var payload map[string]any
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	filters := payload["filters"].(map[string]any)
	raw := filters["award_type_codes"].([]any)
	codes := make([]string, len(raw))
	for i, c := range raw {
		codes[i] = c.(string)
	}
	return codes
}

func TestSearchAwardsParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/spending_by_award/", func(w http.ResponseWriter, r *http.Request) {
		codes := requestTypeCodes(t, r)
		if codes[0] == "A" { // the contracts group
			fmt.Fprint(w, realSpendingByAwardResponse)
			return
		}
		fmt.Fprint(w, `{"results":[]}`) // the grants group: no matches
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	awards, err := c.SearchAwards("Lockheed Martin", 5)
	if err != nil {
		t.Fatalf("SearchAwards: %v", err)
	}
	if len(awards) != 1 {
		t.Fatalf("got %d awards, want 1", len(awards))
	}
	a := awards[0]
	if a.RecipientName != "LOCKHEED MARTIN CORP" {
		t.Errorf("RecipientName = %q", a.RecipientName)
	}
	if a.Amount != 48063737196.35 {
		t.Errorf("Amount = %v", a.Amount)
	}
	if a.AwardingAgency != "Department of Energy" {
		t.Errorf("AwardingAgency = %q", a.AwardingAgency)
	}
	if a.GeneratedID != "CONT_AWD_DEAC0494AL85000_8900_-NONE-_-NONE-" {
		t.Errorf("GeneratedID = %q", a.GeneratedID)
	}
}

// TestSearchAwardsIssuesOneRequestPerAwardTypeGroup guards the fix for
// a real, live-confirmed bug: USAspending rejects a single request
// whose award_type_codes mix more than one of its own award-type
// groups ("'award_type_codes' must only contain types from one
// group"). SearchAwards must issue one request per group instead.
func TestSearchAwardsIssuesOneRequestPerAwardTypeGroup(t *testing.T) {
	var seenGroups [][]string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/spending_by_award/", func(w http.ResponseWriter, r *http.Request) {
		seenGroups = append(seenGroups, requestTypeCodes(t, r))
		fmt.Fprint(w, `{"results":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchAwards("Example", 5); err != nil {
		t.Fatalf("SearchAwards: %v", err)
	}
	if len(seenGroups) != 2 {
		t.Fatalf("got %d requests, want 2 (one per award-type group)", len(seenGroups))
	}
	contracts := []string{"A", "B", "C", "D"}
	grants := []string{"02", "03", "04", "05"}
	for _, codes := range seenGroups {
		if !reflect.DeepEqual(codes, contracts) && !reflect.DeepEqual(codes, grants) {
			t.Errorf("request codes = %v, want exactly the contracts group or exactly the grants group, never mixed", codes)
		}
	}
}

func TestSearchAwardsReturnsEmptyOnNoMatches(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/spending_by_award/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	awards, err := c.SearchAwards("Nonexistent Org XYZ", 5)
	if err != nil {
		t.Fatalf("SearchAwards: %v", err)
	}
	if len(awards) != 0 {
		t.Errorf("got %d awards, want 0", len(awards))
	}
}

func TestSearchAwardsReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/spending_by_award/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchAwards("Example", 5); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/search/spending_by_award/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		codes := requestTypeCodes(t, r)
		if codes[0] == "A" { // the contracts group
			fmt.Fprint(w, realSpendingByAwardResponse)
			return
		}
		fmt.Fprint(w, `{"results":[]}`) // the grants group: no matches
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	awards, err := c.SearchAwards("Lockheed Martin", 5)
	if err != nil {
		t.Fatalf("SearchAwards: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 4 {
		t.Errorf("made %d attempts, want 4 (two 429s then a success, for the first of the two award-type-group requests; the second group's request succeeds immediately)", attempts)
	}
	if len(awards) != 1 {
		t.Errorf("got %d awards, want 1", len(awards))
	}
}
