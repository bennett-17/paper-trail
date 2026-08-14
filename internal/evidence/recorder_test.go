package evidence

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRedactURLRemovesCredentials is the test that matters most in
// this package: an evidence bundle is a file the user may hand to
// someone else, so a leaked API key here is a real security failure,
// not a cosmetic one.
func TestRedactURLRemovesCredentials(t *testing.T) {
	cases := []struct {
		name, raw, mustNotContain string
	}{
		{"SAM.gov style api_key", "https://api.sam.gov/exclusions?api_key=SECRETVALUE123&q=x", "SECRETVALUE123"},
		{"camel apiKey", "https://example.com/v1?apiKey=SECRETVALUE123", "SECRETVALUE123"},
		{"bare key param", "https://example.com/v1?key=SECRETVALUE123", "SECRETVALUE123"},
		{"token", "https://example.com/v1?token=SECRETVALUE123", "SECRETVALUE123"},
		{"access_token", "https://example.com/v1?access_token=SECRETVALUE123", "SECRETVALUE123"},
		{"subscription-key", "https://example.com/v1?subscription-key=SECRETVALUE123", "SECRETVALUE123"},
		{"basic auth in URL", "https://SECRETVALUE123:@api.company-information.service.gov.uk/company/123", "SECRETVALUE123"},
	}
	for _, c := range cases {
		got := RedactURL(c.raw)
		if strings.Contains(got, c.mustNotContain) {
			t.Errorf("%s: RedactURL(%q) = %q -- STILL CONTAINS THE CREDENTIAL", c.name, c.raw, got)
		}
		if !strings.Contains(got, redactedValue) {
			t.Errorf("%s: RedactURL(%q) = %q, want it to show it was redacted", c.name, c.raw, got)
		}
	}
}

func TestRedactURLKeepsNonSecretParams(t *testing.T) {
	got := RedactURL("https://api.example.com/search?q=Example+Ltd&limit=5&api_key=SECRET")
	if !strings.Contains(got, "q=Example+Ltd") {
		t.Errorf("got %q, want the query term preserved -- it's the whole point of the record", got)
	}
	if !strings.Contains(got, "limit=5") {
		t.Errorf("got %q, want ordinary params preserved", got)
	}
	if strings.Contains(got, "SECRET") {
		t.Errorf("got %q, want the credential gone", got)
	}
}

func TestRedactURLUnparseableIsFullyRedacted(t *testing.T) {
	// Better to lose an unparseable URL entirely than to risk writing
	// a credential it might contain.
	if got := RedactURL("://not a url at all?key=SECRET"); strings.Contains(got, "SECRET") {
		t.Errorf("got %q, want no credential from an unparseable URL", got)
	}
}

func TestRecorderCapturesBodyAndLeavesResponseReadable(t *testing.T) {
	const payload = `{"hello":"world"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	rec, err := NewRecorder(dir, []string{"Example Ltd"})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	client := &http.Client{Transport: rec.Transport(nil)}

	resp, err := client.Get(srv.URL + "/search?q=x&api_key=SECRETVALUE123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	// The caller must see exactly what it would have without recording.
	if string(body) != payload {
		t.Errorf("caller got body %q, want %q -- recording must be transparent", body, payload)
	}

	if err := rec.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}
	if strings.Contains(string(manifestData), "SECRETVALUE123") {
		t.Error("the manifest contains a live API key")
	}

	var m Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("got %d manifest entries, want 1", len(m.Entries))
	}
	e := m.Entries[0]
	if e.Status != 200 || e.Bytes != len(payload) {
		t.Errorf("entry = %+v, want status 200 and %d bytes", e, len(payload))
	}
	if e.SHA256 == "" {
		t.Error("entry has no SHA-256 -- tampering would be undetectable")
	}
	if len(m.Queries) != 1 || m.Queries[0] != "Example Ltd" {
		t.Errorf("Queries = %v, want the scan's own query terms recorded", m.Queries)
	}

	// The recorded file must exist and match what the server sent.
	saved, err := os.ReadFile(filepath.Join(dir, e.File))
	if err != nil {
		t.Fatalf("reading recorded response %q: %v", e.File, err)
	}
	if string(saved) != payload {
		t.Errorf("recorded body = %q, want %q", saved, payload)
	}
}

func TestRecorderIsConcurrencySafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	rec, err := NewRecorder(dir, nil)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	client := &http.Client{Transport: rec.Transport(nil)}

	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(fmt.Sprintf("%s/x/%d", srv.URL, i))
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	if got := rec.Count(); got != n {
		t.Errorf("Count() = %d, want %d -- a lost capture means a missing receipt", got, n)
	}
	if err := rec.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "responses"))
	if err != nil {
		t.Fatalf("reading responses dir: %v", err)
	}
	if len(entries) != n {
		t.Errorf("wrote %d response files, want %d (filenames must not collide)", len(entries), n)
	}
}

func TestRecorderRecordsErrorResponsesToo(t *testing.T) {
	// A 403 or 429 is itself evidence -- it documents that a source was
	// unreachable at that moment, which is exactly what a reader needs
	// to interpret a gap in the report.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Access Denied")
	}))
	defer srv.Close()

	dir := t.TempDir()
	rec, _ := NewRecorder(dir, nil)
	client := &http.Client{Transport: rec.Transport(nil)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if rec.Count() != 1 {
		t.Fatalf("Count() = %d, want the 403 recorded", rec.Count())
	}
	if err := rec.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	var m Manifest
	json.Unmarshal(data, &m)
	if m.Entries[0].Status != http.StatusForbidden {
		t.Errorf("recorded status = %d, want 403", m.Entries[0].Status)
	}
}
