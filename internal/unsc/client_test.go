package unsc

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
	c.URL = srv.URL
	return c
}

// TestListParsesIndividualsAndEntities is modeled directly on a real
// excerpt of the live Consolidated List XML (individuals BADEGE and
// MUHINDO AKILI MUNDOS, and entity ADF, all under the DRC sanctions
// committee).
func TestListParsesIndividualsAndEntities(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "unsc_consolidated.xml"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	designations, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(designations) != 3 {
		t.Fatalf("got %d designations, want 3 (2 individuals + 1 entity)", len(designations))
	}

	if designations[0].Name != "ERIC BADEGE" || designations[0].IsEntity {
		t.Errorf("designations[0] = %+v, want individual ERIC BADEGE", designations[0])
	}
	// The fixture's second alias entry is blank (matching a real one
	// confirmed live) and must be dropped, not returned as an empty
	// string.
	if len(designations[0].Aliases) != 1 || designations[0].Aliases[0] != "Eric Badeje" {
		t.Errorf("designations[0].Aliases = %v, want just [\"Eric Badeje\"]", designations[0].Aliases)
	}
	if designations[0].ListType != "DRC" || designations[0].ReferenceNumber != "CDi.001" {
		t.Errorf("designations[0] ListType/ReferenceNumber = %q/%q", designations[0].ListType, designations[0].ReferenceNumber)
	}

	if designations[1].Name != "MUHINDO AKILI MUNDOS" {
		t.Errorf("designations[1].Name = %q, want MUHINDO AKILI MUNDOS (FIRST+SECOND+THIRD name joined)", designations[1].Name)
	}

	if designations[2].Name != "ADF" || !designations[2].IsEntity {
		t.Errorf("designations[2] = %+v, want entity ADF", designations[2])
	}
	if len(designations[2].Aliases) != 2 || designations[2].Aliases[0] != "Allied Democratic Forces" {
		t.Errorf("designations[2].Aliases = %v", designations[2].Aliases)
	}
}

// TestListCachesAfterFirstFetch confirms the sync.Once caching: a
// second List() call must not trigger a second HTTP request.
func TestListCachesAfterFirstFetch(t *testing.T) {
	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, mustReadFixture(t, "unsc_consolidated.xml"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.List(); err != nil {
		t.Fatalf("first List: %v", err)
	}
	if _, err := c.List(); err != nil {
		t.Fatalf("second List: %v", err)
	}
	if requests != 1 {
		t.Errorf("made %d requests, want 1 (second List() should use the cached copy)", requests)
	}
}

func TestListReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.List(); err == nil {
		t.Fatal("expected an error for a 503 response")
	}
}
