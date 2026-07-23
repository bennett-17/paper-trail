package samgov

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
	c, err := NewClient("test-api-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.RetryBaseDelay = 0
	c.BaseURL = srv.URL
	return c
}

func TestNewClientRequiresAKey(t *testing.T) {
	os.Unsetenv("SAM_GOV_API_KEY")
	if _, err := NewClient(""); err == nil {
		t.Fatal("expected error when no key is configured")
	}
}

func TestNewClientFallsBackToEnvVar(t *testing.T) {
	os.Setenv("SAM_GOV_API_KEY", "from-env")
	defer os.Unsetenv("SAM_GOV_API_KEY")
	c, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.APIKey != "from-env" {
		t.Errorf("APIKey = %q, want from-env", c.APIKey)
	}
}

func TestSearchByNameParsesFirmAndIndividual(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, mustReadFixture(t, "samgov_exclusions.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	exclusions, err := c.SearchByName("Example", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(exclusions) != 2 {
		t.Fatalf("got %d exclusions, want 2", len(exclusions))
	}

	firm := exclusions[0]
	if firm.Name != "EXAMPLE HOLDINGS LLC" || firm.Classification != "Firm" {
		t.Errorf("firm = %+v", firm)
	}
	if firm.ExcludingAgency != "ENVIRONMENTAL PROTECTION AGENCY" {
		t.Errorf("firm.ExcludingAgency = %q", firm.ExcludingAgency)
	}
	if firm.ActivationDate != "01/15/2020" || firm.TerminationDate != "" {
		t.Errorf("firm ActivationDate/TerminationDate = %q/%q", firm.ActivationDate, firm.TerminationDate)
	}

	individual := exclusions[1]
	if individual.Name != "Jane Q Example" {
		t.Errorf("individual.Name = %q, want %q (first/middle/last joined)", individual.Name, "Jane Q Example")
	}
	if individual.Classification != "Individual" || individual.TerminationDate != "06/01/2024" {
		t.Errorf("individual = %+v", individual)
	}
}

func TestSearchByNamePassesAPIKeyAndParams(t *testing.T) {
	var gotQuery map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		fmt.Fprint(w, `{"totalRecords":0,"excludedEntity":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchByName("Example", 3); err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if gotQuery["api_key"][0] != "test-api-key" {
		t.Errorf("api_key = %v, want test-api-key", gotQuery["api_key"])
	}
	if gotQuery["exclusionName"][0] != "Example" {
		t.Errorf("exclusionName = %v", gotQuery["exclusionName"])
	}
	if gotQuery["size"][0] != "3" {
		t.Errorf("size = %v, want 3", gotQuery["size"])
	}
}

// TestSearchByNameClampsLimitToMaxPageSize guards the documented API
// constraint (size must be 1-10) -- an out-of-range limit must be
// clamped, not sent through unchanged and rejected by the API.
func TestSearchByNameClampsLimitToMaxPageSize(t *testing.T) {
	var gotSize string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotSize = r.URL.Query().Get("size")
		fmt.Fprint(w, `{"totalRecords":0,"excludedEntity":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchByName("Example", 500); err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if gotSize != "10" {
		t.Errorf("size = %q, want it clamped to MaxPageSize (10)", gotSize)
	}
}

func TestSearchByNameReturns403ErrorWithHelpfulMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"API_KEY_INVALID","message":"An invalid API key was supplied."}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.SearchByName("Example", 0)
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
}

// TestRetriesOn429ThenSucceeds mirrors every other client package's
// retry behavior in this project.
func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, mustReadFixture(t, "samgov_exclusions.json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	exclusions, err := c.SearchByName("Example", 0)
	if err != nil {
		t.Fatalf("SearchByName: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if len(exclusions) != 2 {
		t.Errorf("got %d exclusions, want 2", len(exclusions))
	}
}
