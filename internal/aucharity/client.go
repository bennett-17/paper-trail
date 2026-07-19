// Package aucharity provides a client for the Australian Charities and
// Not-for-profits Commission (ACNC) register, covering entities that
// never appear in SEC EDGAR or US IRS Form 990 data at all -- an
// organization operating out of Australia files with the ACNC, not
// either US regulator. Accessed via data.gov.au's CKAN datastore_search
// API against the published ACNC charity register dataset: genuinely
// free, keyless, and live-queryable (no bulk download required), unlike
// the UK Charity Commission's own API (which requires a registered key).
package aucharity

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// Default endpoints. Overridable on Client for testing against a local
// httptest server instead of the live API.
const (
	DefaultSearchURL = "https://data.gov.au/data/api/3/action/datastore_search"

	// DefaultResourceID identifies the "ACNC Register of Australian
	// charities CSV" resource within data.gov.au's CKAN catalog -- a
	// stable identifier data.gov.au assigns to this specific dataset
	// resource, not a per-query parameter.
	DefaultResourceID = "8fb32972-24e9-4c95-885e-7140be51be8a"
)

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to data.gov.au's CKAN datastore API.
type Client struct {
	HTTPClient  *http.Client
	MinInterval time.Duration
	UserAgent   string

	SearchURL  string
	ResourceID string

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		HTTPClient:  &http.Client{Timeout: 15 * time.Second},
		MinInterval: 150 * time.Millisecond,
		UserAgent:   "paper-trail (https://github.com/bennett-17/paper-trail)",
		SearchURL:   DefaultSearchURL,
		ResourceID:  DefaultResourceID,
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

func (c *Client) get(u string) ([]byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, newClientError("request to %s failed: %v", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newClientError("reading response from %s: %v", u, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newClientError("data.gov.au returned HTTP %d for %s", resp.StatusCode, u)
	}
	return body, nil
}

var nonDigitRE = regexp.MustCompile(`\D`)

// normalizeABN strips formatting (spaces) from an Australian Business
// Number so it can be used as an exact-match filter value, e.g.
// "13 172 090 453" -> "13172090453".
func normalizeABN(abn string) string {
	return nonDigitRE.ReplaceAllString(abn, "")
}

// Charity is a registered Australian charity as recorded on the ACNC
// Charity Register.
type Charity struct {
	ABN              string `json:"abn"`
	LegalName        string `json:"legalName"`
	OtherNames       string `json:"otherNames,omitempty"`
	Address          string `json:"address,omitempty"`
	City             string `json:"city,omitempty"`
	State            string `json:"state,omitempty"`
	Postcode         string `json:"postcode,omitempty"`
	Website          string `json:"website,omitempty"`
	RegistrationDate string `json:"registrationDate,omitempty"`
	Size             string `json:"size,omitempty"` // Small, Medium, or Large, per ACNC's own banding
}

// SearchResult is one page of a charity name search.
type SearchResult struct {
	Query     string    `json:"query"`
	Total     int       `json:"total"`
	Offset    int       `json:"offset"`
	Charities []Charity `json:"charities"`
}

type datastoreRecord struct {
	ABN                    string `json:"ABN"`
	CharityLegalName       string `json:"Charity_Legal_Name"`
	OtherOrganisationNames string `json:"Other_Organisation_Names"`
	AddressLine1           string `json:"Address_Line_1"`
	TownCity               string `json:"Town_City"`
	State                  string `json:"State"`
	Postcode               string `json:"Postcode"`
	CharityWebsite         string `json:"Charity_Website"`
	RegistrationDate       string `json:"Registration_Date"`
	CharitySize            string `json:"Charity_Size"`
}

func (r datastoreRecord) toCharity() Charity {
	return Charity{
		ABN:              r.ABN,
		LegalName:        r.CharityLegalName,
		OtherNames:       r.OtherOrganisationNames,
		Address:          r.AddressLine1,
		City:             r.TownCity,
		State:            r.State,
		Postcode:         r.Postcode,
		Website:          r.CharityWebsite,
		RegistrationDate: r.RegistrationDate,
		Size:             r.CharitySize,
	}
}

type datastoreResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Total   int               `json:"total"`
		Records []datastoreRecord `json:"records"`
	} `json:"result"`
}

// SearchCharities searches the ACNC register by name (a relevance-ranked
// full-text match over charity names, not a strict prefix/substring
// match). offset/limit page through results; pass 0 for either to use
// the API's defaults.
func (c *Client) SearchCharities(query string, offset, limit int) (SearchResult, error) {
	params := url.Values{}
	params.Set("resource_id", c.ResourceID)
	params.Set("q", query)
	if offset > 0 {
		params.Set("offset", fmt.Sprintf("%d", offset))
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}

	body, err := c.get(c.SearchURL + "?" + params.Encode())
	if err != nil {
		return SearchResult{}, err
	}

	var resp datastoreResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SearchResult{}, newClientError("parsing ACNC search results: %v", err)
	}

	charities := make([]Charity, 0, len(resp.Result.Records))
	for _, r := range resp.Result.Records {
		charities = append(charities, r.toCharity())
	}
	return SearchResult{
		Query:     query,
		Total:     resp.Result.Total,
		Offset:    offset,
		Charities: charities,
	}, nil
}

// GetCharityByABN fetches a single charity by its exact Australian
// Business Number (with or without spaces).
func (c *Client) GetCharityByABN(abn string) (Charity, error) {
	digits := normalizeABN(abn)
	if digits == "" {
		return Charity{}, newClientError("invalid ABN %q", abn)
	}

	filters, err := json.Marshal(map[string]string{"ABN": digits})
	if err != nil {
		return Charity{}, newClientError("building ABN filter: %v", err)
	}
	params := url.Values{}
	params.Set("resource_id", c.ResourceID)
	params.Set("filters", string(filters))

	body, err := c.get(c.SearchURL + "?" + params.Encode())
	if err != nil {
		return Charity{}, err
	}

	var resp datastoreResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Charity{}, newClientError("parsing ACNC organization lookup: %v", err)
	}
	if len(resp.Result.Records) == 0 {
		return Charity{}, newClientError("no charity found with ABN %s", digits)
	}
	return resp.Result.Records[0].toCharity(), nil
}
