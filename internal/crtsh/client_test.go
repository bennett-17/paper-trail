package crtsh

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

// This fixture is modeled on a real, live-verified crt.sh response for
// example.com: two CT-log entries (different issuer_ca_id, same
// serial_number, i.e. the same certificate logged twice) that should
// collapse into one Certificate, plus a second, genuinely distinct
// certificate.
const twoLogEntriesOneCertificate = `[
  {"issuer_ca_id":413868,"issuer_name":"C=US, O=SSL Corporation, CN=Cloudflare TLS Issuing ECC CA 3","common_name":"example.com","name_value":"*.example.com\nexample.com","id":1,"entry_timestamp":"2026-07-29T22:20:19.316","not_before":"2026-07-29T22:10:08","not_after":"2026-10-27T22:17:21","serial_number":"aaaa","result_count":2},
  {"issuer_ca_id":413864,"issuer_name":"C=US, O=SSL Corporation, CN=Cloudflare TLS Issuing RSA CA 3","common_name":"example.com","name_value":"*.example.com\nexample.com","id":2,"entry_timestamp":"2026-07-29T22:20:18.763","not_before":"2026-07-29T22:09:26","not_after":"2026-10-27T22:17:20","serial_number":"bbbb","result_count":2}
]`

func TestCertificatesCollapsesMultipleLogEntriesIntoOneCertificate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "example.com" {
			t.Errorf("q param = %q, want example.com", got)
		}
		fmt.Fprint(w, twoLogEntriesOneCertificate)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	certs, err := c.Certificates("example.com")
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("got %d certificates, want 2 distinct issuer+serial pairs, got %+v", len(certs), certs)
	}
	if len(certs[0].SANs) != 2 {
		t.Errorf("SANs = %v, want [*.example.com example.com]", certs[0].SANs)
	}
	if !certs[0].NotBefore.Equal(time.Date(2026, 7, 29, 22, 10, 8, 0, time.UTC)) {
		t.Errorf("NotBefore = %v, want 2026-07-29 22:10:08", certs[0].NotBefore)
	}
}

func TestCertificatesSplitsSANsAcrossMultipleNames(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"issuer_ca_id":1,"issuer_name":"Test CA","common_name":"foo.com","name_value":"foo.com\nbar.org\nwww.foo.com","not_before":"2026-01-01T00:00:00","not_after":"2026-04-01T00:00:00","serial_number":"cccc"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	certs, err := c.Certificates("foo.com")
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("got %d certificates, want 1", len(certs))
	}
	want := map[string]bool{"foo.com": true, "bar.org": true, "www.foo.com": true}
	if len(certs[0].SANs) != len(want) {
		t.Fatalf("SANs = %v, want %v", certs[0].SANs, want)
	}
	for _, s := range certs[0].SANs {
		if !want[s] {
			t.Errorf("unexpected SAN %q", s)
		}
	}
}

func TestCertificatesNoMatchReturnsEmptyNotError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	certs, err := c.Certificates("no-such-domain-zzz.com")
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if len(certs) != 0 {
		t.Errorf("got %+v, want empty", certs)
	}
}

func TestCertificatesRetriesOn502(t *testing.T) {
	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Certificates("example.com")
	if err != nil {
		t.Fatalf("Certificates: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures + 1 success)", attempts)
	}
}

func TestCertificatesEmptyDomainReturnsNothing(t *testing.T) {
	c := NewClient()
	certs, err := c.Certificates("")
	if err != nil || certs != nil {
		t.Errorf("got (%v, %v), want (nil, nil) for empty domain", certs, err)
	}
}
