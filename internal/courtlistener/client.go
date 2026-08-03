// Package courtlistener provides a client for CourtListener's free,
// keyless REST search API (run by the nonprofit Free Law Project),
// used here to search the RECAP Archive -- the largest free index of
// federal PACER court dockets in existence -- by party name.
//
// Confirmed live: no API key or account is required for read-only
// search (unlike every "free" source in this project that turned out
// to need at least a self-serve registration), and party_name is a
// real, precise filter parameter, not just a fuzzy full-text match
// against case names. The real constraint is the rate limit: even the
// documented free-tier ceiling (an account with a token) is only 5
// requests/minute, 50/hour, 125/day -- tighter than any other source
// in this project, including GDELT's previous 1-request-per-5-seconds
// (12/minute), which was the strictest before this. Confirmed live
// that unauthenticated bursts get throttled even faster (a 429 after
// 8 rapid requests with no delay between them at all) -- Client's
// MinInterval is set conservatively to stay under the documented
// 5/minute ceiling regardless of whether a request happens to be
// authenticated.
package courtlistener

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// DefaultBaseURL is CourtListener's v4 search API endpoint.
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://www.courtlistener.com/api/rest/v4/search/"

// DefaultSiteURL prefixes the API's relative docket_absolute_url
// values (e.g. "/docket/16908781/wells-fargo/") to build a real,
// clickable link for evidence text.
const DefaultSiteURL = "https://www.courtlistener.com"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to CourtListener's search API. No API key is needed or
// accepted.
type Client struct {
	HTTPClient *http.Client
	// MinInterval defaults to 13s -- padding CourtListener's own
	// documented 5-requests-per-minute free-tier ceiling (12s exactly)
	// slightly, the same margin internal/companieshouse pads its own
	// documented limit by.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string
	SiteURL     string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on a 429 (the
	// only failure mode CourtListener's own documentation describes)
	// and, confirmed live, a plain request timeout too -- a real
	// search against a real party name timed out on a first attempt
	// and succeeded immediately on a second, the same category of
	// live-observed flakiness internal/crtsh's broader retry set
	// already accounts for.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    13 * time.Second,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		BaseURL:        DefaultBaseURL,
		SiteURL:        DefaultSiteURL,
		MaxRetries:     3,
		RetryBaseDelay: 5 * time.Second,
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
	var status int
	var body []byte
	var err error
	for attempt := 0; ; attempt++ {
		status, body, err = c.rawGet(u)
		retryable := err != nil || status == http.StatusTooManyRequests
		if !retryable || attempt >= c.MaxRetries {
			break
		}
		time.Sleep(delay)
		delay *= 2
	}
	if err != nil {
		return nil, err
	}
	switch {
	case status >= 200 && status < 300:
		return body, nil
	case status == http.StatusTooManyRequests:
		return nil, newClientError("CourtListener returned HTTP 429 (rate limited) for %s after %d retries", u, c.MaxRetries)
	default:
		return nil, newClientError("CourtListener returned HTTP %d for %s", status, u)
	}
}

func (c *Client) rawGet(u string) (int, []byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

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

// Docket is a single RECAP-archived federal court case a searched
// party name appears in.
type Docket struct {
	CaseName       string
	Court          string
	CourtCitation  string
	DateFiled      string
	DateTerminated string // empty if the case is still open
	DocketNumber   string
	DocketID       int64
	// DocketURL is an absolute, clickable link (SiteURL + the API's
	// relative docket_absolute_url).
	DocketURL string
	// Parties lists every party name on this docket, not just the one
	// searched for -- confirmed live this includes plaintiffs,
	// defendants, and other parties (receivers, trustees, etc.)
	// together in one flat list; the API doesn't distinguish their
	// role in this field.
	Parties []string
	Cause   string
}

// SearchResult is a page of CourtListener RECAP search results.
type SearchResult struct {
	Total   int
	Dockets []Docket
}

type searchResponse struct {
	Count   int `json:"count"`
	Results []struct {
		CaseName            string   `json:"caseName"`
		Court               string   `json:"court"`
		CourtCitationString string   `json:"court_citation_string"`
		DateFiled           string   `json:"dateFiled"`
		DateTerminated      string   `json:"dateTerminated"`
		DocketNumber        string   `json:"docketNumber"`
		DocketID            int64    `json:"docket_id"`
		DocketAbsoluteURL   string   `json:"docket_absolute_url"`
		Party               []string `json:"party"`
		Cause               string   `json:"cause"`
	} `json:"results"`
}

// SearchParties searches RECAP-archived federal court dockets for a
// party name (plaintiff, defendant, or any other named party) --
// confirmed live to be a real, precise filter field, not just a fuzzy
// full-text match against case captions. limit caps how many results
// come back (0 uses the API's own default page size); results are
// truncated client-side, since the API's own pagination is
// cursor-based rather than a simple page-size parameter this project's
// other clients can just set directly.
func (c *Client) SearchParties(name string, limit int) (SearchResult, error) {
	params := url.Values{}
	params.Set("type", "r")
	params.Set("party_name", name)
	body, err := c.get(c.BaseURL + "?" + params.Encode())
	if err != nil {
		return SearchResult{}, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SearchResult{}, newClientError("parsing CourtListener search results for %q: %v", name, err)
	}

	dockets := make([]Docket, 0, len(resp.Results))
	for i, r := range resp.Results {
		if limit > 0 && i >= limit {
			break
		}
		docketURL := ""
		if r.DocketAbsoluteURL != "" {
			docketURL = c.SiteURL + r.DocketAbsoluteURL
		}
		dockets = append(dockets, Docket{
			CaseName:       r.CaseName,
			Court:          r.Court,
			CourtCitation:  r.CourtCitationString,
			DateFiled:      r.DateFiled,
			DateTerminated: r.DateTerminated,
			DocketNumber:   r.DocketNumber,
			DocketID:       r.DocketID,
			DocketURL:      docketURL,
			Parties:        r.Party,
			Cause:          r.Cause,
		})
	}
	return SearchResult{Total: resp.Count, Dockets: dockets}, nil
}
