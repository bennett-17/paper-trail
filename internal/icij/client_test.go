package icij

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
	c.BaseURL = srv.URL
	return c
}

// TestSearchParsesResults is modeled directly on the real, live
// response for a reconciliation query of "MOSSACK FONSECA & CO." --
// an exact-name match against a real Panama Papers intermediary.
func TestSearchParsesResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "icij_reconcile.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	matches, err := c.Search("MOSSACK FONSECA & CO.", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	if matches[0].Name != "MOSSACK FONSECA & CO." || !matches[0].Match || matches[0].Score != 100.0 {
		t.Errorf("matches[0] = %+v, want an exact match at score 100", matches[0])
	}
	if matches[0].Type != "Intermediary" {
		t.Errorf("matches[0].Type = %q, want Intermediary", matches[0].Type)
	}
	if matches[1].Match {
		t.Errorf("matches[1].Match = true, want false (a weaker, lower-score candidate)")
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "icij_reconcile.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	matches, err := c.Search("MOSSACK FONSECA & CO.", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1 (limit applied)", len(matches))
	}
}

func TestSearchSkipsEmptyName(t *testing.T) {
	c := NewClient()
	c.BaseURL = "http://127.0.0.1:0" // would fail to connect if actually called
	matches, err := c.Search("   ", 0)
	if err != nil {
		t.Fatalf("Search: %v, want no error for a blank name", err)
	}
	if matches != nil {
		t.Errorf("matches = %v, want nil (no request made)", matches)
	}
}

func TestSearchReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"code":500,"message":"Server Error"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.Search("Example", 0); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

// TestRetriesOn429ThenSucceeds mirrors internal/companieshouse,
// internal/sanctions, internal/edgar, internal/nonprofit,
// internal/aucharity, internal/ukcharity, and internal/ofsi's retry
// behavior.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "icij_reconcile.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	matches, err := c.Search("MOSSACK FONSECA & CO.", 0)
	if err != nil {
		t.Fatalf("Search: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if len(matches) != 2 {
		t.Errorf("got %d matches, want 2", len(matches))
	}
}
