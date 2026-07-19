package edgar

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchFullTextParsesAndLimitsResults(t *testing.T) {
	var gotQuery, gotForms string
	mux := http.NewServeMux()
	mux.HandleFunc("/fulltext-search", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotForms = r.URL.Query().Get("forms")
		fmt.Fprint(w, mustReadFixture(t, "fulltext_search_scientology.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	hits, total, err := c.SearchFullText(`"Scientology"`, "4,10-Q", "", "", "", 2)
	if err != nil {
		t.Fatalf("SearchFullText: %v", err)
	}
	if gotQuery != `"Scientology"` {
		t.Errorf("q param = %q, want %q", gotQuery, `"Scientology"`)
	}
	if gotForms != "4,10-Q" {
		t.Errorf("forms param = %q, want %q", gotForms, "4,10-Q")
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

	hits, total, err := c.SearchFullText("no such organization anywhere", "", "", "", "", 10)
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
