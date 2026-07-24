package wayback

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
	c.BaseURL = srv.URL
	return c
}

// TestEarliestSnapshotParsesRealShape is modeled directly on the real,
// live-verified CDX response for "bbc.co.uk".
func TestEarliestSnapshotParsesRealShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[["urlkey","timestamp","original","mimetype","statuscode","digest","length"],
["uk,co,bbc)/","19961221203254","http://www0.bbc.co.uk:80/","text/html","200","WKWTY2XHFDOAPMQLJTK2T7ZSUUSBNNVX","447"]]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	got, found, err := c.EarliestSnapshot("bbc.co.uk")
	if err != nil {
		t.Fatalf("EarliestSnapshot: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	want := time.Date(1996, 12, 21, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestEarliestSnapshotTreatsWrapped503AsNotFound guards a real,
// live-confirmed quirk: a host with no archived snapshots at all
// returns an HTML "503 Service Unavailable" page wrapped in an HTTP
// 200 status, not a clean empty JSON result or a real error status --
// reproduced consistently against a deliberately-nonexistent domain.
func TestEarliestSnapshotTreatsWrapped503AsNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><h1>503 Service Unavailable</h1>
No server is available to handle this request.
</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, found, err := c.EarliestSnapshot("nonexistent-domain-xyz.example")
	if err != nil {
		t.Fatalf("EarliestSnapshot: %v, want nil error for the documented-live no-snapshot shape", err)
	}
	if found {
		t.Error("found = true, want false")
	}
}

// TestEarliestSnapshotRetriesRealServiceUnavailable guards the other
// real, live-confirmed quirk of this service: a genuine HTTP 503 (not
// wrapped in 200) reproduced for a host that had just succeeded
// seconds earlier -- transient flakiness distinct from the
// wrapped-503-as-200 "no snapshot" case, retried the same way every
// other unreliable client in this project handles its own transient
// failures.
func TestEarliestSnapshotRetriesRealServiceUnavailable(t *testing.T) {
	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `[["urlkey","timestamp","original","mimetype","statuscode","digest","length"],
["com,example)/","20020120142510","http://example.com:80/","text/html","200","X","1"]]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, found, err := c.EarliestSnapshot("example.com")
	if err != nil {
		t.Fatalf("EarliestSnapshot: %v", err)
	}
	if !found {
		t.Error("found = false, want true after retries succeed")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestEarliestSnapshotEmptyHost(t *testing.T) {
	c := NewClient()
	c.BaseURL = "http://127.0.0.1:0" // would fail to connect if actually called
	_, found, err := c.EarliestSnapshot("")
	if err != nil {
		t.Fatalf("EarliestSnapshot: %v, want no error for an empty host", err)
	}
	if found {
		t.Error("found = true, want false (no request made)")
	}
}
