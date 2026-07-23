// Package gleif provides a client for the Global Legal Entity
// Identifier Foundation's LEI (Legal Entity Identifier) database --
// confirmed live to be free, keyless, and publicly accessible (no
// registration or approval process found). Unlike every national
// company registry this project integrates, GLEIF isn't scoped to one
// jurisdiction: an LEI is required for financial-market transaction
// reporting worldwide, so its ~2.5 million records span virtually
// every country, not just the UK/US/AU this project otherwise covers.
// Confirmed live that GLEIF also records real direct-parent/
// ultimate-parent ownership relationships between entities (e.g.
// "Goldman Sachs International" in the UK correctly resolves its
// direct parent to "Goldman Sachs Group UK Limited") -- data most
// national registries this project uses don't expose at all.
package gleif

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the GLEIF API's live endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.gleif.org/api/v1"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the GLEIF LEI database API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though GLEIF publishes no
	// documented rate limit -- errs conservative out of politeness,
	// same reasoning as internal/ofsi and internal/unsc.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as every other client package in this project.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. No API key is needed or accepted.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    300 * time.Millisecond,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		BaseURL:        DefaultBaseURL,
		MaxRetries:     3,
		RetryBaseDelay: time.Second,
	}
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

// get performs a GET request with retry-with-backoff on 429. notFoundOK
// callers (DirectParent/UltimateParent) check for a 404 status
// themselves via the returned status code, since a missing parent
// relationship is a normal, expected outcome there, not an error.
func (c *Client) get(u string) (status int, body []byte, err error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, body, err = c.rawGet(u)
		if err != nil || status != http.StatusTooManyRequests || attempt >= c.MaxRetries {
			return status, body, err
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func (c *Client) rawGet(u string) (int, []byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, newClientError("request to %s failed: %v", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, newClientError("reading response from %s: %v", u, err)
	}
	return resp.StatusCode, body, nil
}

// Address is a simplified legal/headquarters address as GLEIF
// represents it.
type Address struct {
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	Country    string `json:"country,omitempty"` // ISO 3166-1 alpha-2, e.g. "GB", "US"
	PostalCode string `json:"postalCode,omitempty"`
}

// AsSingleLine renders the address as a comma-separated single line,
// skipping empty fields -- same shape as internal/companieshouse's
// Address.AsSingleLine.
func (a Address) AsSingleLine() string {
	parts := []string{a.City, a.Region, a.Country, a.PostalCode}
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, ", ")
}

// Country returns just the ISO country-code portion of a GLEIF
// jurisdiction, which can be a bare country ("GB") or a country plus
// subdivision ("US-DE") -- comparing this instead of the raw
// jurisdiction string is what lets a same-country, different-state
// relationship (e.g. a Delaware subsidiary of a New York parent) be
// correctly treated as domestic, not cross-border.
func Country(jurisdiction string) string {
	if i := strings.IndexByte(jurisdiction, '-'); i >= 0 {
		return jurisdiction[:i]
	}
	return jurisdiction
}

// Record is one entity's LEI record.
type Record struct {
	LEI           string   `json:"lei"`
	Name          string   `json:"name"`
	PreviousNames []string `json:"previousNames,omitempty"`
	LegalAddress  Address  `json:"legalAddress,omitempty"`
	Jurisdiction  string   `json:"jurisdiction,omitempty"` // ISO 3166-1/2, e.g. "GB", "US-DE"
	RegisteredAs  string   `json:"registeredAs,omitempty"` // this entity's own registration number in its home jurisdiction/registry
	Status        string   `json:"status,omitempty"`       // e.g. "ACTIVE", "INACTIVE"
	CreationDate  string   `json:"creationDate,omitempty"` // RFC3339, e.g. "1988-06-02T00:00:00Z"
}

type leiRecordAttributes struct {
	LEI    string `json:"lei"`
	Entity struct {
		LegalName struct {
			Name string `json:"name"`
		} `json:"legalName"`
		OtherNames []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"otherNames"`
		LegalAddress struct {
			City       string `json:"city"`
			Region     string `json:"region"`
			Country    string `json:"country"`
			PostalCode string `json:"postalCode"`
		} `json:"legalAddress"`
		RegisteredAs string `json:"registeredAs"`
		Jurisdiction string `json:"jurisdiction"`
		Status       string `json:"status"`
		CreationDate string `json:"creationDate"`
	} `json:"entity"`
}

type leiRecordData struct {
	ID         string              `json:"id"`
	Attributes leiRecordAttributes `json:"attributes"`
}

func (d leiRecordData) toRecord() Record {
	var previousNames []string
	for _, n := range d.Attributes.Entity.OtherNames {
		if n.Type == "PREVIOUS_LEGAL_NAME" && strings.TrimSpace(n.Name) != "" {
			previousNames = append(previousNames, n.Name)
		}
	}
	return Record{
		LEI:           d.Attributes.LEI,
		Name:          d.Attributes.Entity.LegalName.Name,
		PreviousNames: previousNames,
		LegalAddress: Address{
			City:       d.Attributes.Entity.LegalAddress.City,
			Region:     d.Attributes.Entity.LegalAddress.Region,
			Country:    d.Attributes.Entity.LegalAddress.Country,
			PostalCode: d.Attributes.Entity.LegalAddress.PostalCode,
		},
		Jurisdiction: d.Attributes.Entity.Jurisdiction,
		RegisteredAs: d.Attributes.Entity.RegisteredAs,
		Status:       d.Attributes.Entity.Status,
		CreationDate: d.Attributes.Entity.CreationDate,
	}
}

type leiRecordResponse struct {
	Data leiRecordData `json:"data"`
}

type leiRecordListResponse struct {
	Data []leiRecordData `json:"data"`
}

// SearchByName searches for legal entities by (partial) legal name.
// limit caps how many results come back (0 uses GLEIF's own default
// page size).
func (c *Client) SearchByName(name string, limit int) ([]Record, error) {
	params := url.Values{}
	params.Set("filter[entity.legalName]", name)
	if limit > 0 {
		params.Set("page[size]", strconv.Itoa(limit))
	}
	status, body, err := c.get(c.BaseURL + "/lei-records?" + params.Encode())
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, newClientError("GLEIF API returned HTTP %d searching for %q", status, name)
	}

	var resp leiRecordListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing GLEIF search results: %v", err)
	}
	records := make([]Record, 0, len(resp.Data))
	for _, d := range resp.Data {
		records = append(records, d.toRecord())
	}
	return records, nil
}

// DirectParent fetches lei's immediate parent entity, or (nil, nil) if
// none is reported -- confirmed live that this is a normal, common
// outcome (an entity with no reported parent, e.g. because it's the
// top of its own group, or its jurisdiction doesn't require
// disclosing one), not an error.
func (c *Client) DirectParent(lei string) (*Record, error) {
	return c.fetchRelationship(lei, "direct-parent")
}

// UltimateParent fetches lei's ultimate (top-of-group) parent entity,
// or (nil, nil) if none is reported. GLEIF resolves this server-side
// across the whole ownership chain -- unlike this project's UK PSC
// chain-following (which walks one hop at a time against Companies
// House), there's no need to iterate hops manually here.
func (c *Client) UltimateParent(lei string) (*Record, error) {
	return c.fetchRelationship(lei, "ultimate-parent")
}

func (c *Client) fetchRelationship(lei, relationship string) (*Record, error) {
	status, body, err := c.get(c.BaseURL + "/lei-records/" + url.PathEscape(lei) + "/" + relationship)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status < 200 || status >= 300 {
		return nil, newClientError("GLEIF API returned HTTP %d fetching %s for %s", status, relationship, lei)
	}

	var resp leiRecordResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing GLEIF %s response: %v", relationship, err)
	}
	record := resp.Data.toRecord()
	return &record, nil
}
