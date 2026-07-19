package edgar

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	c.FullTextSearchURL = srv.URL + "/fulltext-search"
	c.CacheDir = t.TempDir() // isolate from the real OS cache dir
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

// TestGetInsiderRelationshipsFetchesReportingOwners exercises the full
// path against the Form 4, Form 5, and Form 3 feeds: entries that only
// carry a filing-href (no reporting owner name, matching SEC's current
// live format), a directory listing at "<filing>/index.json" naming the
// primary XML document, and that document's own <reportingOwner> block.
//
//   - Form 4 feed: Cook and Maestri. The third entry's directory listing
//     404s and should be skipped rather than failing the whole call.
//   - Form 5 feed: Wagner, a director present only via her annual filing
//     (no recent Form 4).
//   - Form 3 feed: Newstead (not present in either feed above — this is
//     the case the full-roster fix exists for, e.g. a director who
//     hasn't traded recently) and a years-old Form 3 for Cook, which
//     should be deduped away in favor of the Form 4 entry already
//     recorded (Form 4 is queried first and wins the single evidence
//     slot on Relationship).
func TestGetInsiderRelationshipsFetchesReportingOwners(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()

	mux.HandleFunc("/browse-edgar", func(w http.ResponseWriter, r *http.Request) {
		fixture := "insider_filings_apple.atom"
		switch r.URL.Query().Get("type") {
		case "3":
			fixture = "insider_filings_apple_form3.atom"
		case "5":
			fixture = "insider_filings_apple_form5.atom"
		}
		tmpl := mustReadFixture(t, fixture)
		fmt.Fprint(w, strings.ReplaceAll(tmpl, "{{BASE}}", srv.URL))
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126025622/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"directory":{"item":[{"name":"form4.xml"}]}}`)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126025622/form4.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "form4_cook.xml"))
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126025620/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"directory":{"item":[{"name":"form4.xml"}]}}`)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126025620/form4.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "form4_maestri.xml"))
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126000099/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000178052526000003/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"directory":{"item":[{"name":"wk-form3_1.xml"}]}}`)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000178052526000003/wk-form3_1.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "form3_newstead.xml"))
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000121415618000001/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"directory":{"item":[{"name":"wk-form3_2.xml"}]}}`)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000121415618000001/wk-form3_2.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "form3_cook.xml"))
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000032019324000102/index.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"directory":{"item":[{"name":"wk-form5_1.xml"}]}}`)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000032019324000102/wk-form5_1.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "form5_wagner.xml"))
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	rels, err := c.GetInsiderRelationships("0000320193", "Apple Inc.", 50)
	if err != nil {
		t.Fatalf("GetInsiderRelationships: %v", err)
	}
	if len(rels) != 4 {
		t.Fatalf("got %d relationships, want 4: %+v", len(rels), rels)
	}
	byName := map[string]Relationship{}
	for _, r := range rels {
		byName[r.TargetName] = r
		if r.RelationshipType != "insider_filer" {
			t.Errorf("got relationship type %q", r.RelationshipType)
		}
		if r.SourceCIK != "0000320193" || r.SourceName != "Apple Inc." {
			t.Errorf("got source %s/%s, want issuer identity", r.SourceCIK, r.SourceName)
		}
	}
	cook, ok := byName["COOK TIMOTHY D"]
	if !ok {
		t.Fatalf("got names %v, missing COOK TIMOTHY D", byName)
	}
	if cook.TargetCIK != "0001214156" {
		t.Errorf("Cook TargetCIK = %q, want 0001214156", cook.TargetCIK)
	}
	if cook.EvidenceForm != "4" || cook.EvidenceAccessionNumber != "0001140361-26-025622" {
		t.Errorf("Cook evidence = form %q accession %q, want the Form 4 entry (Form 4 should win over the older Form 3)", cook.EvidenceForm, cook.EvidenceAccessionNumber)
	}
	maestri, ok := byName["MAESTRI LUCA"]
	if !ok {
		t.Fatalf("got names %v, missing MAESTRI LUCA", byName)
	}
	if maestri.TargetCIK != "0001513142" {
		t.Errorf("Maestri TargetCIK = %q, want 0001513142", maestri.TargetCIK)
	}
	wagner, ok := byName["WAGNER SUSAN"]
	if !ok {
		t.Fatalf("got names %v, missing WAGNER SUSAN (Form-5-only insider)", byName)
	}
	if wagner.TargetCIK != "0001059235" {
		t.Errorf("Wagner TargetCIK = %q, want 0001059235", wagner.TargetCIK)
	}
	if wagner.EvidenceForm != "5" {
		t.Errorf("Wagner EvidenceForm = %q, want 5", wagner.EvidenceForm)
	}
	newstead, ok := byName["Newstead Jennifer"]
	if !ok {
		t.Fatalf("got names %v, missing Newstead Jennifer (Form-3-only insider)", byName)
	}
	if newstead.TargetCIK != "0001780525" {
		t.Errorf("Newstead TargetCIK = %q, want 0001780525", newstead.TargetCIK)
	}
	if newstead.EvidenceForm != "3" {
		t.Errorf("Newstead EvidenceForm = %q, want 3", newstead.EvidenceForm)
	}
}

// TestFindRelatedCIKsMatchesNormalizedNames models BlackRock's real
// corporate history: the company search returns three candidate CIKs,
// but only two actually share a name with the queried company after
// normalization (one via a former name that only matches once
// punctuation is stripped, matching what's observed live: SEC's search
// index and stored names use inconsistent punctuation for the same
// legal identity). The third candidate is a same-industry decoy with a
// merely-similar name and must be filtered out, not flagged.
func TestFindRelatedCIKsMatchesNormalizedNames(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/browse-edgar", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "company_search_blackrock.atom"))
	})
	mux.HandleFunc("/submissions/CIK0001060021.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"cik":"0001060021","name":"BlackRock Holdco 2, Inc.","formerNames":[{"name":"BLACKROCK INC /NY","from":"1999-11-12T00:00:00.000Z","to":"2006-09-22T00:00:00.000Z"}]}`)
	})
	mux.HandleFunc("/submissions/CIK0001364742.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"cik":"0001364742","name":"BlackRock Finance, Inc.","formerNames":[{"name":"BlackRock Inc.","from":"2006-09-05T00:00:00.000Z","to":"2024-09-26T00:00:00.000Z"}]}`)
	})
	mux.HandleFunc("/submissions/CIK0009999999.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"cik":"0009999999","name":"Blackrock Realty Trust","formerNames":[]}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	company := Company{
		CIK:  "0002012383",
		Name: "BlackRock, Inc.",
		FormerNames: []FormerName{
			{Name: "BlackRock Funding, Inc. /DE"},
		},
	}
	related, err := c.FindRelatedCIKs(company)
	if err != nil {
		t.Fatalf("FindRelatedCIKs: %v", err)
	}
	if len(related) != 2 {
		t.Fatalf("got %d related CIKs, want 2: %+v", len(related), related)
	}
	byCIK := map[string]RelatedEntity{}
	for _, r := range related {
		byCIK[r.CIK] = r
	}
	if _, ok := byCIK["0001060021"]; !ok {
		t.Errorf("missing BlackRock Holdco 2 (0001060021): %+v", related)
	}
	if _, ok := byCIK["0001364742"]; !ok {
		t.Errorf("missing BlackRock Finance (0001364742): %+v", related)
	}
	if _, ok := byCIK["0009999999"]; ok {
		t.Errorf("decoy Blackrock Realty Trust (0009999999) should have been filtered out, got %+v", related)
	}
}

