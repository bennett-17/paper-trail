package wikidata

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
	c.APIURL = srv.URL
	return c
}

// TestSearchPeopleParsesResults is modeled directly on the real,
// live-verified response for a search of "Angela Merkel" -- multiple
// candidates share the exact same label (the real chancellor, an
// unrelated board member, and several biography-book entities), which
// is exactly why a bare label match isn't enough on its own.
func TestSearchPeopleParsesResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"searchinfo":{"search":"Angela Merkel"},"search":[
			{"id":"Q567","label":"Angela Merkel","description":"chancellor of Germany from 2005 to 2021 (born 1954)"},
			{"id":"Q94746073","label":"Angela Merkel","description":"Board member of the Moses Mendelssohn Society Dessau"}
		]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	candidates, err := c.SearchPeople("Angela Merkel", 5)
	if err != nil {
		t.Fatalf("SearchPeople: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(candidates))
	}
	if candidates[0].QID != "Q567" || candidates[0].Description == "" {
		t.Errorf("candidates[0] = %+v", candidates[0])
	}
}

func TestSearchPeopleEmptyResultIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"searchinfo":{"search":"zxqvwplkjhqwerty998877"},"search":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	candidates, err := c.SearchPeople("zxqvwplkjhqwerty998877", 5)
	if err != nil {
		t.Fatalf("SearchPeople: %v, want nil error for a genuine zero-match query", err)
	}
	if len(candidates) != 0 {
		t.Errorf("got %d candidates, want 0", len(candidates))
	}
}

func TestSearchPeopleSkipsBlankQuery(t *testing.T) {
	c := NewClient()
	c.APIURL = "http://127.0.0.1:0" // would fail to connect if actually called
	candidates, err := c.SearchPeople("   ", 5)
	if err != nil {
		t.Fatalf("SearchPeople: %v, want no error for a blank query", err)
	}
	if candidates != nil {
		t.Errorf("candidates = %v, want nil (no request made)", candidates)
	}
}

// TestOccupationsIgnoresUnrelatedPropertyShapes guards a real bug this
// package hit live: a real Wikidata entity's claims map mixes many
// different value types across its OTHER properties (dates,
// quantities, strings...) -- decoding the whole claims map with one
// fixed struct shape fails the moment any non-P106 property doesn't
// match it. This fixture includes a P569 (date of birth, a "time"
// value, not an entity reference) alongside P106, modeled on the real
// live response for Angela Merkel (Q567) that originally broke this.
func TestOccupationsIgnoresUnrelatedPropertyShapes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"entities":{"Q567":{"claims":{
			"P106":[{"mainsnak":{"snaktype":"value","property":"P106","datavalue":{"value":{"entity-type":"item","id":"Q82955"},"type":"wikibase-entityid"}}}],
			"P569":[{"mainsnak":{"snaktype":"value","property":"P569","datavalue":{"value":{"time":"+1954-07-17T00:00:00Z"},"type":"time"}}}]
		}}}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	occupations, err := c.Occupations([]string{"Q567"})
	if err != nil {
		t.Fatalf("Occupations: %v", err)
	}
	if len(occupations["Q567"]) != 1 || occupations["Q567"][0] != PoliticianOccupationQID {
		t.Errorf("occupations[Q567] = %v, want [%s]", occupations["Q567"], PoliticianOccupationQID)
	}
}

// TestOccupationsReturnsEmptyForNoOccupationClaims guards a real,
// live-confirmed non-politician homonym: an unrelated "Angela Merkel"
// (Q94746073, a society board member) has zero P106 claims at all --
// this must come back as an empty slice, not an error or a nil map
// entry that a caller might mistake for "not checked".
func TestOccupationsReturnsEmptyForNoOccupationClaims(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"entities":{"Q94746073":{"claims":{}}}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	occupations, err := c.Occupations([]string{"Q94746073"})
	if err != nil {
		t.Fatalf("Occupations: %v", err)
	}
	if len(occupations["Q94746073"]) != 0 {
		t.Errorf("occupations[Q94746073] = %v, want empty", occupations["Q94746073"])
	}
}

func TestOccupationsSkipsSomevalueSnaks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"entities":{"Q1":{"claims":{
			"P106":[{"mainsnak":{"snaktype":"somevalue","property":"P106"}}]
		}}}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	occupations, err := c.Occupations([]string{"Q1"})
	if err != nil {
		t.Fatalf("Occupations: %v", err)
	}
	if len(occupations["Q1"]) != 0 {
		t.Errorf("occupations[Q1] = %v, want empty (a somevalue snak has no datavalue)", occupations["Q1"])
	}
}

func TestOccupationsEmptyQIDsReturnsNil(t *testing.T) {
	c := NewClient()
	c.APIURL = "http://127.0.0.1:0" // would fail to connect if actually called
	occupations, err := c.Occupations(nil)
	if err != nil {
		t.Fatalf("Occupations: %v, want no error for empty qids", err)
	}
	if occupations != nil {
		t.Errorf("occupations = %v, want nil (no request made)", occupations)
	}
}

func TestThrottleRespectsMinInterval(t *testing.T) {
	var count int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		count++
		fmt.Fprint(w, `{"search":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)
	c.MinInterval = 50 * time.Millisecond

	start := time.Now()
	c.SearchPeople("a", 1)
	c.SearchPeople("b", 1)
	if elapsed := time.Since(start); elapsed < c.MinInterval {
		t.Errorf("elapsed = %v, want at least %v between two requests", elapsed, c.MinInterval)
	}
	if count != 2 {
		t.Fatalf("got %d requests, want 2", count)
	}
}
