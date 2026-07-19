package ukcharity

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	c, err := NewClient("test-primary-key", "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.SearchURL = srv.URL + "/searchCharityName/%s"
	c.DetailURL = srv.URL + "/allcharitydetailsV2/%d/%d"
	return c
}

func TestNewClientRequiresAKey(t *testing.T) {
	os.Unsetenv("UK_CHARITY_API_KEY_PRIMARY")
	os.Unsetenv("UK_CHARITY_API_KEY_SECONDARY")
	if _, err := NewClient("", ""); err == nil {
		t.Fatal("expected error when neither key is configured")
	}
}

func TestNewClientFallsBackToEnvVars(t *testing.T) {
	os.Setenv("UK_CHARITY_API_KEY_PRIMARY", "env-primary")
	os.Setenv("UK_CHARITY_API_KEY_SECONDARY", "env-secondary")
	t.Cleanup(func() {
		os.Unsetenv("UK_CHARITY_API_KEY_PRIMARY")
		os.Unsetenv("UK_CHARITY_API_KEY_SECONDARY")
	})

	c, err := NewClient("", "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.PrimaryKey != "env-primary" {
		t.Errorf("PrimaryKey = %q, want env-primary", c.PrimaryKey)
	}
	if c.SecondaryKey != "env-secondary" {
		t.Errorf("SecondaryKey = %q, want env-secondary", c.SecondaryKey)
	}
}

func TestNewClientAcceptsSecondaryKeyAlone(t *testing.T) {
	os.Unsetenv("UK_CHARITY_API_KEY_PRIMARY")
	os.Unsetenv("UK_CHARITY_API_KEY_SECONDARY")
	if _, err := NewClient("", "only-secondary"); err != nil {
		t.Errorf("NewClient with only a secondary key: %v, want no error", err)
	}
}

func TestSearchCharitiesSendsSubscriptionKeyHeader(t *testing.T) {
	var gotKey, gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/searchCharityName/", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Ocp-Apim-Subscription-Key")
		gotPath = r.URL.Path
		fmt.Fprint(w, mustReadFixture(t, "ukcharity_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	charities, err := c.SearchCharities("Scientology")
	if err != nil {
		t.Fatalf("SearchCharities: %v", err)
	}
	if gotKey != "test-primary-key" {
		t.Errorf("Ocp-Apim-Subscription-Key header = %q, want test-primary-key", gotKey)
	}
	if gotPath != "/searchCharityName/Scientology" {
		t.Errorf("request path = %q, want /searchCharityName/Scientology", gotPath)
	}
	if len(charities) != 2 {
		t.Fatalf("got %d charities, want 2", len(charities))
	}

	first := charities[0]
	if first.RegisteredNumber != 283127 {
		t.Errorf("first.RegisteredNumber = %d, want 283127", first.RegisteredNumber)
	}
	if first.Status != "R" {
		t.Errorf("first.Status = %q, want R", first.Status)
	}
}

func TestSearchCharitiesEscapesQueryInPath(t *testing.T) {
	// r.URL.Path is automatically percent-decoded by net/http, so this
	// checks the raw request line (RequestURI) to confirm the client
	// actually sent an escaped path, not what the server decoded it to.
	var gotRequestURI string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchCharities("Save the Children"); err != nil {
		t.Fatalf("SearchCharities: %v", err)
	}
	if gotRequestURI != "/searchCharityName/Save%20the%20Children" {
		t.Errorf("RequestURI = %q, want URL-escaped charity name on the wire", gotRequestURI)
	}
}

func TestGetCharityDetailParsesAddressAndTrustees(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/allcharitydetailsV2/283127/0", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "ukcharity_detail.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	detail, err := c.GetCharityDetail(283127, 0)
	if err != nil {
		t.Fatalf("GetCharityDetail: %v", err)
	}
	if detail.Name != "Church Of Scientology Religious Education College Incorporated" {
		t.Errorf("Name = %q", detail.Name)
	}
	wantAddr := "Saint Hill Manor, Saint Hill Road, East Grinstead"
	if detail.Address != wantAddr {
		t.Errorf("Address = %q, want %q (blank lines should be dropped)", detail.Address, wantAddr)
	}
	if detail.Postcode != "RH19 4JY" {
		t.Errorf("Postcode = %q", detail.Postcode)
	}
	if detail.LatestIncome == nil || *detail.LatestIncome != 1234567 {
		t.Errorf("LatestIncome = %v, want 1234567", detail.LatestIncome)
	}
	if len(detail.Trustees) != 2 || detail.Trustees[0] != "Jane Example" {
		t.Errorf("Trustees = %v", detail.Trustees)
	}
}

func TestGet401ReturnsActionableError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"statusCode":401,"message":"Access denied due to invalid subscription key"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.SearchCharities("anything")
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	if !strings.Contains(err.Error(), "UK_CHARITY_API_KEY_PRIMARY") {
		t.Errorf("error %q should mention UK_CHARITY_API_KEY_PRIMARY so the user knows how to fix it", err.Error())
	}
}

// TestFallsBackToSecondaryKeyOn401 is the actual point of having two
// keys: if the primary is rejected (e.g. mid-rotation, or it was
// regenerated and .env hasn't caught up), the client should retry once
// with the secondary before giving up.
func TestFallsBackToSecondaryKeyOn401(t *testing.T) {
	var keysReceived []string
	mux := http.NewServeMux()
	mux.HandleFunc("/searchCharityName/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Ocp-Apim-Subscription-Key")
		keysReceived = append(keysReceived, key)
		if key != "good-secondary" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"statusCode":401,"message":"Access denied due to invalid subscription key"}`)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "ukcharity_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewClient("stale-primary", "good-secondary")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.SearchURL = srv.URL + "/searchCharityName/%s"

	charities, err := c.SearchCharities("Scientology")
	if err != nil {
		t.Fatalf("SearchCharities: %v, want it to succeed after falling back to the secondary key", err)
	}
	if len(charities) != 2 {
		t.Errorf("got %d charities, want 2", len(charities))
	}
	want := []string{"stale-primary", "good-secondary"}
	if len(keysReceived) != 2 || keysReceived[0] != want[0] || keysReceived[1] != want[1] {
		t.Errorf("keys tried = %v, want %v (primary first, then secondary)", keysReceived, want)
	}
}

// TestBothKeysRejectedReturnsError verifies the client doesn't retry
// forever or silently succeed if neither key works.
func TestBothKeysRejectedReturnsError(t *testing.T) {
	requestCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/searchCharityName/", func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"statusCode":401,"message":"Access denied due to invalid subscription key"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewClient("bad-primary", "bad-secondary")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.SearchURL = srv.URL + "/searchCharityName/%s"

	if _, err := c.SearchCharities("anything"); err == nil {
		t.Fatal("expected an error when both keys are rejected")
	}
	if requestCount != 2 {
		t.Errorf("made %d requests, want exactly 2 (one per key, no extra retries)", requestCount)
	}
}
