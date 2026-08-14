package interpol

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

// This fixture is modeled directly on the real, live-verified
// response shape for a "Smith" search against INTERPOL's public Red
// Notices API (one real record, trimmed to the fields this package
// actually parses).
const realSearchResponse = `{
  "total": 1,
  "query": {"page": 1, "resultPerPage": 3, "name": "Smith"},
  "_embedded": {
    "notices": [
      {
        "name": "SMITH",
        "forename": "TAYLER ANTHONY",
        "date_of_birth": "1999/08/19",
        "nationalities": ["BZ"],
        "entity_id": "2024/10341",
        "_links": {
          "self": {"href": "https://ws-public.interpol.int/notices/v1/red/2024/10341"},
          "images": {"href": "https://ws-public.interpol.int/notices/v1/red/2024/10341/images"},
          "thumbnail": {"href": "https://ws-public.interpol.int/notices/v1/red/2024/10341/images/thumbnail"}
        }
      }
    ]
  },
  "_links": {
    "self": {"href": "https://ws-public.interpol.int/notices/v1/red?name=Smith&page=1&resultPerPage=3"},
    "first": {"href": "https://ws-public.interpol.int/notices/v1/red?name=Smith&page=1&resultPerPage=3"},
    "last": {"href": "https://ws-public.interpol.int/notices/v1/red?name=Smith&page=1&resultPerPage=3"}
  }
}`

func TestSearchByNameParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "Smith" {
			t.Errorf("name param = %q, want Smith", got)
		}
		if got := r.URL.Query().Get("resultPerPage"); got != "3" {
			t.Errorf("resultPerPage param = %q, want 3", got)
		}
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchByName("Smith", 3)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(notices) != 1 {
		t.Fatalf("got %d notices, want 1", len(notices))
	}

	n := notices[0]
	if n.Name != "SMITH" || n.Forename != "TAYLER ANTHONY" {
		t.Errorf("Name/Forename = %q/%q, want SMITH/TAYLER ANTHONY", n.Name, n.Forename)
	}
	if n.DateOfBirth != "1999/08/19" {
		t.Errorf("DateOfBirth = %q, want 1999/08/19", n.DateOfBirth)
	}
	if len(n.Nationalities) != 1 || n.Nationalities[0] != "BZ" {
		t.Errorf("Nationalities = %v, want [BZ]", n.Nationalities)
	}
	if n.EntityID != "2024/10341" {
		t.Errorf("EntityID = %q, want 2024/10341", n.EntityID)
	}
	if n.DetailURL != "https://ws-public.interpol.int/notices/v1/red/2024/10341" {
		t.Errorf("DetailURL = %q, unexpected", n.DetailURL)
	}
	if n.ThumbnailURL != "https://ws-public.interpol.int/notices/v1/red/2024/10341/images/thumbnail" {
		t.Errorf("ThumbnailURL = %q, unexpected", n.ThumbnailURL)
	}
}

func TestSearchByNameNoMatchesReturnsEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total": 0, "_embedded": {"notices": []}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchByName("Zzznomatch", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(notices) != 0 {
		t.Errorf("got %d notices, want 0", len(notices))
	}
}

func TestGetRetriesOn429(t *testing.T) {
	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"total": 0, "_embedded": {"notices": []}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	notices, err := c.SearchByName("anything", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures then a success)", attempts)
	}
	if len(notices) != 0 {
		t.Errorf("got %d notices, want 0", len(notices))
	}
}

func TestGetNonOKStatusReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<HTML>Access Denied</HTML>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchByName("anything", 0); err == nil {
		t.Error("SearchByName with HTTP 403: got nil error, want one")
	}
}
