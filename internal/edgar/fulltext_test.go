package edgar

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchFullTextParsesAndLimitsResults(t *testing.T) {
	var gotQuery, gotForms string
	var sawFrom bool
	mux := http.NewServeMux()
	mux.HandleFunc("/fulltext-search", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotForms = r.URL.Query().Get("forms")
		_, sawFrom = r.URL.Query()["from"]
		fmt.Fprint(w, mustReadFixture(t, "fulltext_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	hits, total, err := c.SearchFullText(`"Scientology"`, "4,10-Q", "", "", "", 0, 2)
	if err != nil {
		t.Fatalf("SearchFullText: %v", err)
	}
	if gotQuery != `"Scientology"` {
		t.Errorf("q param = %q, want %q", gotQuery, `"Scientology"`)
	}
	if gotForms != "4,10-Q" {
		t.Errorf("forms param = %q, want %q", gotForms, "4,10-Q")
	}
	if sawFrom {
		t.Errorf(`offset=0 should omit the "from" param entirely, but it was present`)
	}
	if total != 193 {
		t.Errorf("total = %d, want 193 (limit should not affect the reported total)", total)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (limit=2 should truncate the 3 available)", len(hits))
	}

	first := hits[0]
	if first.AccessionNumber != "0001209191-05-040615" {
		t.Errorf("first.AccessionNumber = %q, want 0001209191-05-040615", first.AccessionNumber)
	}
	if first.DocumentFile != "doc4.xml" {
		t.Errorf("first.DocumentFile = %q, want doc4.xml", first.DocumentFile)
	}
	if first.Form != "4" {
		t.Errorf("first.Form = %q, want 4", first.Form)
	}
	if len(first.CIKs) != 2 || first.CIKs[0] != "0001035267" {
		t.Errorf("first.CIKs = %v, want [0001035267 0001055919]", first.CIKs)
	}
	wantURL := "https://www.sec.gov/Archives/edgar/data/1035267/000120919105040615/0001209191-05-040615-index.htm"
	if got := first.IndexURL(); got != wantURL {
		t.Errorf("first.IndexURL() = %q, want %q", got, wantURL)
	}
}

func TestSearchFullTextNoResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/fulltext-search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	hits, total, err := c.SearchFullText("no such organization anywhere", "", "", "", "", 0, 10)
	if err != nil {
		t.Fatalf("SearchFullText: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits, want 0", len(hits))
	}
}

// TestSearchFullTextOffsetPagesThroughResults verifies offset is passed
// through as SEC's "from" param, and that a nonzero offset can retrieve
// results beyond whatever the first page returned (SEC caps each page at
// ~100 hits, so paging is the only way to see everything past that).
func TestSearchFullTextOffsetPagesThroughResults(t *testing.T) {
	var gotFrom string
	mux := http.NewServeMux()
	mux.HandleFunc("/fulltext-search", func(w http.ResponseWriter, r *http.Request) {
		gotFrom = r.URL.Query().Get("from")
		if gotFrom == "3" {
			fmt.Fprint(w, mustReadFixture(t, "fulltext_search_scientology_page2.json"))
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "fulltext_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	page1, total1, err := c.SearchFullText(`"Scientology"`, "", "", "", "", 0, 100)
	if err != nil {
		t.Fatalf("SearchFullText (page 1): %v", err)
	}

	page2, total2, err := c.SearchFullText(`"Scientology"`, "", "", "", "", 3, 100)
	if err != nil {
		t.Fatalf("SearchFullText (page 2): %v", err)
	}
	if gotFrom != "3" {
		t.Errorf(`offset=3 should send from=3, got from=%q`, gotFrom)
	}
	if total1 != total2 {
		t.Errorf("total differs across pages of the same query: %d vs %d, want equal", total1, total2)
	}
	if len(page2) != 1 || page2[0].AccessionNumber != "0001409970-13-000123" {
		t.Errorf("page 2 = %+v, want the single page-2 fixture hit", page2)
	}
	if page1[0].AccessionNumber == page2[0].AccessionNumber {
		t.Errorf("page 1 and page 2 returned the same hit %q -- offset had no effect", page1[0].AccessionNumber)
	}
}
