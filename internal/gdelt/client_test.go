package gdelt

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	c := NewClient()
	c.MinInterval = 0
	c.RetryBaseDelay = 0
	c.BaseURL = srv.URL
	return c
}

// TestSearchArticlesParsesResults is modeled directly on the real,
// live response for a query of "Mossack Fonseca" -- a real, current
// ICIJ/American Banker story about a Swedbank Panama Papers fine.
func TestSearchArticlesParsesResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "gdelt_search.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	articles, err := c.SearchArticles("Mossack Fonseca", 0)
	if err != nil {
		t.Fatalf("SearchArticles: %v", err)
	}
	if len(articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(articles))
	}
	if articles[0].Domain != "icij.org" || articles[0].SourceCountry != "United States" {
		t.Errorf("articles[0] = %+v", articles[0])
	}
	if articles[0].Title == "" || articles[0].URL == "" {
		t.Errorf("articles[0] missing Title/URL: %+v", articles[0])
	}
	if articles[0].SeenDate != "20260717T224500Z" {
		t.Errorf("articles[0].SeenDate = %q", articles[0].SeenDate)
	}
}

// TestSearchArticlesQuotesMultiWordQuery confirms a multi-word query
// is sent as an exact phrase (quoted), not a loose OR-of-words search
// -- otherwise a company name like "Acme Holdings" would match any
// article mentioning "Acme" OR "Holdings" separately.
func TestSearchArticlesQuotesMultiWordQuery(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchArticles("Acme Holdings", 0); err != nil {
		t.Fatalf("SearchArticles: %v", err)
	}
	if gotQuery != `"Acme Holdings"` {
		t.Errorf("query sent = %q, want a quoted exact phrase", gotQuery)
	}
}

// TestSearchArticlesEmptyResultIsNotAnError guards a real, confirmed-
// live find: a query with no matches returns a bare "{}" (no
// "articles" key at all) with HTTP 200, not an error.
func TestSearchArticlesEmptyResultIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	articles, err := c.SearchArticles("zxqvwplkjhqwerty998877", 0)
	if err != nil {
		t.Fatalf("SearchArticles: %v, want nil error for a genuine zero-match query", err)
	}
	if len(articles) != 0 {
		t.Errorf("got %d articles, want 0", len(articles))
	}
}

func TestSearchArticlesSkipsBlankQuery(t *testing.T) {
	c := NewClient()
	c.BaseURL = "http://127.0.0.1:0" // would fail to connect if actually called
	articles, err := c.SearchArticles("   ", 0)
	if err != nil {
		t.Fatalf("SearchArticles: %v, want no error for a blank query", err)
	}
	if articles != nil {
		t.Errorf("articles = %v, want nil (no request made)", articles)
	}
}

// TestSearchArticlesReturnsErrorWithPlainTextBody guards a real,
// confirmed-live find: GDELT's own rate-limit error response is plain
// text, not JSON -- the error message must still surface usefully
// rather than failing to parse or being swallowed.
func TestSearchArticlesReturnsErrorWithPlainTextBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "Please limit requests to one every 5 seconds...")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)
	c.MaxRetries = 0 // don't wait through retries in this test

	_, err := c.SearchArticles("Example", 0)
	if err == nil {
		t.Fatal("expected an error for a 429 response")
	}
	if got := err.Error(); !strings.Contains(got, "limit requests") {
		t.Errorf("error = %q, want it to include GDELT's own plain-text message", got)
	}
}

// TestSearchArticlesEncodesQueryParamsCorrectly is a light sanity
// check that maxrecords/mode/format all land in the request GDELT
// actually receives.
func TestSearchArticlesSetsExpectedParams(t *testing.T) {
	var got url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchArticles("Example", 5); err != nil {
		t.Fatalf("SearchArticles: %v", err)
	}
	if got.Get("mode") != "artlist" || got.Get("format") != "json" || got.Get("maxrecords") != "5" {
		t.Errorf("params = %v, missing expected mode/format/maxrecords", got)
	}
}

// TestRetriesOn429ThenSucceeds mirrors every other client package's
// retry behavior in this project.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, "Please limit requests to one every 5 seconds...")
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "gdelt_search.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	articles, err := c.SearchArticles("Mossack Fonseca", 0)
	if err != nil {
		t.Fatalf("SearchArticles: %v, want it to succeed after retrying past the 429", err)
	}
	if attempts != 2 {
		t.Errorf("made %d attempts, want 2 (one 429 then a success)", attempts)
	}
	if len(articles) != 2 {
		t.Errorf("got %d articles, want 2", len(articles))
	}
}
