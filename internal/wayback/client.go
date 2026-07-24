// Package wayback provides a client for the Internet Archive's free,
// keyless CDX API, used here to find a website's earliest archived
// snapshot -- a different signal from the RDAP domain-registration
// date elsewhere in this project: registration says when a domain was
// claimed, this says when it first had real, crawlable content.
//
// Confirmed live, two genuine quirks of this specific endpoint:
//  1. A host with no archived snapshots at all doesn't return an empty
//     JSON result the way a "genuine zero match" normally does
//     elsewhere in this project -- the CDX API instead returns an HTML
//     "503 Service Unavailable" error page wrapped in an HTTP 200
//     status (reproduced consistently against a deliberately-
//     nonexistent domain). This package treats that specific shape as
//     "no snapshot found", not an error.
//  2. Separately, the service is also genuinely flaky under repeated
//     use -- a real HTTP 503 (not wrapped in 200) was reproduced live
//     for domains that had just succeeded seconds earlier, unrelated
//     to the "no snapshot" case above. Client retries a real 503 with
//     backoff, the same approach every other unreliable source in this
//     project takes.
package wayback

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Internet Archive's CDX API endpoint.
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://web.archive.org/cdx/search/cdx"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the Internet Archive's CDX API. No API key is
// needed or accepted.
type Client struct {
	HTTPClient *http.Client
	// MinInterval is a courteous default, not a documented hard limit.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on a real
	// HTTP 503 -- confirmed live that this service is flaky enough to
	// need it, the same approach every other unreliable client in this
	// project takes.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 20 * time.Second},
		MinInterval:    250 * time.Millisecond,
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

func (c *Client) get(u string) (status int, body []byte, err error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, body, err = c.rawGet(u)
		// Confirmed live that this service is flaky enough to
		// sometimes fail at the network level too (a request
		// timeout), not just with a real HTTP 503 -- both are
		// retried here, unlike every other client in this project
		// (which only retries a specific status code), since a
		// network-level error is the more common failure mode
		// observed for this specific free service.
		retryable := err != nil || status == http.StatusServiceUnavailable
		if !retryable || attempt >= c.MaxRetries {
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

// EarliestSnapshot returns the date of a host's earliest archived
// snapshot, and true, when the Wayback Machine has ever archived it at
// all. host should be a bare host (no scheme, no path), e.g.
// "example.com". Results are already returned earliest-first by the
// CDX API's own default ordering, so only the first row is used.
func (c *Client) EarliestSnapshot(host string) (t time.Time, found bool, err error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return time.Time{}, false, nil
	}

	params := url.Values{}
	params.Set("url", host)
	params.Set("output", "json")
	params.Set("limit", "1")
	params.Set("filter", "statuscode:200")

	status, body, err := c.get(c.BaseURL + "?" + params.Encode())
	if err != nil {
		return time.Time{}, false, err
	}
	if status < 200 || status >= 300 {
		return time.Time{}, false, newClientError("Wayback CDX API returned HTTP %d for %s", status, host)
	}

	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "[") {
		// The documented-live "no snapshots" shape (an HTML 503 page
		// wrapped in an HTTP 200) -- see the package doc comment.
		return time.Time{}, false, nil
	}

	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return time.Time{}, false, newClientError("parsing Wayback CDX response for %s: %v", host, err)
	}
	if len(rows) < 2 {
		// Header row only (or empty) -- no actual snapshot rows.
		return time.Time{}, false, nil
	}

	const timestampColumn = 1 // ["urlkey","timestamp","original","mimetype","statuscode","digest","length"]
	if timestampColumn >= len(rows[1]) {
		return time.Time{}, false, newClientError("unexpected Wayback CDX row shape for %s: %v", host, rows[1])
	}
	ts := rows[1][timestampColumn]
	if len(ts) < 8 {
		return time.Time{}, false, newClientError("unexpected Wayback timestamp %q for %s", ts, host)
	}
	parsed, err := time.Parse("20060102", ts[:8])
	if err != nil {
		return time.Time{}, false, newClientError("parsing Wayback timestamp %q for %s: %v", ts, host, err)
	}
	return parsed, true, nil
}
