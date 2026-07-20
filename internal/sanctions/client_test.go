package sanctions

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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
	c.RetryBaseDelay = time.Millisecond
	c.SearchURL = srv.URL + "/search"
	return c
}

func TestNewClientRequiresAKey(t *testing.T) {
	os.Unsetenv("CSL_API_KEY_PRIMARY")
	os.Unsetenv("CSL_API_KEY_SECONDARY")
	if _, err := NewClient("", ""); err == nil {
		t.Fatal("expected error when neither key is configured")
	}
}

func TestNewClientFallsBackToEnvVars(t *testing.T) {
	os.Setenv("CSL_API_KEY_PRIMARY", "env-primary")
	os.Setenv("CSL_API_KEY_SECONDARY", "env-secondary")
	t.Cleanup(func() {
		os.Unsetenv("CSL_API_KEY_PRIMARY")
		os.Unsetenv("CSL_API_KEY_SECONDARY")
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
	os.Unsetenv("CSL_API_KEY_PRIMARY")
	os.Unsetenv("CSL_API_KEY_SECONDARY")
	if _, err := NewClient("", "only-secondary"); err != nil {
		t.Errorf("NewClient with only a secondary key: %v, want no error", err)
	}
}

func TestSearchEntitiesSendsSubscriptionKeyAsQueryParam(t *testing.T) {
	var gotKey, gotName, gotFuzzy string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("subscription-key")
		gotName = r.URL.Query().Get("name")
		gotFuzzy = r.URL.Query().Get("fuzzy_name")
		fmt.Fprint(w, mustReadFixture(t, "sanctions_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchEntities("Example", true, 0, 0)
	if err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}
	if gotKey != "test-primary-key" {
		t.Errorf("subscription-key query param = %q, want test-primary-key (this API rejects the header form the UK client uses)", gotKey)
	}
	if gotName != "Example" {
		t.Errorf("name param = %q, want Example", gotName)
	}
	if gotFuzzy != "true" {
		t.Errorf("fuzzy_name param = %q, want true", gotFuzzy)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
	if len(result.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(result.Hits))
	}

	first := result.Hits[0]
	if first.Name != "EXAMPLE SANCTIONED ENTITY LTD" {
		t.Errorf("first.Name = %q", first.Name)
	}
	if first.Type != "Entity" {
		t.Errorf("first.Type = %q, want Entity", first.Type)
	}
	if len(first.Programs) != 1 || first.Programs[0] != "RUSSIA-EO14024" {
		t.Errorf("first.Programs = %v", first.Programs)
	}
	if len(first.Addresses) != 1 || first.Addresses[0].City != "Moscow" {
		t.Errorf("first.Addresses = %v", first.Addresses)
	}

	second := result.Hits[1]
	if second.Type != "Individual" {
		t.Errorf("second.Type = %q, want Individual (null country/remarks fields must not break parsing)", second.Type)
	}
}

func TestSearchEntitiesOmitsOffsetAndSizeWhenZero(t *testing.T) {
	var sawOffset, sawSize bool
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		_, sawOffset = r.URL.Query()["offset"]
		_, sawSize = r.URL.Query()["size"]
		fmt.Fprint(w, `{"total":0,"results":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchEntities("Example", false, 0, 0); err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}
	if sawOffset {
		t.Error("offset=0 should omit the offset param entirely")
	}
	if sawSize {
		t.Error("limit=0 should omit the size param entirely")
	}
}

func TestSearchEntitiesNoResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total":0,"sources":[],"results":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchEntities("no such organization anywhere", false, 0, 10)
	if err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}
	if result.Total != 0 || len(result.Hits) != 0 {
		t.Errorf("result = %+v, want a clean empty result", result)
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

	_, err := c.SearchEntities("anything", false, 0, 0)
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
	if !strings.Contains(err.Error(), "CSL_API_KEY_PRIMARY") {
		t.Errorf("error %q should mention CSL_API_KEY_PRIMARY so the user knows how to fix it", err.Error())
	}
}

// TestFallsBackToSecondaryKeyOn401 is the actual point of having two
// keys: if the primary is rejected (e.g. mid-rotation), the client
// should retry once with the secondary before giving up.
func TestFallsBackToSecondaryKeyOn401(t *testing.T) {
	var keysReceived []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("subscription-key")
		keysReceived = append(keysReceived, key)
		if key != "good-secondary" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"statusCode":401,"message":"Access denied due to invalid subscription key"}`)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "sanctions_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewClient("stale-primary", "good-secondary")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.SearchURL = srv.URL + "/search"

	result, err := c.SearchEntities("Example", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchEntities: %v, want it to succeed after falling back to the secondary key", err)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
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
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
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
	c.SearchURL = srv.URL + "/search"

	if _, err := c.SearchEntities("anything", false, 0, 0); err == nil {
		t.Fatal("expected an error when both keys are rejected")
	}
	if requestCount != 2 {
		t.Errorf("made %d requests, want exactly 2 (one per key, no extra retries)", requestCount)
	}
}

// TestRetriesOn429ThenSucceeds is the actual point of retry-with-backoff:
// a transient rate-limit response shouldn't surface as a hard failure if
// a retry on the same key would have worked -- observed live when a
// `risk` scan screens many names in a short burst.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"statusCode":429,"message":"Rate limit exceeded"}`)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "sanctions_search_results.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	result, err := c.SearchEntities("Example", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchEntities: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
}

// TestGivesUpOn429AfterMaxRetries verifies the client doesn't retry
// forever against a persistently rate-limited endpoint, and that it
// still tries the secondary key afterward (a second key may have its
// own separate quota).
func TestGivesUpOn429AfterMaxRetries(t *testing.T) {
	var keysReceived []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		keysReceived = append(keysReceived, r.URL.Query().Get("subscription-key"))
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"statusCode":429,"message":"Rate limit exceeded"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewClient("primary-key", "secondary-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.RetryBaseDelay = time.Millisecond
	c.MaxRetries = 2
	c.SearchURL = srv.URL + "/search"

	if _, err := c.SearchEntities("anything", false, 0, 0); err == nil {
		t.Fatal("expected an error when every attempt on both keys is rate-limited")
	}
	// MaxRetries=2 means 3 attempts per key (the original try plus 2
	// retries), times 2 keys.
	if len(keysReceived) != 6 {
		t.Errorf("made %d requests, want 6 (3 attempts x 2 keys)", len(keysReceived))
	}
	for i := 0; i < 3; i++ {
		if keysReceived[i] != "primary-key" {
			t.Errorf("request %d used key %q, want primary-key", i, keysReceived[i])
		}
	}
	for i := 3; i < 6; i++ {
		if keysReceived[i] != "secondary-key" {
			t.Errorf("request %d used key %q, want secondary-key", i, keysReceived[i])
		}
	}
}
