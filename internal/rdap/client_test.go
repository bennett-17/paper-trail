package rdap

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer builds a mux-backed test server whose own bootstrap
// endpoint (served at /dns.json) points back at itself for the given
// TLD -- the bootstrap file's RDAP base URLs are always full external
// URLs in real life, so tests need to reference the just-created
// server's own address.
func newTestServer(t *testing.T, tld string) (*httptest.Server, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/dns.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"services":[[[%q],[%q]]]}`, tld, srv.URL+"/rdap/")
	})
	return srv, mux
}

func newTestClient(srv *httptest.Server) *Client {
	c := NewClient()
	c.MinInterval = 0
	c.BootstrapURL = srv.URL + "/dns.json"
	return c
}

// TestRegistrationDateParsesRealShape is modeled directly on the real,
// live-verified RDAP response shape for google.com via Verisign's own
// RDAP server.
func TestRegistrationDateParsesRealShape(t *testing.T) {
	srv, mux := newTestServer(t, "com")
	mux.HandleFunc("/rdap/domain/example.com", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"events":[{"eventAction":"registration","eventDate":"1997-09-15T04:00:00Z"},{"eventAction":"expiration","eventDate":"2028-09-14T04:00:00Z"}]}`)
	})
	c := newTestClient(srv)

	got, err := c.RegistrationDate("example.com")
	if err != nil {
		t.Fatalf("RegistrationDate: %v", err)
	}
	want := time.Date(1997, 9, 15, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestRegistrationDateStripsSubdomain guards a real, live-confirmed
// RDAP behavior: querying an arbitrary subdomain (e.g. "www.google.com")
// 404s where the bare registered domain succeeds -- this project has
// no public-suffix-list dependency, so RegistrationDate strips one
// leading label at a time and retries instead.
func TestRegistrationDateStripsSubdomain(t *testing.T) {
	srv, mux := newTestServer(t, "com")
	mux.HandleFunc("/rdap/domain/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rdap/domain/example.com" {
			fmt.Fprint(w, `{"events":[{"eventAction":"registration","eventDate":"1997-09-15T04:00:00Z"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestClient(srv)

	got, err := c.RegistrationDate("www.example.com")
	if err != nil {
		t.Fatalf("RegistrationDate: %v", err)
	}
	want := time.Date(1997, 9, 15, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRegistrationDateUnknownTLDReturnsError(t *testing.T) {
	srv, _ := newTestServer(t, "com")
	c := newTestClient(srv)

	if _, err := c.RegistrationDate("example.zzzznotarealtld"); err == nil {
		t.Error("expected an error for a TLD not in the bootstrap registry")
	}
}

func TestRegistrationDateNoRecordReturnsError(t *testing.T) {
	srv, mux := newTestServer(t, "com")
	mux.HandleFunc("/rdap/domain/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestClient(srv)

	if _, err := c.RegistrationDate("neverregistered.com"); err == nil {
		t.Error("expected an error when no RDAP record exists at all")
	}
}

func TestRegistrationDateNoRegistrationEventReturnsError(t *testing.T) {
	srv, mux := newTestServer(t, "com")
	mux.HandleFunc("/rdap/domain/example.com", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"events":[{"eventAction":"expiration","eventDate":"2028-09-14T04:00:00Z"}]}`)
	})
	c := newTestClient(srv)

	if _, err := c.RegistrationDate("example.com"); err == nil {
		t.Error("expected an error when the record has no registration event")
	}
}

func TestRegistrationDateFetchesBootstrapOnlyOnce(t *testing.T) {
	var bootstrapHits int
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/dns.json", func(w http.ResponseWriter, r *http.Request) {
		bootstrapHits++
		fmt.Fprintf(w, `{"services":[[["com"],[%q]]]}`, srv.URL+"/rdap/")
	})
	mux.HandleFunc("/rdap/domain/example.com", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"events":[{"eventAction":"registration","eventDate":"1997-09-15T04:00:00Z"}]}`)
	})
	mux.HandleFunc("/rdap/domain/other.com", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"events":[{"eventAction":"registration","eventDate":"2020-01-01T00:00:00Z"}]}`)
	})
	c := newTestClient(srv)

	if _, err := c.RegistrationDate("example.com"); err != nil {
		t.Fatalf("RegistrationDate: %v", err)
	}
	if _, err := c.RegistrationDate("other.com"); err != nil {
		t.Fatalf("RegistrationDate: %v", err)
	}
	if bootstrapHits != 1 {
		t.Errorf("bootstrap fetched %d times, want 1 (cached per Client)", bootstrapHits)
	}
}

func TestRegistrationDateEmptyDomain(t *testing.T) {
	c := NewClient()
	c.BootstrapURL = "http://127.0.0.1:0" // would fail to connect if actually called
	if _, err := c.RegistrationDate(""); err == nil {
		t.Error("expected an error for an empty domain")
	}
}