func TestNormalizeEntityNameUnifiesPunctuationAndStateSuffix(t *testing.T) {
	cases := map[string]string{
		"BlackRock, Inc.":     "BLACKROCK INC",
		"BLACKROCK INC /NY":   "BLACKROCK INC",
		"  Apple   Inc. ":     "APPLE INC",
		"New BlackRock, Inc.": "NEW BLACKROCK INC",
	}
	for input, want := range cases {
		if got := normalizeEntityName(input); got != want {
			t.Errorf("normalizeEntityName(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestFetchReportingOwnersCachesAcrossClients verifies the on-disk owner
// cache actually avoids re-fetching a filing's XML document -- both
// within one client (after saveOwnerCache persists it) and, more
// importantly, across a brand-new *Client pointed at the same CacheDir,
// which is what real separate `paper-trail graph` invocations look like.
func TestFetchReportingOwnersCachesAcrossClients(t *testing.T) {
	var indexHits, xmlHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126025622/index.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&indexHits, 1)
		fmt.Fprint(w, `{"directory":{"item":[{"name":"form4.xml"}]}}`)
	})
	mux.HandleFunc("/Archives/edgar/data/320193/000114036126025622/form4.xml", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&xmlHits, 1)
		fmt.Fprint(w, mustReadFixture(t, "form4_cook.xml"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cacheDir := t.TempDir()
	filingHref := srv.URL + "/Archives/edgar/data/320193/000114036126025622/0001140361-26-025622-index.htm"

	c1 := newTestClient(t, srv)
	c1.CacheDir = cacheDir
	owners1, err := c1.fetchReportingOwners(filingHref)
	if err != nil {
		t.Fatalf("fetchReportingOwners (client 1): %v", err)
	}
	if len(owners1) != 1 || owners1[0].Name != "COOK TIMOTHY D" {
		t.Fatalf("client 1 got %+v, want [COOK TIMOTHY D]", owners1)
	}
	if indexHits != 1 || xmlHits != 1 {
		t.Fatalf("after client 1: indexHits=%d xmlHits=%d, want 1/1", indexHits, xmlHits)
	}

	// Not yet persisted to disk -- a second call on the SAME client should
	// still be an in-memory cache hit, not a second round of requests.
	if _, err := c1.fetchReportingOwners(filingHref); err != nil {
		t.Fatalf("fetchReportingOwners (client 1, repeat): %v", err)
	}
	if indexHits != 1 || xmlHits != 1 {
		t.Fatalf("after client 1 repeat call: indexHits=%d xmlHits=%d, want still 1/1", indexHits, xmlHits)
	}

	// This is what a real run does at the end of GetInsiderRelationships.
	c1.saveOwnerCache()
	if _, err := os.Stat(filepath.Join(cacheDir, "insider-owners-cache.json")); err != nil {
		t.Fatalf("expected cache file on disk after saveOwnerCache: %v", err)
	}

	// A brand-new *Client pointed at the same CacheDir -- simulating a
	// separate `paper-trail graph` invocation -- should load the
	// persisted cache and make zero further requests.
	c2 := newTestClient(t, srv)
	c2.CacheDir = cacheDir
	owners2, err := c2.fetchReportingOwners(filingHref)
	if err != nil {
		t.Fatalf("fetchReportingOwners (client 2): %v", err)
	}
	if len(owners2) != 1 || owners2[0].Name != "COOK TIMOTHY D" {
		t.Fatalf("client 2 got %+v, want [COOK TIMOTHY D]", owners2)
	}
	if indexHits != 1 || xmlHits != 1 {
		t.Errorf("after client 2 (should be a disk cache hit): indexHits=%d xmlHits=%d, want still 1/1", indexHits, xmlHits)
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
