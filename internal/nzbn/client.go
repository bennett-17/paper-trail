// Package nzbn provides a client for two New Zealand government APIs
// hosted under MBIE's api.business.govt.nz gateway: the NZBN API
// (New Zealand Business Number entity search and detail) and the
// Companies Entity Role Search API (director/shareholder name search
// across the NZ Companies Register). Both authenticate the same way --
// a subscription key in the Ocp-Apim-Subscription-Key header -- but
// live under different base paths, since they're separate,
// separately-subscribed API products in MBIE's Azure API Management
// portal.
//
// Unlike UK Companies House, there is no stable per-person officer ID
// here: the Companies Entity Role Search API only supports a name
// search, so a caller fanning out from a director to their other
// directorships has no ID-based way to confirm it's the same real
// person -- see risk_gather.go's use of SearchEntityRoles for how this
// project guards against that.
package nzbn

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the NZBN API host (entity search/detail).
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.business.govt.nz/gateway/nzbn/v5"

// DefaultEntityRoleBaseURL is the Companies Entity Role Search API
// host (director/shareholder name search). A separate API product
// from the NZBN API above, hence a separate base URL.
const DefaultEntityRoleBaseURL = "https://api.business.govt.nz/gateway/companies-office/companies-register/entity-roles/v3"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the NZBN API and the Companies Entity Role Search
// API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests client-side. Neither API's
	// documentation publishes a specific rate limit the way Companies
	// House's does, so this is a conservative default rather than one
	// confirmed against documented headers.
	MinInterval time.Duration
	UserAgent   string
	// APIKey authenticates the NZBN API (entity search/detail).
	APIKey string
	// EntityRoleAPIKey authenticates the Companies Entity Role Search
	// API. Azure API Management issues a subscription key per product,
	// so these two keys are not guaranteed to be the same value even
	// though the portal account may be the same -- NewClient defaults
	// this to APIKey, which works if your account's subscriptions
	// happen to share one key, and falls back to the
	// NZBN_ENTITY_ROLE_API_KEY environment variable if you need to
	// override it separately.
	EntityRoleAPIKey  string
	BaseURL           string
	EntityRoleBaseURL string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as internal/companieshouse and internal/sanctions.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. An empty apiKey falls back to the
// NZBN_API_KEY environment variable. Returns an error if neither is
// set -- like Companies House and the UK Charity Commission, neither
// of these two APIs has a keyless path.
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("NZBN_API_KEY")
	}
	if apiKey == "" {
		return nil, newClientError(
			"the NZBN and Companies Entity Role Search APIs require a subscription key. " +
				"Register a free account at https://portal.api.business.govt.nz, subscribe to " +
				"both the \"NZBN\" and \"Companies Entity Role Search\" API products (approval " +
				"is a manual review, typically granted within one working day once you've " +
				"signed their API Access Agreement), then set NZBN_API_KEY to the subscription key.",
		)
	}
	entityRoleAPIKey := os.Getenv("NZBN_ENTITY_ROLE_API_KEY")
	if entityRoleAPIKey == "" {
		entityRoleAPIKey = apiKey
	}
	return &Client{
		HTTPClient:        &http.Client{Timeout: 15 * time.Second},
		MinInterval:       350 * time.Millisecond,
		UserAgent:         "paper-trail (https://github.com/bennett-17/paper-trail)",
		APIKey:            apiKey,
		EntityRoleAPIKey:  entityRoleAPIKey,
		BaseURL:           DefaultBaseURL,
		EntityRoleBaseURL: DefaultEntityRoleBaseURL,
		MaxRetries:        3,
		RetryBaseDelay:    time.Second,
	}, nil
}

func (c *Client) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.lastRequestAt)
	if elapsed < c.MinInterval {
		time.Sleep(c.MinInterval - elapsed)
	}
	c.lastRequestAt = time.Now()
}

// get performs a GET request against the NZBN API (entity
// search/detail) with retry-with-backoff on 429, and turns any non-2xx
// status into an actionable error.
func (c *Client) get(u string) ([]byte, error) {
	return c.getWithKey(u, c.APIKey)
}

// getEntityRole is the same as get, but authenticates with
// EntityRoleAPIKey -- the Companies Entity Role Search API is a
// separate subscribed product from the NZBN API, so its key isn't
// guaranteed to be the same value (see EntityRoleAPIKey's doc comment).
func (c *Client) getEntityRole(u string) ([]byte, error) {
	return c.getWithKey(u, c.EntityRoleAPIKey)
}

