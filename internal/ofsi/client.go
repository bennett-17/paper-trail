// Package ofsi provides a client for searching the UK's consolidated
// financial sanctions list (the "UK Sanctions List"), maintained by
// HM Treasury's Office of Financial Sanctions Implementation (OFSI).
//
// Unlike every other subscription-gated UK source in this project
// (ukcharity, companieshouse), this one needs no API key at all --
// confirmed live: this is the same public, same-origin JSON API that
// backs the official search tool at
// https://search-uk-sanctions-list.service.gov.uk, discovered by
// inspecting that page's own network requests, not a documented
// public API with a stable published contract. It could change
// without notice; the alternative (OFSI's own bulk ConList.csv/.xml
// download, confirmed live at ~16MB and no query support) would need
// a full download and local search on every call, so this per-query
// endpoint is a much better fit for this project's live-query model.
package ofsi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// DefaultSearchURL is the UK Sanctions List search API's live
// endpoint. Overridable on Client for testing against a local
// httptest server.
const DefaultSearchURL = "https://search-uk-sanctions-list.service.gov.uk/api/search/designations-minimal-open-search?minimal=true"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the UK Sanctions List search API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though this API publishes no
	// documented rate limit -- it's a public search tool's backing
	// endpoint, not a service designed for heavy programmatic use, so
	// this errs conservative out of politeness.
	MinInterval time.Duration
	UserAgent   string
	SearchURL   string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as internal/companieshouse, internal/sanctions,
	// internal/edgar, internal/nonprofit, internal/aucharity, and
	// internal/ukcharity.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. No API key is needed or accepted.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    500 * time.Millisecond,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		SearchURL:      DefaultSearchURL,
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

// searchRequest mirrors the exact JSON body the live search tool
// sends (confirmed live by inspecting its own network request) --
// every field appears to be required; omitting dateFrom/dateTo or the
// paging fields was not independently verified to still work, so this
// sends the same shape unconditionally.
type searchRequest struct {
	SearchValue   string         `json:"searchValue"`
	DateFrom      map[string]any `json:"dateFrom"`
	DateTo        map[string]any `json:"dateTo"`
	IsFuzzySearch bool           `json:"isFuzzySearch"`
	IsExactMatch  bool           `json:"isExactMatch"`
	CurrentPageNo int            `json:"currentPageNo"`
	PageSize      int            `json:"pageSize"`
	TotalPages    int            `json:"totalPages"`
	Page          int            `json:"page"`
	ItemFrom      int            `json:"itemFrom"`
	ItemTo        int            `json:"itemTo"`
}

type searchResponse struct {
	TotalCount int `json:"TotalCount"`
	Hits       []struct {
		Source struct {
			OfsiID            string `json:"OfsiID"`
			DateDesignated    string `json:"DateDesignated"`
			RegimeShortName   string `json:"RegimeShortName"`
			EntityType        string `json:"EntityType"`
			DesignationSource string `json:"DesignationSource"`
			PrimaryFullName   string `json:"PrimaryFullName"`
			SanctionsImposed  string `json:"SanctionsImposed"`
			DocumentKey       string `json:"DocumentKey"`
		} `json:"Source"`
	} `json:"Hits"`
}

// Hit is a single UK Sanctions List designation match.
type Hit struct {
	Name             string `json:"name"`
	EntityType       string `json:"entityType,omitempty"` // "Individual" or "Entity"
	Regime           string `json:"regime,omitempty"`     // the sanctions regime, e.g. "Russia", "Global Human Rights" -- not always literally a country
	SanctionsImposed string `json:"sanctionsImposed,omitempty"`
	DateDesignated   string `json:"dateDesignated,omitempty"`
	OfsiID           string `json:"ofsiId,omitempty"`
	DocumentKey      string `json:"documentKey,omitempty"`
}

// SearchResult is the full response to a designations search.
type SearchResult struct {
	Total int   `json:"total"`
	Hits  []Hit `json:"hits"`
}

// doPostWithRetry wraps rawPost with exponential backoff retries when
// the response is HTTP 429 (rate limited) -- same approach as
// internal/companieshouse, internal/sanctions, internal/edgar,
// internal/nonprofit, internal/aucharity, and internal/ukcharity.
func (c *Client) doPostWithRetry(payload []byte) (statusCode int, body []byte, err error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, respBody, doErr := c.rawPost(payload)
		if doErr != nil || status != http.StatusTooManyRequests || attempt >= c.MaxRetries {
			return status, respBody, doErr
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func (c *Client) rawPost(payload []byte) (statusCode int, body []byte, err error) {
	c.throttle()

	req, reqErr := http.NewRequest(http.MethodPost, c.SearchURL, bytes.NewReader(payload))
	if reqErr != nil {
		return 0, nil, newClientError("building request for %s: %v", c.SearchURL, reqErr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, doErr := c.HTTPClient.Do(req)
	if doErr != nil {
		return 0, nil, newClientError("request to %s failed: %v", c.SearchURL, doErr)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return 0, nil, newClientError("reading response from %s: %v", c.SearchURL, readErr)
	}
	return resp.StatusCode, respBody, nil
}

// SearchDesignations searches the UK Sanctions List by name. limit
// caps how many results come back (0 defaults to 50, the same default
// the live search tool itself uses).
func (c *Client) SearchDesignations(name string, limit int) (SearchResult, error) {
	if limit <= 0 {
		limit = 50
	}
	reqBody := searchRequest{
		SearchValue:   name,
		DateFrom:      map[string]any{},
		DateTo:        map[string]any{},
		IsFuzzySearch: false,
		IsExactMatch:  false,
		CurrentPageNo: 0,
		PageSize:      limit,
		TotalPages:    1,
		Page:          1,
		ItemFrom:      1,
		ItemTo:        limit,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return SearchResult{}, newClientError("building search request: %v", err)
	}

	status, body, err := c.doPostWithRetry(payload)
	if err != nil {
		return SearchResult{}, err
	}
	if status < 200 || status >= 300 {
		return SearchResult{}, newClientError("UK Sanctions List search returned HTTP %d for %s", status, c.SearchURL)
	}

	var raw searchResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return SearchResult{}, newClientError("parsing UK Sanctions List search results: %v", err)
	}

	hits := make([]Hit, 0, len(raw.Hits))
	for _, item := range raw.Hits {
		hits = append(hits, Hit{
			Name:             item.Source.PrimaryFullName,
			EntityType:       item.Source.EntityType,
			Regime:           item.Source.RegimeShortName,
			SanctionsImposed: item.Source.SanctionsImposed,
			DateDesignated:   item.Source.DateDesignated,
			OfsiID:           item.Source.OfsiID,
			DocumentKey:      item.Source.DocumentKey,
		})
	}
	return SearchResult{Total: raw.TotalCount, Hits: hits}, nil
}
