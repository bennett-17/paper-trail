package edgar

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

// newTestServer serves fixture content and lets each test control what's
// returned for the tickers endpoint, the submissions endpoint, and the
// browse-edgar (Atom) endpoint independently.
func newTestServer(t *testing.T, tickersBody, submissionsBody, atomBody string, submissionsStatus, atomStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/tickers.json", func(w http.ResponseWriter, r *http.Request) {
		if tickersBody == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, tickersBody)
	})
	mux.HandleFunc("/submissions/", func(w http.ResponseWriter, r *http.Request) {
		if submissionsStatus != 0 && submissionsStatus != http.StatusOK {
			w.WriteHeader(submissionsStatus)
			return
		}
		fmt.Fprint(w, submissionsBody)
	})
	mux.HandleFunc("/browse-edgar", func(w http.ResponseWriter, r *http.Request) {
		if atomStatus != 0 && atomStatus != http.StatusOK {
			w.WriteHeader(atomStatus)
			return
		}
		fmt.Fprint(w, atomBody)
	})
	return httptest.NewServer(mux)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient("Test Suite test@example.com")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.TickersURL = srv.URL + "/tickers.json"
	c.SubmissionsURL = srv.URL + "/submissions/CIK%s.json"
	c.BrowseEdgarURL = srv.URL + "/browse-edgar"
	return c
}

func TestNewClientRequiresUserAgent(t *testing.T) {
	os.Unsetenv("EDGAR_USER_AGENT")
	if _, err := NewClient(""); err == nil {
		t.Fatal("expected error when no user agent is configured")
	}
}

func TestNewClientRejectsUserAgentWithoutEmail(t *testing.T) {
	if _, err := NewClient("Just A Name"); err == nil {
		t.Fatal("expected error for user agent without an email address")
	}
}

func TestResolveCIKByTicker(t *testing.T) {
	tickers := mustReadFixture(t, "company_tickers.json")
	srv := newTestServer(t, tickers, "", "", 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	cik, err := c.ResolveCIK("AAPL")
	if err != nil {
		t.Fatalf("ResolveCIK: %v", err)
	}
	if cik != "0000320193" {
		t.Errorf("got CIK %s, want 0000320193", cik)
	}
}

func TestResolveCIKByExactName(t *testing.T) {
	tickers := mustReadFixture(t, "company_tickers.json")
	srv := newTestServer(t, tickers, "", "", 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	cik, err := c.ResolveCIK("Apple Inc.")
	if err != nil {
		t.Fatalf("ResolveCIK: %v", err)
	}
	if cik != "0000320193" {
		t.Errorf("got CIK %s, want 0000320193", cik)
	}
}

func TestResolveCIKNoMatch(t *testing.T) {
	tickers := mustReadFixture(t, "company_tickers.json")
	srv := newTestServer(t, tickers, "", "", 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.ResolveCIK("Definitely Not A Real Company XYZ"); err == nil {
		t.Fatal("expected error for no match")
	}
}

func TestResolveCIKAmbiguous(t *testing.T) {
	ambiguous := `{
		"0": {"cik_str": 1, "ticker": "AAA", "title": "Alpha Corp"},
		"1": {"cik_str": 2, "ticker": "BBB", "title": "Beta Corp"}
	}`
	srv := newTestServer(t, ambiguous, "", "", 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ResolveCIK("corp")
	if err == nil {
		t.Fatal("expected ambiguous match error")
	}
	if !strings.Contains(err.Error(), "matched 2 companies") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGetCompany(t *testing.T) {
	submissions := mustReadFixture(t, "submissions_apple.json")
	srv := newTestServer(t, "", submissions, "", 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	company, err := c.GetCompany("0000320193")
	if err != nil {
		t.Fatalf("GetCompany: %v", err)
	}
	if company.Name != "Apple Inc." {
		t.Errorf("got name %q", company.Name)
	}
	if len(company.Tickers) != 1 || company.Tickers[0] != "AAPL" {
		t.Errorf("got tickers %v", company.Tickers)
	}
	if company.SICDescription != "Electronic Computers" {
		t.Errorf("got SIC description %q", company.SICDescription)
	}
	if len(company.FormerNames) != 1 || company.FormerNames[0].Name != "APPLE COMPUTER INC" {
		t.Errorf("got former names %v", company.FormerNames)
	}
	if company.BusinessAddress == nil || company.BusinessAddress.City != "CUPERTINO" {
		t.Errorf("got business address %+v", company.BusinessAddress)
	}
}

func TestGetFilingsFilteredByForm(t *testing.T) {
	submissions := mustReadFixture(t, "submissions_apple.json")
	srv := newTestServer(t, "", submissions, "", 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	filings, err := c.GetFilings("0000320193", "10-Q", 10)
	if err != nil {
		t.Fatalf("GetFilings: %v", err)
	}
	if len(filings) != 2 {
		t.Fatalf("got %d filings, want 2", len(filings))
	}
	for _, f := range filings {
		if f.Form != "10-Q" {
			t.Errorf("got form %q, want 10-Q", f.Form)
		}
	}
	if filings[0].AccessionNumber != "0000320193-24-000123" {
		t.Errorf("got accession number %q", filings[0].AccessionNumber)
	}
}

func TestGetInsiderRelationshipsParsesAtom(t *testing.T) {
	atom := mustReadFixture(t, "insider_filings_apple.atom")
	srv := newTestServer(t, "", "", atom, 0, 0)
	defer srv.Close()
	c := newTestClient(t, srv)

	rels, err := c.GetInsiderRelationships("0000320193", "Apple Inc.", 50)
	if err != nil {
		t.Fatalf("GetInsiderRelationships: %v", err)
	}
	// Third entry is malformed and should be skipped, not raise.
	if len(rels) != 2 {
		t.Fatalf("got %d relationships, want 2", len(rels))
	}
	names := map[string]bool{}
	for _, r := range rels {
		names[r.TargetName] = true
		if r.RelationshipType != "insider_filer" {
			t.Errorf("got relationship type %q", r.RelationshipType)
		}
	}
	if !names["COOK TIMOTHY D"] || !names["MAESTRI LUCA"] {
		t.Errorf("got names %v", names)
	}
}

func TestRateLimitedResponseReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tickers.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ResolveCIK("AAPL")
	if err == nil {
		t.Fatal("expected an error for a 429 ticker response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected error to mention 429, got: %v", err)
	}
}