func (c *Client) getWithKey(u, key string) ([]byte, error) {
	status, body, err := c.doGetWithRetry(u, key)
	if err != nil {
		return nil, err
	}
	switch {
	case status >= 200 && status < 300:
		return body, nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, newClientError(
			"NZBN API returned HTTP %d for %s -- check that NZBN_API_KEY (or NZBN_ENTITY_ROLE_API_KEY, "+
				"if you needed to set that separately) is a valid, approved subscription key for this "+
				"product (both the NZBN and Companies Entity Role Search products must be subscribed to "+
				"separately)", status, u,
		)
	case status == http.StatusNotFound:
		return nil, newClientError("NZBN API returned 404 Not Found for %s -- no such NZBN", u)
	default:
		return nil, newClientError("NZBN API returned HTTP %d for %s", status, u)
	}
}

func (c *Client) doGetWithRetry(u, key string) (statusCode int, body []byte, err error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, respBody, doErr := c.doGet(u, key)
		if doErr != nil || status != http.StatusTooManyRequests || attempt >= c.MaxRetries {
			return status, respBody, doErr
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func (c *Client) doGet(u, key string) (statusCode int, body []byte, err error) {
	c.throttle()

	req, reqErr := http.NewRequest(http.MethodGet, u, nil)
	if reqErr != nil {
		return 0, nil, newClientError("building request for %s: %v", u, reqErr)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Ocp-Apim-Subscription-Key", key)

	resp, doErr := c.HTTPClient.Do(req)
	if doErr != nil {
		return 0, nil, newClientError("request to %s failed: %v", u, doErr)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return 0, nil, newClientError("reading response from %s: %v", u, readErr)
	}
	return resp.StatusCode, respBody, nil
}

// Address is a New Zealand postal/registered address, as the NZBN API
// represents it -- up to four free-text lines plus a postcode and
// country code, no single structured "locality" field the way UK
// Companies House has.
type Address struct {
	Address1    string `json:"address1,omitempty"`
	Address2    string `json:"address2,omitempty"`
	Address3    string `json:"address3,omitempty"`
	Address4    string `json:"address4,omitempty"`
	PostCode    string `json:"postCode,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
}

// AsSingleLine renders the address as a comma-separated single line,
// skipping empty fields.
func (a Address) AsSingleLine() string {
	parts := []string{a.Address1, a.Address2, a.Address3, a.Address4, a.PostCode, a.CountryCode}
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, ", ")
}

// EntitySummary is a single NZBN entity search result.
type EntitySummary struct {
	NZBN                  string `json:"nzbn"`
	Name                  string `json:"entityName"`
	EntityTypeCode        string `json:"entityTypeCode,omitempty"`
	EntityTypeDescription string `json:"entityTypeDescription,omitempty"`
	StatusCode            string `json:"entityStatusCode,omitempty"`
	StatusDescription     string `json:"entityStatusDescription,omitempty"`
	RegistrationDate      string `json:"registrationDate,omitempty"`
}

// SearchResult is a page of NZBN entity search results.
type SearchResult struct {
	Total    int             `json:"total"`
	Entities []EntitySummary `json:"entities"`
}

type searchResponse struct {
	TotalItems int `json:"totalItems"`
	Items      []struct {
		NZBN                    string `json:"nzbn"`
		EntityName              string `json:"entityName"`
		EntityTypeCode          string `json:"entityTypeCode"`
		EntityTypeDescription   string `json:"entityTypeDescription"`
		EntityStatusCode        string `json:"entityStatusCode"`
		EntityStatusDescription string `json:"entityStatusDescription"`
		RegistrationDate        string `json:"registrationDate"`
	} `json:"items"`
}

// SearchEntities searches the NZBN register by free-text name (matches
// against current and past entity names, current and past trading
// names, and the NZBN or legacy company number itself). limit caps how
// many results come back via the API's own page-size parameter (0 uses
// its default).
func (c *Client) SearchEntities(name string, limit int) (SearchResult, error) {
	params := url.Values{}
	params.Set("search-term", name)
	if limit > 0 {
		params.Set("page-size", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "/entities?" + params.Encode())
	if err != nil {
		return SearchResult{}, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SearchResult{}, newClientError("parsing NZBN entity search results: %v", err)
	}

	summaries := make([]EntitySummary, 0, len(resp.Items))
	for _, item := range resp.Items {
		summaries = append(summaries, EntitySummary{
			NZBN:                  item.NZBN,
			Name:                  item.EntityName,
			EntityTypeCode:        item.EntityTypeCode,
			EntityTypeDescription: item.EntityTypeDescription,
			StatusCode:            item.EntityStatusCode,
			StatusDescription:     item.EntityStatusDescription,
			RegistrationDate:      item.RegistrationDate,
		})
	}
	return SearchResult{Total: resp.TotalItems, Entities: summaries}, nil
}

// Role is a person or organisation holding a role (director being the
// only one this project currently uses) at an entity, from the NZBN
// entity-detail response's roles[] array. Name is derived: a person's
// first/middle/last names joined, or the role-holding organisation's
// own name for a corporate officer -- the raw response carries these
// as two separate, mutually-exclusive nested objects (rolePerson vs
// roleEntity).
type Role struct {
	Name      string
	RoleType  string `json:"roleType,omitempty"`
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"` // empty if currently serving
}

// Entity is a single NZBN entity's detail record: its own identity,
// current addresses, and roles (currently: directors only -- see
// Role's doc comment).
type Entity struct {
	NZBN                  string
	Name                  string
	EntityTypeCode        string
	EntityTypeDescription string
	StatusCode            string
	StatusDescription     string
	RegistrationDate      string
	Addresses             []Address
	Roles                 []Role
}

type entityDetailResponse struct {
	NZBN                    string `json:"nzbn"`
	EntityName              string `json:"entityName"`
	EntityTypeCode          string `json:"entityTypeCode"`
	EntityTypeDescription   string `json:"entityTypeDescription"`
	EntityStatusCode        string `json:"entityStatusCode"`
	EntityStatusDescription string `json:"entityStatusDescription"`
	RegistrationDate        string `json:"registrationDate"`
	Addresses               struct {
		AddressList []struct {
			Address1    string `json:"address1"`
			Address2    string `json:"address2"`
			Address3    string `json:"address3"`
			Address4    string `json:"address4"`
			PostCode    string `json:"postCode"`
			CountryCode string `json:"countryCode"`
			EndDate     string `json:"endDate"`
		} `json:"addressList"`
	} `json:"addresses"`
	Roles []struct {
		RoleType   string `json:"roleType"`
		StartDate  string `json:"startDate"`
		EndDate    string `json:"endDate"`
		RoleEntity struct {
			NZBN       string `json:"nzbn"`
			EntityName string `json:"entityName"`
		} `json:"roleEntity"`
		RolePerson struct {
			FirstName   string `json:"firstName"`
			MiddleNames string `json:"middleNames"`
			LastName    string `json:"lastName"`
		} `json:"rolePerson"`
	} `json:"roles"`
}

func roleName(firstName, middleNames, lastName, orgName string) string {
	if lastName != "" {
		parts := []string{firstName, middleNames, lastName}
		nonEmpty := make([]string, 0, len(parts))
		for _, p := range parts {
			if p != "" {
				nonEmpty = append(nonEmpty, p)
			}
		}
		return strings.Join(nonEmpty, " ")
	}
	return orgName
}

// GetEntity fetches an NZBN entity's detail record by its exact 13-digit
// NZBN. Only currently-active addresses (no endDate) are returned.
func (c *Client) GetEntity(nzbn string) (Entity, error) {
	body, err := c.get(c.BaseURL + "/entities/" + url.PathEscape(nzbn))
	if err != nil {
		return Entity{}, err
	}

	var resp entityDetailResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Entity{}, newClientError("parsing NZBN entity detail: %v", err)
	}

	var addrs []Address
	for _, a := range resp.Addresses.AddressList {
		if a.EndDate != "" {
			continue
		}
		addrs = append(addrs, Address{
			Address1:    a.Address1,
			Address2:    a.Address2,
			Address3:    a.Address3,
			Address4:    a.Address4,
			PostCode:    a.PostCode,
			CountryCode: a.CountryCode,
		})
	}

	var roles []Role
	for _, r := range resp.Roles {
		name := roleName(r.RolePerson.FirstName, r.RolePerson.MiddleNames, r.RolePerson.LastName, r.RoleEntity.EntityName)
		if name == "" {
			continue
		}
		roles = append(roles, Role{
			Name:      name,
			RoleType:  r.RoleType,
			StartDate: r.StartDate,
			EndDate:   r.EndDate,
		})
	}

	return Entity{
		NZBN:                  resp.NZBN,
		Name:                  resp.EntityName,
		EntityTypeCode:        resp.EntityTypeCode,
		EntityTypeDescription: resp.EntityTypeDescription,
		StatusCode:            resp.EntityStatusCode,
		StatusDescription:     resp.EntityStatusDescription,
		RegistrationDate:      resp.RegistrationDate,
		Addresses:             addrs,
		Roles:                 roles,
	}, nil
}

// EntityRole is a single result from the Companies Entity Role Search
// API: one person or organisation's role (director or shareholder) at
// one company, found by name search across the whole NZ Companies
// Register -- not scoped to any one entity the way Entity.Roles above
// is. Name is derived the same way Role's is.
type EntityRole struct {
	Name                  string
	RoleType              string `json:"roleType,omitempty"`
	Status                string `json:"status,omitempty"`
	AppointmentDate       string `json:"appointmentDate,omitempty"`
	ResignationDate       string `json:"resignationDate,omitempty"` // empty if currently serving
	AssociatedCompanyName string `json:"associatedCompanyName,omitempty"`
	AssociatedCompanyNZBN string `json:"associatedCompanyNzbn,omitempty"`
}

// EntityRoleResult is a page of Companies Entity Role Search results.
type EntityRoleResult struct {
	Total int          `json:"total"`
	Roles []EntityRole `json:"roles"`
}

type entityRoleSearchResponse struct {
	TotalResults int `json:"totalResults"`
	Roles        []struct {
		FirstName             string      `json:"firstName"`
		MiddleName            string      `json:"middleName"`
		LastName              string      `json:"lastName"`
		Name                  string      `json:"name"`
		RoleType              string      `json:"roleType"`
		Status                string      `json:"status"`
		AppointmentDate       string      `json:"appointmentDate"`
		ResignationDate       string      `json:"resignationDate"`
		AssociatedCompanyName string      `json:"associatedCompanyName"`
		AssociatedCompanyNzbn json.Number `json:"associatedCompanyNzbn"`
	} `json:"roles"`
}

// SearchEntityRoles searches the NZ Companies Register by director or
// shareholder name. roleType must be one of "DIR" (director), "SHR"
// (shareholder), or "ALL"; an empty value is sent as "ALL" (the API's
// own default is "DIR", but this project wants both unless a caller
// deliberately narrows it). Only registered-only results (at least one
// currently-registered associated company) are requested, matching the
// API's registered-only parameter. limit caps how many results come
// back via the API's own page-size parameter (0 uses its default of 10).
//
// There is no stable per-person ID in this API's response -- only the
// name as matched -- so a caller treating two results as the same
// person should compare normalized names itself rather than trusting
// the search alone; see risk_gather.go's use of this for how.
func (c *Client) SearchEntityRoles(name, roleType string, limit int) (EntityRoleResult, error) {
	if roleType == "" {
		roleType = "ALL"
	}
	params := url.Values{}
	params.Set("name", name)
	params.Set("role-type", roleType)
	params.Set("registered-only", "true")
	if limit > 0 {
		params.Set("page-size", strconv.Itoa(limit))
	}
	body, err := c.getEntityRole(c.EntityRoleBaseURL + "/search?" + params.Encode())
	if err != nil {
		return EntityRoleResult{}, err
	}

	var resp entityRoleSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return EntityRoleResult{}, newClientError("parsing Companies Entity Role Search results: %v", err)
	}

	roles := make([]EntityRole, 0, len(resp.Roles))
	for _, r := range resp.Roles {
		name := roleName(r.FirstName, r.MiddleName, r.LastName, r.Name)
		if name == "" {
			continue
		}
		roles = append(roles, EntityRole{
			Name:                  name,
			RoleType:              r.RoleType,
			Status:                r.Status,
			AppointmentDate:       r.AppointmentDate,
			ResignationDate:       r.ResignationDate,
			AssociatedCompanyName: r.AssociatedCompanyName,
			AssociatedCompanyNZBN: r.AssociatedCompanyNzbn.String(),
		})
	}
	return EntityRoleResult{Total: resp.TotalResults, Roles: roles}, nil
}
