// Package gdelt provides a client for the GDELT Project's DOC 2.0 API
// -- confirmed live to be free, keyless, and publicly accessible (no
// registration or approval process found), searching worldwide online
// news coverage by keyword/phrase. Unlike the SEC full-text mentions
// check elsewhere in this project (which only sees a name mentioned
// inside another company's own SEC filing), this sees a name mentioned
// anywhere in GDELT's indexed global news coverage -- a much broader,
// more current, but also noisier signal.
//
// Confirmed live: GDELT enforces a strict rate limit of one request
// every 5 seconds per client, returning a plain-text (not JSON) 429
// response if exceeded ("Please limit requests to one every 5
// seconds..."), far stricter than any other source this project uses
// -- Client's MinInterval reflects that.
package gdelt

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

// DefaultBaseURL is the GDELT DOC 2.0 API's live endpoint. Overridable
// on Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.gdeltproject.org/api/v2/doc/doc"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the GDELT DOC 2.0 API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval defaults to 5.5s -- GDELT's own documented limit is
	// one request every 5 seconds per client; this pads that slightly,
	// same reasoning as internal/companieshouse padding its own
	// documented limit.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as every other client package in this project --
	// RetryBaseDelay starts at 6s here (not the usual 1s) since a 429
	// from GDELT specifically means "wait at least 5 more seconds",
	// not a generic rate-limit backoff.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. No API key is needed or accepted.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 20 * time.Second},
		MinInterval:    5500 * time.Millisecond,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		BaseURL:        DefaultBaseURL,
		MaxRetries:     2,
		RetryBaseDelay: 6 * time.Second,
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

// Article is one news article GDELT's index matched.
type Article struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	SeenDate      string `json:"seenDate"` // GDELT's own compact format, e.g. "20260717T224500Z"
	Domain        string `json:"domain"`   // publishing site, e.g. "reuters.com"
	Language      string `json:"language"`
	SourceCountry string `json:"sourceCountry"`
}

type searchResponse struct {
	Articles []struct {
		URL           string `json:"url"`
		Title         string `json:"title"`
		SeenDate      string `json:"seendate"`
		Domain        string `json:"domain"`
		Language      string `json:"language"`
		SourceCountry string `json:"sourcecountry"`
	} `json:"articles"`
}

// SearchArticles searches GDELT's indexed global news coverage for an
// exact-phrase match of query (multi-word queries are always
// phrase-quoted, so a company name searches as that whole name, not a
// loose OR-of-words match). limit caps how many articles come back (0
// uses GDELT's own default). Confirmed live that a query with no
// matches returns a valid empty response (no "articles" key at all),
// not an error -- that comes back here as a nil/empty slice.
func (c *Client) SearchArticles(query string, limit int) ([]Article, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("query", `"`+query+`"`)
	params.Set("mode", "artlist")
	params.Set("format", "json")
	if limit > 0 {
		params.Set("maxrecords", strconv.Itoa(limit))
	}

	status, body, err := c.get(c.BaseURL + "?" + params.Encode())
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		// GDELT's own error responses (e.g. the rate-limit message)
		// are plain text, not JSON -- included verbatim here since
		// it's the actual actionable message ("please limit requests
		// to one every 5 seconds...").
		return nil, newClientError("GDELT API returned HTTP %d searching for %q: %s", status, query, strings.TrimSpace(string(body)))
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing GDELT search results: %v", err)
	}
	articles := make([]Article, 0, len(resp.Articles))
	for _, a := range resp.Articles {
		articles = append(articles, Article{
			URL:           a.URL,
			Title:         a.Title,
			SeenDate:      a.SeenDate,
			Domain:        a.Domain,
			Language:      a.Language,
			SourceCountry: a.SourceCountry,
		})
	}
	return articles, nil
}
