package ireland

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

// This fixture is modeled directly on the real, live-verified CRO Open
// Data Portal response for a "Wells Fargo" search (two of the five
// real records, trimmed to the fields this package actually parses,
// including the real "********NO ADDRESS DETAILS*******" placeholder
// and the real trailing-whitespace "Normal " status value).
const realSearchResponse = `{
  "help": "https://opendata.cro.ie/api/3/action/help_show?name=datastore_search",
  "success": true,
  "result": {
    "resource_id": "3fef41bc-b8f4-4b10-8434-ce51c29b1bba",
    "total": 5,
    "records": [
      {
        "_id": 21697,
        "company_num": 25332,
        "company_name": "WELLS FARGO EXPRESS COMPANY LIMITED",
        "company_status": "Dissolved",
        "company_type": "Unknown",
        "company_reg_date": null,
        "comp_dissolved_date": "1977-07-01T00:00:00",
        "company_address_1": "********NO ADDRESS DETAILS*******",
        "company_address_2": "********NO ADDRESS DETAILS*******",
        "company_address_3": "********NO ADDRESS DETAILS******* ********NO ADDRESS DETAILS*******",
        "company_address_4": null,
        "eircode": null
      },
      {
        "_id": 89428,
        "company_num": 93069,
        "company_name": "WELLS FARGO LIMITED",
        "company_status": "Dissolved",
        "company_type": "Private limited by shares",
        "company_reg_date": "1983-02-01T00:00:00",
        "comp_dissolved_date": "1995-09-26T00:00:00",
        "company_address_1": "8 WINDSOR TERRACE",
        "company_address_2": "PORTOBELLO",
        "company_address_3": "DUBLIN 8.",
        "company_address_4": "DUBLIN 8",
        "eircode": null
      },
      {
        "_id": 500001,
        "company_num": 429222,
        "company_name": "WELLS FARGO BANK INTERNATIONAL UNLIMITED COMPANY",
        "company_status": "Normal ",
        "company_type": "PUC - Public Unlimited Company",
        "company_reg_date": "2015-06-01T00:00:00",
        "comp_dissolved_date": null,
        "company_address_1": "2 GRAND CANAL SQUARE",
        "company_address_2": "DUBLIN 2",
        "company_address_3": null,
        "company_address_4": null,
        "eircode": "D02 A342"
      }
    ]
  }
}`

func TestSearchByNameParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "Wells Fargo" {
			t.Errorf("q param = %q, want Wells Fargo", got)
		}
		if got := r.URL.Query().Get("resource_id"); got != ResourceID {
			t.Errorf("resource_id param = %q, want %q", got, ResourceID)
		}
		fmt.Fprint(w, realSearchResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	companies, err := c.SearchByName("Wells Fargo", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(companies) != 3 {
		t.Fatalf("got %d companies, want 3", len(companies))
	}

	// A null company_reg_date and an address made entirely of the
	// placeholder string must both come back empty, not literal text.
	if companies[0].Number != "25332" || companies[0].RegisteredOn != "" || companies[0].Address != "" {
		t.Errorf("companies[0] = %+v, want number 25332, empty RegisteredOn, empty Address", companies[0])
	}
	// A real, present address must be joined and comma-separated.
	if want := "8 WINDSOR TERRACE, PORTOBELLO, DUBLIN 8., DUBLIN 8"; companies[1].Address != want {
		t.Errorf("companies[1].Address = %q, want %q", companies[1].Address, want)
	}
	// Trailing whitespace on Status must be trimmed.
	if companies[2].Status != "Normal" {
		t.Errorf("companies[2].Status = %q, want trimmed %q", companies[2].Status, "Normal")
	}
	if companies[2].Eircode != "D02 A342" {
		t.Errorf("companies[2].Eircode = %q, want D02 A342", companies[2].Eircode)
	}
}

func TestGetByNumberUsesStructuredFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		filters := r.URL.Query().Get("filters")
		if filters != `{"company_num":"25332"}` {
			t.Errorf("filters param = %q, want the exact company_num filter", filters)
		}
		fmt.Fprint(w, `{"success": true, "result": {"records": [
			{"company_num": 25332, "company_name": "WELLS FARGO EXPRESS COMPANY LIMITED", "company_status": "Dissolved"}
		]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	company, err := c.GetByNumber("25332")
	if err != nil {
		t.Fatalf("GetByNumber: %v", err)
	}
	if company.Name != "WELLS FARGO EXPRESS COMPANY LIMITED" {
		t.Errorf("Name = %q, want WELLS FARGO EXPRESS COMPANY LIMITED", company.Name)
	}
}

func TestGetByNumberNoMatchReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success": true, "result": {"records": []}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.GetByNumber("00000000"); err == nil {
		t.Error("GetByNumber with no matching record: got nil error, want one")
	}
}

func TestSearchByNameSuccessFalseReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success": false, "error": {"message": "resource not found"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchByName("anything", 0); err == nil {
		t.Error("SearchByName with success:false: got nil error, want one")
	}
}

func TestGetRetriesOn429(t *testing.T) {
	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("/datastore_search", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"success": true, "result": {"records": []}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	companies, err := c.SearchByName("anything", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures then a success)", attempts)
	}
	if len(companies) != 0 {
		t.Errorf("got %d companies, want 0", len(companies))
	}
}
