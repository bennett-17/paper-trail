package gleif

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestSearchByNameParsesResults is modeled directly on the real, live
// response for a name search of "Goldman Sachs International" --
// confirmed live, including the real previous-legal-name entry
// ("TRUSHELFCO (NO.1266) LIMITED", the shelf-company name it was
// originally incorporated under).
func TestSearchByNameParsesResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/lei-records", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "gleif_search.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	records, err := c.SearchByName("Goldman Sachs International", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].LEI != "W22LROWP2IHZNBB6K528" || records[0].Name != "GOLDMAN SACHS INTERNATIONAL" {
		t.Errorf("records[0] = %+v", records[0])
	}
	if records[0].Jurisdiction != "GB" || records[0].Status != "ACTIVE" {
		t.Errorf("records[0] Jurisdiction/Status = %q/%q", records[0].Jurisdiction, records[0].Status)
	}
	if records[0].RegisteredAs != "02263951" {
		t.Errorf("records[0].RegisteredAs = %q, want 02263951", records[0].RegisteredAs)
	}
	if len(records[0].PreviousNames) != 1 || records[0].PreviousNames[0] != "TRUSHELFCO (NO.1266) LIMITED" {
		t.Errorf("records[0].PreviousNames = %v", records[0].PreviousNames)
	}
	if records[0].CreationDate != "1988-06-02T00:00:00Z" {
		t.Errorf("records[0].CreationDate = %q", records[0].CreationDate)
	}
	if records[1].Jurisdiction != "IE" {
		t.Errorf("records[1].Jurisdiction = %q, want IE", records[1].Jurisdiction)
	}
}

// TestDirectParentParsesRecord is modeled on the real, live response
// confirmed for Goldman Sachs International's own direct parent
// (Goldman Sachs Group UK Limited).
func TestDirectParentParsesRecord(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/lei-records/W22LROWP2IHZNBB6K528/direct-parent", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "gleif_direct_parent.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	parent, err := c.DirectParent("W22LROWP2IHZNBB6K528")
	if err != nil {
		t.Fatalf("DirectParent: %v", err)
	}
	if parent == nil {
		t.Fatal("parent = nil, want a record")
	}
	if parent.Name != "GOLDMAN SACHS GROUP UK LIMITED" || parent.LEI != "549300RQT6K4WXZL3083" {
		t.Errorf("parent = %+v", parent)
	}
}

// TestDirectParentReturnsNilNotErrorOn404 guards a real, confirmed-live
// find: an entity with no reported parent (e.g. Apple Inc., the top of
// its own group) 404s on this endpoint -- that must come back as
// (nil, nil), a normal outcome, not an error.
func TestDirectParentReturnsNilNotErrorOn404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/lei-records/HWUPKR0MPOU8FGXBT394/direct-parent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"errors":[{"status":"404","title":"Resource not found","detail":"Related resource not found"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	parent, err := c.DirectParent("HWUPKR0MPOU8FGXBT394")
	if err != nil {
		t.Fatalf("DirectParent: %v, want nil error for a normal no-parent-reported case", err)
	}
	if parent != nil {
		t.Errorf("parent = %+v, want nil", parent)
	}
}

func TestUltimateParentParsesRecord(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/lei-records/W22LROWP2IHZNBB6K528/ultimate-parent", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "gleif_direct_parent.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	parent, err := c.UltimateParent("W22LROWP2IHZNBB6K528")
	if err != nil {
		t.Fatalf("UltimateParent: %v", err)
	}
	if parent == nil || parent.LEI != "549300RQT6K4WXZL3083" {
		t.Errorf("parent = %+v", parent)
	}
}

func TestCountryStripsSubdivision(t *testing.T) {
	cases := []struct{ jurisdiction, want string }{
		{"US-DE", "US"},
		{"US-NY", "US"},
		{"GB", "GB"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Country(c.jurisdiction); got != c.want {
			t.Errorf("Country(%q) = %q, want %q", c.jurisdiction, got, c.want)
		}
	}
}

func TestAddressAsSingleLineSkipsEmptyFields(t *testing.T) {
	a := Address{City: "London", Country: "GB"}
	if got := a.AsSingleLine(); got != "London, GB" {
		t.Errorf("AsSingleLine() = %q, want %q", got, "London, GB")
	}
}

func TestSearchByNameReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/lei-records", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchByName("Example", 0); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

// TestRetriesOn429ThenSucceeds mirrors every other client package's
// retry behavior in this project.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/lei-records", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "gleif_search.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	records, err := c.SearchByName("Goldman Sachs International", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if len(records) != 2 {
		t.Errorf("got %d records, want 2", len(records))
	}
}
