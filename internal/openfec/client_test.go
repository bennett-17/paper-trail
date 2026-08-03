package openfec

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient("test-key")
	c.MinInterval = 0
	c.RetryBaseDelay = 0
	c.BaseURL = srv.URL
	return c
}

// This fixture is modeled directly on the real, live-verified OpenFEC
// Schedule A response for a contributor_name search, trimmed to the
// fields this package actually parses.
const realScheduleAResponse = `{
  "results": [
    {
      "contributor_name": "SMITH, D P",
      "contributor_employer": "AFLAC",
      "contributor_occupation": "ASSOCIATE",
      "contributor_city": "JACKSON",
      "contributor_state": "MS",
      "contribution_receipt_amount": 250.0,
      "contribution_receipt_date": "2007-06-01T00:00:00",
      "pdf_url": "http://docquery.fec.gov/cgi-bin/fecimg/?27991076151",
      "committee": {
        "name": "AFLAC INCORPORATED FEDERAL POLITICAL ACTION COMMITTEE",
        "committee_id": "C00034157",
        "cycle": 2008
      }
    }
  ]
}`

func TestSearchContributionsParsesRealShapeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schedules/schedule_a/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("contributor_name"); got != "SMITH, D P" {
			t.Errorf("contributor_name param = %q, want SMITH, D P", got)
		}
		if got := r.URL.Query().Get("api_key"); got != "test-key" {
			t.Errorf("api_key param = %q, want test-key", got)
		}
		fmt.Fprint(w, realScheduleAResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	contributions, err := c.SearchContributions("SMITH, D P", 0)
	if err != nil {
		t.Fatalf("SearchContributions: %v", err)
	}
	if len(contributions) != 1 {
		t.Fatalf("got %d contributions, want 1", len(contributions))
	}
	got := contributions[0]
	if got.ContributorName != "SMITH, D P" || got.ContributorEmployer != "AFLAC" {
		t.Errorf("contribution = %+v", got)
	}
	if got.Amount != 250.0 {
		t.Errorf("Amount = %v, want 250.0", got.Amount)
	}
	if got.CommitteeName != "AFLAC INCORPORATED FEDERAL POLITICAL ACTION COMMITTEE" || got.CommitteeID != "C00034157" {
		t.Errorf("committee fields = %q/%q", got.CommitteeName, got.CommitteeID)
	}
}

func TestSearchContributionsRespectsLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schedules/schedule_a/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[
			{"contributor_name":"A"},
			{"contributor_name":"B"}
		]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	contributions, err := c.SearchContributions("X", 1)
	if err != nil {
		t.Fatalf("SearchContributions: %v", err)
	}
	if len(contributions) != 1 {
		t.Fatalf("got %d contributions, want 1 (limit)", len(contributions))
	}
}

func TestNewClientFallsBackToDemoKey(t *testing.T) {
	os.Unsetenv("OPENFEC_API_KEY")
	c := NewClient("")
	if c.APIKey != "DEMO_KEY" {
		t.Errorf("APIKey = %q, want DEMO_KEY when nothing else is configured", c.APIKey)
	}
}

func TestNewClientPrefersExplicitKeyOverEnv(t *testing.T) {
	os.Setenv("OPENFEC_API_KEY", "env-key")
	defer os.Unsetenv("OPENFEC_API_KEY")
	c := NewClient("explicit-key")
	if c.APIKey != "explicit-key" {
		t.Errorf("APIKey = %q, want explicit-key", c.APIKey)
	}
}

func TestSearchContributionsReturnsErrorOnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schedules/schedule_a/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.SearchContributions("Example", 0); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/schedules/schedule_a/", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, realScheduleAResponse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	contributions, err := c.SearchContributions("SMITH, D P", 0)
	if err != nil {
		t.Fatalf("SearchContributions: %v, want it to succeed after retrying past the 429s", err)
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3 (two 429s then a success)", attempts)
	}
	if len(contributions) != 1 {
		t.Errorf("got %d contributions, want 1", len(contributions))
	}
}
