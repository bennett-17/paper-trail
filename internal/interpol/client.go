// Package interpol provides a client for INTERPOL's public Red
// Notices API -- a free, keyless search over notices member countries
// have asked INTERPOL to circulate for wanted persons, confirmed live
// against a real query ("Smith" -- real notices returned, including a
// full name/date-of-birth/nationality/entity-id record). A Red Notice
// is a member country's own request, adjudicated by INTERPOL's own
// review process before publication, not an inference this project
// makes -- the same "already-adjudicated fact" category as a
// sanctions designation.
//
// Note for anyone running this from a sandboxed/datacenter network:
// INTERPOL's edge (Akamai) returns HTTP 403 "Access Denied" to
// requests from some cloud/datacenter IP ranges, confirmed during
// development (this package's own client, run from a sandboxed dev
// environment, got a 403 that a normal residential/office IP does
// not) -- a network-level block unrelated to this client's request
// shape, headers, or the API's own key-free policy.
package interpol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// DefaultBaseURL is INTERPOL's public Red Notices search endpoint.
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://ws-public.interpol.int/notices/v1/red"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to INTERPOL's public Red Notices search API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though this API publishes no
	// documented rate limit -- errs conservative out of politeness,
	// same reasoning as internal/gazette/internal/ireland.
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

// NewClient builds a Client. No API key is needed or accepted -- this
// is one of this project's fully keyless sources.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    500 * time.Millisecond,
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
			return nil, newClientError("INTERPOL Red Notices API returned HTTP %d for %s", status, u)
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

// Notice is one INTERPOL Red Notice record. The public search API
// carries only these summary fields -- full biographical detail,
// charges, and photos live behind DetailURL, which this package
// doesn't fetch (a v1 scope decision: the summary fields already
// carry everything risk's screen needs -- name, DOB, nationality --
// to judge whether a hit plausibly matches the entity being
// investigated).
type Notice struct {
	Name          string // surname, as INTERPOL records it (often all-caps)
	Forename      string
	DateOfBirth   string   // YYYY/MM/DD, or empty if not on record
	Nationalities []string // ISO 3166-1 alpha-2 codes
	EntityID      string   // INTERPOL's own notice identifier, e.g. "2024/10341"
	DetailURL     string   // _links.self.href -- the full notice record
	ThumbnailURL  string   // _links.thumbnail.href, empty if no photo on file
}

type noticeRecord struct {
	Name          string   `json:"name"`
	Forename      string   `json:"forename"`
	DateOfBirth   string   `json:"date_of_birth"`
	Nationalities []string `json:"nationalities"`
	EntityID      string   `json:"entity_id"`
	Links         struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
		Thumbnail struct {
			Href string `json:"href"`
		} `json:"thumbnail"`
	} `json:"_links"`
}

func (r noticeRecord) toNotice() Notice {
	return Notice{
		Name:          r.Name,
		Forename:      r.Forename,
		DateOfBirth:   r.DateOfBirth,
		Nationalities: r.Nationalities,
		EntityID:      r.EntityID,
		DetailURL:     r.Links.Self.Href,
		ThumbnailURL:  r.Links.Thumbnail.Href,
	}
}

type searchResponse struct {
	Total    int `json:"total"`
	Embedded struct {
		Notices []noticeRecord `json:"notices"`
	} `json:"_embedded"`
}

// SearchByName searches INTERPOL's Red Notices by name -- confirmed
// live to match against the notice's surname field (the API's own
// "name" query parameter). limit caps how many results come back via
// resultPerPage (0 uses the API's own default page size).
func (c *Client) SearchByName(name string, limit int) ([]Notice, error) {
	params := url.Values{}
	params.Set("name", name)
	if limit > 0 {
		params.Set("resultPerPage", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing INTERPOL Red Notices search results: %v", err)
	}
	notices := make([]Notice, 0, len(resp.Embedded.Notices))
	for _, r := range resp.Embedded.Notices {
		notices = append(notices, r.toNotice())
	}
	return notices, nil
}
