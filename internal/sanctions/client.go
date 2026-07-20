// Package sanctions provides a client for the U.S. International Trade
// Administration's Consolidated Screening List (CSL) API, which
// aggregates OFAC's Specially Designated Nationals list together with
// the State Department, Commerce/BIS, and other federal restricted-
// party lists into one live JSON search API.
//
// Like the UK Charity Commission API elsewhere in this project, the CSL
// sits behind Azure API Management and requires a free registered
// subscription key, issued as a primary/secondary pair. Unlike the UK
// Charity Commission, the key is passed as a query parameter
// ("subscription-key"), not a header -- confirmed live: a request with
// the header form the UK API uses still came back 401, while the
// query-param form succeeded.
package sanctions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

// DefaultSearchURL is the CSL's live search endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultSearchURL = "https://data.trade.gov/consolidated_screening_list/v1/search"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the Consolidated Screening List API.
//
// Trade.gov issues every subscription a primary and secondary key so
// one can be rotated without downtime (same model as the UK Charity
// Commission API -- see internal/ukcharity). PrimaryKey is tried first
// on every request; SecondaryKey is only used as an automatic fallback
// if a request with PrimaryKey comes back 401.
type Client struct {
	HTTPClient   *http.Client
	MinInterval  time.Duration
	UserAgent    string
	PrimaryKey   string
	SecondaryKey string

	SearchURL string

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. Empty arguments fall back to the
// CSL_API_KEY_PRIMARY / CSL_API_KEY_SECONDARY environment variables.
// Returns an error if neither a primary nor a secondary key is
// available -- like the UK Charity Commission, this API has no keyless
// path.
func NewClient(primaryKey, secondaryKey string) (*Client, error) {
	if primaryKey == "" {
		primaryKey = os.Getenv("CSL_API_KEY_PRIMARY")
	}
	if secondaryKey == "" {
		secondaryKey = os.Getenv("CSL_API_KEY_SECONDARY")
	}
	if primaryKey == "" && secondaryKey == "" {
		return nil, newClientError(
			"the Consolidated Screening List API requires a subscription key. Register for a " +
				"free account and subscribe to \"Data Services Platform APIs\" at " +
				"https://developer.trade.gov, then set CSL_API_KEY_PRIMARY (and, optionally, " +
				"CSL_API_KEY_SECONDARY) to the keys shown on your Profile page.",
		)
	}
	return &Client{
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
		MinInterval:  150 * time.Millisecond,
		UserAgent:    "paper-trail (https://github.com/bennett-17/paper-trail)",
		PrimaryKey:   primaryKey,
		SecondaryKey: secondaryKey,
		SearchURL:    DefaultSearchURL,
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

// getWithFallback tries PrimaryKey first and SecondaryKey only if the
// primary comes back 401 (see the Client doc comment). base is the
// request URL without the subscription-key parameter; getWithFallback
// appends it per attempt.
func (c *Client) getWithFallback(base *url.URL) ([]byte, error) {
	keys := make([]string, 0, 2)
	if c.PrimaryKey != "" {
		keys = append(keys, c.PrimaryKey)
	}
	if c.SecondaryKey != "" {
		keys = append(keys, c.SecondaryKey)
	}

	var lastErr error
	for _, key := range keys {
		status, body, err := c.doGet(base, key)
		if err != nil {
			return nil, err
		}
		switch {
		case status >= 200 && status < 300:
			return body, nil
		case status == http.StatusUnauthorized:
			lastErr = newClientError(
				"Consolidated Screening List API returned 401 Unauthorized for %s -- check that "+
					"CSL_API_KEY_PRIMARY / CSL_API_KEY_SECONDARY are set to valid, active "+
					"subscription keys", base.Path,
			)
			continue // try the next key, if any
		default:
			return nil, newClientError("Consolidated Screening List API returned HTTP %d for %s", status, base.Path)
		}
	}
	return nil, lastErr
}

// doGet performs a single request with the given subscription key
// appended as a query parameter.
func (c *Client) doGet(base *url.URL, key string) (statusCode int, body []byte, err error) {
	c.throttle()

	u := *base
	q := u.Query()
	q.Set("subscription-key", key)
	u.RawQuery = q.Encode()

	req, reqErr := http.NewRequest(http.MethodGet, u.String(), nil)
	if reqErr != nil {
		return 0, nil, newClientError("building request for %s: %v", u.Path, reqErr)
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, doErr := c.HTTPClient.Do(req)
	if doErr != nil {
		return 0, nil, newClientError("request to %s failed: %v", u.Path, doErr)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return 0, nil, newClientError("reading response from %s: %v", u.Path, readErr)
	}
	return resp.StatusCode, respBody, nil
}

// Address is a single address associated with a screening-list entry.
type Address struct {
	Address    string `json:"address,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
}

// Hit is a single screening-list match. Fields are a subset of what the
// CSL actually returns -- vessel/aircraft-only and individual-only
// fields (tonnage, dates of birth, nationalities, etc.) are omitted
// since this tool's other commands resolve companies and their officers/
// trustees, not ships or people by birthdate.
type Hit struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	AltNames             []string  `json:"alt_names,omitempty"`
	Type                 string    `json:"type,omitempty"`   // "Individual", "Entity", "Vessel", "Aircraft"
	Source               string    `json:"source,omitempty"` // full list name, e.g. "Specially Designated Nationals (SDN) - Treasury Department"
	Country              string    `json:"country,omitempty"`
	EntityNumber         string    `json:"entity_number,omitempty"`
	Remarks              string    `json:"remarks,omitempty"` // often names the specific sanctions basis, e.g. an executive order
	Programs             []string  `json:"programs,omitempty"`
	Addresses            []Address `json:"addresses,omitempty"`
	SourceInformationURL string    `json:"source_information_url,omitempty"`
	SourceListURL        string    `json:"source_list_url,omitempty"`
}

// SourceCount is a per-list breakdown of how many of a search's results
// came from that particular screening list.
type SourceCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// SearchResult is the full response to a screening search.
type SearchResult struct {
	Total   int           `json:"total"`
	Sources []SourceCount `json:"sources,omitempty"`
	Hits    []Hit         `json:"results"`
}

// SearchEntities searches all US restricted-party lists (OFAC SDN, State
// Department, Commerce/BIS, and others aggregated by the CSL) for a
// given name. fuzzy enables the API's own fuzzy name matching, useful
// for catching transliteration/spelling variants at the cost of more
// false positives; offset/limit page through results (the API caps size
// at 50 and offset at 1000 per request -- confirmed via the API's own
// parameter names, not independently verified against those exact caps).
func (c *Client) SearchEntities(name string, fuzzy bool, offset, limit int) (SearchResult, error) {
	base, err := url.Parse(c.SearchURL)
	if err != nil {
		return SearchResult{}, newClientError("parsing search URL: %v", err)
	}
	q := base.Query()
	q.Set("name", name)
	if fuzzy {
		q.Set("fuzzy_name", "true")
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		q.Set("size", strconv.Itoa(limit))
	}
	base.RawQuery = q.Encode()

	body, err := c.getWithFallback(base)
	if err != nil {
		return SearchResult{}, err
	}

	var result SearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return SearchResult{}, newClientError("parsing screening search results: %v", err)
	}
	return result, nil
}
