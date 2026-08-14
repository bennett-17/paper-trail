// Package ireland provides a client for Ireland's Companies
// Registration Office (CRO) Open Data Portal -- a free, keyless CKAN
// datastore_search API launched in late 2024 following the EU Open
// Data Directive (which required basic company registration data be
// published free and machine-readable), covering every company
// registered in Ireland, current and dissolved, updated daily.
// Confirmed live against a real query ("Wells Fargo" -- three
// dissolved Irish "Wells Fargo"-named entities, none related to the
// US bank, exactly the kind of name-collision noise this project's
// own HTML report relevance flag exists to catch).
//
// This dataset carries company-record fields only (name, number,
// status, type, formation/dissolution dates, address) -- no officer
// or director data at all, so an Ireland entity can only ever match
// on shared_address, never shared_person -- the same limitation this
// project's ACNC (Australia) integration already documents for the
// same reason (its own free data has no officer data either).
package ireland

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

// DefaultBaseURL is the CRO Open Data Portal's CKAN action API.
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://opendata.cro.ie/api/3/action"

// ResourceID is the CKAN resource ID for the CRO's "Company Records"
// dataset -- confirmed live via the portal's own package_show action
// (https://opendata.cro.ie/api/3/action/package_show?id=companies),
// which lists exactly one resource under this ID. A CKAN resource ID
// is a stable, permanent per-dataset identifier, not expected to
// change even as the underlying daily snapshot's contents do.
const ResourceID = "3fef41bc-b8f4-4b10-8434-ce51c29b1bba"

// addressPlaceholder is the CRO's own literal placeholder text for a
// missing address line, confirmed live (e.g. WELLS FARGO EXPRESS
// COMPANY LIMITED, company_num 25332, has this in three of its four
// address fields). Filtered out when building a Company's Address --
// left in, this exact string would itself become a false
// shared_address match between every unrelated company missing
// address data, defeating the whole point of that check.
const addressPlaceholder = "********NO ADDRESS DETAILS*******"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the CRO Open Data Portal's CKAN datastore_search
// action.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though the portal publishes
	// no documented rate limit -- errs conservative out of politeness,
	// same reasoning as internal/gazette/internal/gleif.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string
	ResourceID  string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as every other client package in this project.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. No API key is needed or accepted -- this
// is one of this project's fully keyless sources.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    500 * time.Millisecond,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		BaseURL:        DefaultBaseURL,
		ResourceID:     ResourceID,
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

func (c *Client) get(u string) ([]byte, error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, body, err := c.rawGet(u)
		if err != nil {
			return nil, err
		}
		if status == http.StatusTooManyRequests && attempt < c.MaxRetries {
			time.Sleep(delay)
			delay *= 2
			continue
		}
		if status < 200 || status >= 300 {
			return nil, newClientError("CRO Open Data Portal returned HTTP %d for %s", status, u)
		}
		return body, nil
	}
}

func (c *Client) rawGet(u string) (int, []byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
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

// Company is one Irish company record from the CRO's daily snapshot.
type Company struct {
	Number       string // company_num, e.g. "25332"
	Name         string
	Status       string // e.g. "Normal", "Dissolved"
	Type         string // e.g. "Private limited by shares", "U.C.I.T"
	RegisteredOn string // company_reg_date, RFC3339-ish, "" if null
	DissolvedOn  string // comp_dissolved_date, "" if null or still active
	Address      string // company_address_1..4 joined, placeholder lines dropped
	Eircode      string
}

// ckanRecord mirrors the CRO Company Records dataset's own column
// names exactly (confirmed live) -- company_num is a genuine JSON
// number in the response, not a quoted string, hence the int field
// below even though every other identifier in this project is a
// string (Company.Number converts it once, at the boundary).
type ckanRecord struct {
	CompanyNum      int    `json:"company_num"`
	CompanyName     string `json:"company_name"`
	CompanyStatus   string `json:"company_status"`
	CompanyType     string `json:"company_type"`
	CompanyRegDate  string `json:"company_reg_date"`
	CompDissolvedDt string `json:"comp_dissolved_date"`
	CompanyAddress1 string `json:"company_address_1"`
	CompanyAddress2 string `json:"company_address_2"`
	CompanyAddress3 string `json:"company_address_3"`
	CompanyAddress4 string `json:"company_address_4"`
	Eircode         string `json:"eircode"`
}

func (r ckanRecord) toCompany() Company {
	var addrParts []string
	for _, part := range []string{r.CompanyAddress1, r.CompanyAddress2, r.CompanyAddress3, r.CompanyAddress4} {
		part = strings.TrimSpace(part)
		if part == "" || strings.Contains(part, addressPlaceholder) {
			continue
		}
		addrParts = append(addrParts, part)
	}
	return Company{
		Number:       strconv.Itoa(r.CompanyNum),
		Name:         r.CompanyName,
		Status:       strings.TrimSpace(r.CompanyStatus), // confirmed live: some status values ("Normal ") carry trailing whitespace in the raw dataset
		Type:         r.CompanyType,
		RegisteredOn: r.CompanyRegDate,
		DissolvedOn:  r.CompDissolvedDt,
		Address:      strings.Join(addrParts, ", "),
		Eircode:      r.Eircode,
	}
}

type datastoreSearchResponse struct {
	Success bool `json:"success"`
	Result  struct {
		Total   int          `json:"total"`
		Records []ckanRecord `json:"records"`
	} `json:"result"`
}

// SearchByName full-text searches the CRO's Company Records dataset --
// confirmed live to be CKAN's own PostgreSQL full-text search (results
// come back ranked, not merely substring-filtered). limit caps how
// many results come back (0 uses 100, CKAN's own default page size).
func (c *Client) SearchByName(name string, limit int) ([]Company, error) {
	params := url.Values{}
	params.Set("resource_id", c.ResourceID)
	params.Set("q", name)
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "/datastore_search?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp datastoreSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing CRO Open Data Portal search results: %v", err)
	}
	if !resp.Success {
		return nil, newClientError("CRO Open Data Portal reported failure for a search of %q", name)
	}
	companies := make([]Company, 0, len(resp.Result.Records))
	for _, r := range resp.Result.Records {
		companies = append(companies, r.toCompany())
	}
	return companies, nil
}

// GetByNumber looks up a single company by its exact CRO company
// number, via CKAN's own structured filters parameter (confirmed live:
// filters={"company_num":"25332"} returns exactly that one record) --
// an exact-match lookup, not a text search, so it's the right tool
// when the number is already known (e.g. from a prior SearchByName
// result) rather than re-running a fuzzy name search.
func (c *Client) GetByNumber(number string) (Company, error) {
	filters, err := json.Marshal(map[string]string{"company_num": number})
	if err != nil {
		return Company{}, newClientError("building filter for company number %q: %v", number, err)
	}
	params := url.Values{}
	params.Set("resource_id", c.ResourceID)
	params.Set("filters", string(filters))
	body, err := c.get(c.BaseURL + "/datastore_search?" + params.Encode())
	if err != nil {
		return Company{}, err
	}

	var resp datastoreSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Company{}, newClientError("parsing CRO Open Data Portal lookup result: %v", err)
	}
	if !resp.Success || len(resp.Result.Records) == 0 {
		return Company{}, newClientError("no CRO company found with number %q", number)
	}
	return resp.Result.Records[0].toCompany(), nil
}
