package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bennett-17/paper-trail/internal/crtsh"
	"github.com/bennett-17/paper-trail/internal/gdelt"
	"github.com/bennett-17/paper-trail/internal/risk"
)

// TestAverageToneRealCalibrationExamples is modeled on the real,
// live-verified GDELT timelinetone data this project calibrated
// gdeltNegativeToneThreshold against: "Swedbank" (routine bank
// coverage) averaged +0.01 over 81 days, well clear of the threshold;
// "Wirecard" (the real, proven accounting-fraud collapse) averaged
// -0.61 over the same window length, clearly crossing it.
func TestAverageToneRealCalibrationExamples(t *testing.T) {
	swedbank := []gdelt.TonePoint{{Value: 2.3946}, {Value: -4.0816}, {Value: 2.4}}
	if avg, ok := averageTone(swedbank); !ok || avg <= gdeltNegativeToneThreshold {
		t.Errorf("avg = %v, ok = %v, want above threshold %v (routine coverage shouldn't fire)", avg, ok, gdeltNegativeToneThreshold)
	}

	wirecard := []gdelt.TonePoint{{Value: -0.5}, {Value: -0.7}, {Value: -0.62}}
	if avg, ok := averageTone(wirecard); !ok || avg > gdeltNegativeToneThreshold {
		t.Errorf("avg = %v, ok = %v, want at or below threshold %v (a proven fraud collapse should cross it)", avg, ok, gdeltNegativeToneThreshold)
	}
}

func TestAverageToneEmptyIsNotOK(t *testing.T) {
	if avg, ok := averageTone(nil); ok {
		t.Errorf("ok = true for empty points (avg = %v), want false -- nothing to average", avg)
	}
}

func newTestCrtshClient(t *testing.T, srv *httptest.Server) *crtsh.Client {
	t.Helper()
	c := crtsh.NewClient()
	c.MinInterval = 0
	c.RetryBaseDelay = time.Millisecond
	c.BaseURL = srv.URL
	return c
}

// TestScreenCertificateTransparencyFlagsSharedCertificateAcrossEntities
// models the core signal this check exists for: two different
// entities whose own, otherwise-unrelated-looking domains turn out to
// be covered by the exact same TLS certificate.
func TestScreenCertificateTransparencyFlagsSharedCertificateAcrossEntities(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"issuer_ca_id":1,"issuer_name":"Test CA","common_name":"entity-a.com","name_value":"entity-a.com\nentity-b.com","not_before":"2026-01-01T00:00:00","not_after":"2026-04-01T00:00:00","serial_number":"shared-cert"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newTestCrtshClient(t, srv)

	entities := []risk.Entity{
		risk.NewEntity("edgar", "1", "Entity A", nil, nil),
		risk.NewEntity("edgar", "2", "Entity B", nil, nil),
	}
	entities[0].Websites = []string{"https://entity-a.com"}
	entities[1].Websites = []string{"https://entity-b.com"}

	extra, notes := screenCertificateTransparency(client, entities, newProgressReporter(io.Discard))
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	var found *risk.Indicator
	for i := range extra {
		if extra[i].Code == "ct_shared_certificate" {
			found = &extra[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a ct_shared_certificate indicator, got %+v", extra)
	}
	if len(found.Entities) != 2 {
		t.Errorf("Entities = %v, want both entity labels", found.Entities)
	}
	if !strings.Contains(found.Evidence, "entity-a.com") || !strings.Contains(found.Evidence, "entity-b.com") {
		t.Errorf("Evidence = %q, want both domains named", found.Evidence)
	}
}

// TestScreenCertificateTransparencyIgnoresSameEntitysOwnDomains
// confirms one entity legitimately listing two of its own domains
// under one certificate isn't treated as a cross-entity finding.
func TestScreenCertificateTransparencyIgnoresSameEntitysOwnDomains(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"issuer_ca_id":1,"issuer_name":"Test CA","common_name":"brand-one.com","name_value":"brand-one.com\nbrand-two.com","not_before":"2026-01-01T00:00:00","not_after":"2026-04-01T00:00:00","serial_number":"self-cert"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newTestCrtshClient(t, srv)

	e := risk.NewEntity("edgar", "1", "One Company", nil, nil)
	e.Websites = []string{"https://brand-one.com", "https://brand-two.com"}

	extra, _ := screenCertificateTransparency(client, []risk.Entity{e}, newProgressReporter(io.Discard))
	for _, ind := range extra {
		if ind.Code == "ct_shared_certificate" {
			t.Fatalf("one entity's own two domains must not fire a cross-entity indicator: %+v", ind)
		}
	}
}

// TestScreenCertificateTransparencyIgnoresSubdomainOfSameDomain
// confirms a certificate covering only a wildcard/apex pair of the
// SAME domain (the overwhelmingly common case) never fires, since
// there's no second, different domain involved at all.
func TestScreenCertificateTransparencyIgnoresSubdomainOfSameDomain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"issuer_ca_id":1,"issuer_name":"Test CA","common_name":"example.com","name_value":"*.example.com\nexample.com","not_before":"2026-01-01T00:00:00","not_after":"2026-04-01T00:00:00","serial_number":"ordinary-cert"}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := newTestCrtshClient(t, srv)

	e := risk.NewEntity("edgar", "1", "Ordinary Co", nil, nil)
	e.Websites = []string{"https://example.com"}

	extra, _ := screenCertificateTransparency(client, []risk.Entity{e}, newProgressReporter(io.Discard))
	if len(extra) != 0 {
		t.Errorf("got %+v, want no indicators for an ordinary same-domain certificate", extra)
	}
}

func TestScreenCertificateTransparencyNoWebsitesReturnsNothing(t *testing.T) {
	e := risk.NewEntity("edgar", "1", "No Website Co", nil, nil)
	extra, notes := screenCertificateTransparency(nil, []risk.Entity{e}, newProgressReporter(io.Discard))
	if extra != nil || notes != nil {
		t.Errorf("got (%v, %v), want (nil, nil) when no entity has a website", extra, notes)
	}
}

func TestWebsiteHostStripsSchemeAndWWW(t *testing.T) {
	cases := map[string]string{
		"https://www.example.org/": "example.org",
		"http://example.org":       "example.org",
		"example.org":              "example.org",
		"WWW.Example.ORG":          "example.org",
		"":                         "",
	}
	for input, want := range cases {
		if got := websiteHost(input); got != want {
			t.Errorf("websiteHost(%q) = %q, want %q", input, got, want)
		}
	}
}
