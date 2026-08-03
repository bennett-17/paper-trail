package littlesis

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient()
	c.MinInterval = 0
	c.RetryBaseDelay = 0
	c.BaseURL = srv.URL
	return c
}

// This fixture is modeled directly on the real, live-verified LittleSis
// search response for "Wells Fargo", trimmed to the fields this
// package actually parses.
const realSearchResponse = `{
  "meta": {"currentPage":1,"pageCount":3},
  "data": [
    {
      "type":"entities","id":"41",
      "attributes":{
        "id":41,"name":"Wells Fargo & Company","blurb":"Large San Francisco-based bank holding company",
        "website":"www.wellsfargo.com","primary_ext":"Org",
        "types":["Organization","Business","Private Company","Public Company"],
        "extensions":{"PublicCompany":{"ticker":"WFC","sec_cik":72971}}
      },
      "links":{"self":"https://littlesis.org/entities/41-Wells_Fargo_%26_Company"}
    }
  ]
}`

func TestSearchByNameParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "Wells Fargo" {
			t.Errorf("q param = %q, want Wells Fargo", got)
		}
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	entities, err := c.SearchByName("Wells Fargo", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(entities))
	}
	e := entities[0]
	if e.ID != 41 || e.Name != "Wells Fargo & Company" {
		t.Errorf("entity = %+v", e)
	}
	if !e.IsOrg() {
		t.Error("expected IsOrg() true for primary_ext Org")
	}
	if e.SECCIK != 72971 {
		t.Errorf("SECCIK = %d, want 72971", e.SECCIK)
	}
}

func TestSearchByNameRespectsLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
			{"attributes":{"id":1,"name":"A","primary_ext":"Org"},"links":{"self":"https://littlesis.org/org/1-A"}},
			{"attributes":{"id":2,"name":"B","primary_ext":"Org"},"links":{"self":"https://littlesis.org/org/2-B"}}
		]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	entities, err := c.SearchByName("X", 1)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(entities) != 1 {
		t.Fatalf("got %d entities, want 1 (limit)", len(entities))
	}
}

// This fixture is modeled directly on the real, live-verified LittleSis
// relationships response for Wells Fargo & Company (entity 41),
// trimmed to the fields this package actually parses.
const realRelationshipsResponse = `{
  "meta": {"currentPage":1,"pageCount":9},
  "data": [
    {
      "type":"relationships","id":2031194,
      "attributes":{
        "id":2031194,"entity1_id":459957,"entity2_id":41,"category_id":1,
        "description1":"CEO","description2":null,
        "is_current":false,
        "description":"John W. Morrison  had a position (CEO) at  Wells Fargo & Company ",
        "category_attributes":{"is_board":null,"is_executive":true,"is_employee":null}
      },
      "self":"https://littlesis.org/relationships/2031194",
      "entity":"https://littlesis.org/person/459957-John_W._Morrison",
      "related":"https://littlesis.org/org/41-Wells_Fargo_%26_Company"
    },
    {
      "type":"relationships","id":1969594,
      "attributes":{
        "id":1969594,"entity1_id":191706,"entity2_id":41,"category_id":1,
        "is_current":null,
        "description":"Joseph Saffire has/had a position (Executive Vice President) at Wells Fargo & Company",
        "category_attributes":{"is_board":null,"is_executive":null,"is_employee":true}
      },
      "self":"https://littlesis.org/relationships/1969594",
      "entity":"https://littlesis.org/person/191706-Joseph_Saffire",
      "related":"https://littlesis.org/org/41-Wells_Fargo_%26_Company"
    }
  ]
}`

func TestRelationshipsOrientsToCounterpartyAndDecodesName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/41/relationships", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, realRelationshipsResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	rels, err := c.Relationships(41, 0)
	if err != nil {
		t.Fatalf("Relationships: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("got %d relationships, want 2", len(rels))
	}
	if rels[0].OtherEntityID != 459957 || rels[0].OtherEntityName != "John W. Morrison" {
		t.Errorf("rels[0] = %+v", rels[0])
	}
	if !rels[0].IsExecutive {
		t.Error("expected rels[0].IsExecutive true")
	}
	if rels[1].IsBoard || rels[1].IsExecutive {
		t.Errorf("rels[1] = %+v, want a plain employee relationship (neither board nor executive)", rels[1])
	}
}

func TestEntityNameFromURLDecodesAmpersand(t *testing.T) {
	got := entityNameFromURL("https://littlesis.org/org/41-Wells_Fargo_%26_Company")
	if want := "Wells Fargo & Company"; got != want {
		t.Errorf("entityNameFromURL = %q, want %q", got, want)
	}
}

func TestSearchByNameReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchByName("Example", 0); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/search", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	entities, err := c.SearchByName("Wells Fargo", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if len(entities) != 1 {
		t.Errorf("got %d entities, want 1", len(entities))
	}
}
