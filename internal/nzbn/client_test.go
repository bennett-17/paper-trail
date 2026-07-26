package nzbn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient("test-subscription-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.MinInterval = 0
	c.RetryBaseDelay = time.Millisecond
	c.BaseURL = srv.URL
	c.EntityRoleBaseURL = srv.URL
	return c
}

func TestNewClientRequiresAKey(t *testing.T) {
	os.Unsetenv("NZBN_API_KEY")
	if _, err := NewClient(""); err == nil {
		t.Fatal("expected error when no key is configured")
	}
}

func TestNewClientFallsBackToEnvVar(t *testing.T) {
	os.Setenv("NZBN_API_KEY", "env-key")
	t.Cleanup(func() { os.Unsetenv("NZBN_API_KEY") })

	c, err := NewClient("")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want env-key", c.APIKey)
	}
}

func TestSearchEntitiesUsesSubscriptionKeyHeader(t *testing.T) {
	var gotKey, gotSearchTerm string
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Ocp-Apim-Subscription-Key")
		gotSearchTerm = r.URL.Query().Get("search-term")
		json.NewEncoder(w).Encode(map[string]any{
			"totalItems": 1,
			"items": []map[string]any{
				{
					"nzbn":                    "9429041782718",
					"entityName":              "GRIZZLY LIMITED",
					"entityTypeCode":          "LTD",
					"entityTypeDescription":   "NZ Limited Company",
					"entityStatusCode":        "50",
					"entityStatusDescription": "Registered",
					"registrationDate":        "2010-01-01",
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.SearchEntities("Grizzly", 10)
	if err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}
	if gotKey != "test-subscription-key" {
		t.Errorf("subscription key header = %q, want test-subscription-key", gotKey)
	}
	if gotSearchTerm != "Grizzly" {
		t.Errorf("search-term param = %q, want Grizzly", gotSearchTerm)
	}
	if result.Total != 1 || len(result.Entities) != 1 {
		t.Fatalf("result = %+v, want 1 entity", result)
	}
	got := result.Entities[0]
	if got.NZBN != "9429041782718" || got.Name != "GRIZZLY LIMITED" || got.StatusDescription != "Registered" {
		t.Errorf("entity = %+v, unexpected fields", got)
	}
}

func TestGetEntityParsesAddressesAndRoles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/9429041782718", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"nzbn":                    "9429041782718",
			"entityName":              "GRIZZLY LIMITED",
			"entityStatusCode":        "50",
			"entityStatusDescription": "Registered",
			"addresses": map[string]any{
				"addressList": []map[string]any{
					{"address1": "1 Queen Street", "postCode": "1010", "countryCode": "NZ", "endDate": ""},
					{"address1": "OLD ADDRESS", "postCode": "9999", "countryCode": "NZ", "endDate": "2015-01-01"},
				},
			},
			"roles": []map[string]any{
				{
					"roleType":  "Director",
					"startDate": "2010-01-01",
					"endDate":   "",
					"rolePerson": map[string]any{
						"firstName": "Belinda",
						"lastName":  "Smith",
					},
				},
				{
					"roleType":  "Director",
					"startDate": "2005-01-01",
					"endDate":   "2012-01-01",
					"rolePerson": map[string]any{
						"firstName": "Former",
						"lastName":  "Director",
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	entity, err := c.GetEntity("9429041782718")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if entity.Name != "GRIZZLY LIMITED" {
		t.Errorf("Name = %q", entity.Name)
	}
	if len(entity.Addresses) != 1 || entity.Addresses[0].Address1 != "1 Queen Street" {
		t.Errorf("expected only the current address, got %+v", entity.Addresses)
	}
	if len(entity.Roles) != 2 {
		t.Fatalf("expected both roles to be parsed, got %+v", entity.Roles)
	}
	if entity.Roles[0].Name != "Belinda Smith" || entity.Roles[0].EndDate != "" {
		t.Errorf("current director role = %+v", entity.Roles[0])
	}
	if entity.Roles[1].Name != "Former Director" || entity.Roles[1].EndDate == "" {
		t.Errorf("former director role = %+v", entity.Roles[1])
	}
}

func TestGetEntityUsesOrgNameForCorporateOfficer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities/123", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"nzbn":       "123",
			"entityName": "TEST LIMITED",
			"roles": []map[string]any{
				{
					"roleType": "Director",
					"roleEntity": map[string]any{
						"nzbn":       "456",
						"entityName": "CORPORATE DIRECTOR LIMITED",
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	entity, err := c.GetEntity("123")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if len(entity.Roles) != 1 || entity.Roles[0].Name != "CORPORATE DIRECTOR LIMITED" {
		t.Errorf("expected corporate officer's org name, got %+v", entity.Roles)
	}
}

func TestSearchEntityRolesDefaultsRoleTypeToAll(t *testing.T) {
	var gotRoleType, gotRegisteredOnly string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		gotRoleType = r.URL.Query().Get("role-type")
		gotRegisteredOnly = r.URL.Query().Get("registered-only")
		json.NewEncoder(w).Encode(map[string]any{
			"totalResults": 1,
			"roles": []map[string]any{
				{
					"firstName":             "Martin",
					"lastName":              "Smith",
					"roleType":              "Director",
					"status":                "active",
					"appointmentDate":       "2010-01-01",
					"associatedCompanyName": "ACME LIMITED",
					"associatedCompanyNzbn": 9429000000000,
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	result, err := c.SearchEntityRoles("Smith Martin", "", 10)
	if err != nil {
		t.Fatalf("SearchEntityRoles: %v", err)
	}
	if gotRoleType != "ALL" {
		t.Errorf("role-type = %q, want ALL when unspecified", gotRoleType)
	}
	if gotRegisteredOnly != "true" {
		t.Errorf("registered-only = %q, want true", gotRegisteredOnly)
	}
	if len(result.Roles) != 1 {
		t.Fatalf("expected 1 role, got %d", len(result.Roles))
	}
	got := result.Roles[0]
	if got.Name != "Martin Smith" {
		t.Errorf("Name = %q, want Martin Smith", got.Name)
	}
	if got.AssociatedCompanyNZBN != "9429000000000" {
		t.Errorf("AssociatedCompanyNZBN = %q", got.AssociatedCompanyNZBN)
	}
}

func TestSearchEntityRolesUsesEntityRoleAPIKeyWhenSetSeparately(t *testing.T) {
	var gotKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Ocp-Apim-Subscription-Key")
		json.NewEncoder(w).Encode(map[string]any{"totalResults": 0, "roles": []map[string]any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	c.EntityRoleAPIKey = "different-role-search-key"
	if _, err := c.SearchEntityRoles("Smith Martin", "DIR", 10); err != nil {
		t.Fatalf("SearchEntityRoles: %v", err)
	}
	if gotKey != "different-role-search-key" {
		t.Errorf("subscription key header = %q, want the distinct EntityRoleAPIKey", gotKey)
	}
}

func TestGetReturns401ErrorMentioningSubscriptionKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/entities", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SearchEntities("test", 10)
	if err == nil {
		t.Fatal("expected an error on 401")
	}
}
