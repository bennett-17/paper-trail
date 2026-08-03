package gazette

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

// This fixture is modeled directly on the real, live-verified Gazette
// insolvency notice search response for "Wells Fargo", trimmed to the
// fields this package actually parses.
const realSearchResponse = `{
  "f:total": "7",
  "entry": [
    {
      "id": "https://www.thegazette.co.uk/id/notice/L-59731-1323921",
      "f:status": "published",
      "title": "WELLS FARGO SECURITIES LIMITED",
      "link": [
        {"@href":"https://www.thegazette.co.uk/id/notice/L-59731-1323921","@rel":"self"},
        {"@href":"https://www.thegazette.co.uk/notice/L-59731-1323921"},
        {"@href":"https://www.thegazette.co.uk/notice/L-59731-1323921/data.ttl","@rel":"alternate","@title":"TURTLE"}
      ],
      "published": "2011-03-21T00:00:00",
      "category": {"@term": "Final Meetings"}
    },
    {
      "id": "https://www.thegazette.co.uk/id/notice/L-59342-1049966",
      "f:status": "published",
      "title": "WELLS FARGO SECURITIES LIMITED",
      "link": [
        {"@href":"https://www.thegazette.co.uk/id/notice/L-59342-1049966","@rel":"self"},
        {"@href":"https://www.thegazette.co.uk/notice/L-59342-1049966"}
      ],
      "published": "2010-02-23T00:00:00",
      "category": {"@term": "Notices to Creditors"}
    }
  ]
}`

func TestSearchInsolvencyNoticesParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/insolvency/notice/data.json", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("text"); got != "Wells Fargo" {
			t.Errorf("text param = %q, want Wells Fargo", got)
		}
		// Regression guard for a real, live-confirmed bug: The Gazette's
		// server 500s on any Accept header at all -- must never be set.
		if got := r.Header.Get("Accept"); got != "" {
			t.Errorf("Accept header = %q, want none set (The Gazette 500s if one is present)", got)
		}
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchInsolvencyNotices("Wells Fargo", 0)
	if err != nil {
		t.Fatalf("SearchInsolvencyNotices: %v", err)
	}
	if len(notices) != 2 {
		t.Fatalf("got %d notices, want 2", len(notices))
	}
	n := notices[0]
	if n.Title != "WELLS FARGO SECURITIES LIMITED" {
		t.Errorf("Title = %q", n.Title)
	}
	if n.Category != "Final Meetings" {
		t.Errorf("Category = %q, want Final Meetings", n.Category)
	}
	if n.Published != "2011-03-21T00:00:00" {
		t.Errorf("Published = %q", n.Published)
	}
	if n.URL != "https://www.thegazette.co.uk/notice/L-59731-1323921" {
		t.Errorf("URL = %q, want the bare (no-@rel) notice page link", n.URL)
	}
}

func TestSearchInsolvencyNoticesReturnsEmptyOnNoMatches(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/insolvency/notice/data.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"f:total":"0","entry":null}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchInsolvencyNotices("Zzznonexistent", 0)
	if err != nil {
		t.Fatalf("SearchInsolvencyNotices: %v, want a null entry array to be a normal empty result, not an error", err)
	}
	if len(notices) != 0 {
		t.Errorf("got %d notices, want 0", len(notices))
	}
}

func TestSearchInsolvencyNoticesRespectsLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/insolvency/notice/data.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchInsolvencyNotices("Wells Fargo", 1)
	if err != nil {
		t.Fatalf("SearchInsolvencyNotices: %v", err)
	}
	if len(notices) != 1 {
		t.Fatalf("got %d notices, want 1 (limit)", len(notices))
	}
}

func TestSearchInsolvencyNoticesReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/insolvency/notice/data.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchInsolvencyNotices("Example", 0); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/insolvency/notice/data.json", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchInsolvencyNotices("Wells Fargo", 0)
	if err != nil {
		t.Fatalf("SearchInsolvencyNotices: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if len(notices) != 2 {
		t.Errorf("got %d notices, want 2", len(notices))
	}
}
