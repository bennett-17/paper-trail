// Package icij provides a client for the International Consortium of
// Investigative Journalists' Offshore Leaks Database reconciliation
// API -- confirmed live to be free, keyless, and publicly accessible
// (no registration or approval process found), following the
// OpenRefine Reconciliation API standard. It matches a name against
// ICIJ's combined leaked-documents dataset (the Panama Papers,
// Paradise Papers, Pandora Papers, Offshore Leaks, and Bahamas Leaks
// investigations), covering entities, intermediaries (registered
// agents/law firms), officers, and addresses extracted from those
// leaks. Data is published under the Open Database License; ICIJ
// itself is always cited alongside any result this package returns
// (see the icij_offshore_leaks_match indicator), and inclusion in the
// database is not itself an accusation of wrongdoing -- ICIJ makes
// this same point prominently on its own site.
package icij

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the reconciliation API's live endpoint.
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://offshoreleaks.icij.org/api/v1/reconcile"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the ICIJ Offshore Leaks Database reconciliation API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though this API publishes no
	// documented rate limit -- errs conservative out of politeness,
	// same reasoning as internal/ofsi.
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

// Match is one candidate result for a reconciliation query. Match
// (ICIJ's own high-confidence flag) is confirmed live to be a far
// more reliable signal than Score alone: an exact-name query for a
// real Panama Papers intermediary ("MOSSACK FONSECA & CO.") returns
// Match=true/Score=100, while an unrelated common name ("John Smith")
// still pulls back address/entity results that merely share a word,
// with Match=false and scores under 50 throughout.
type Match struct {
	ID          string
	Name        string
	Description string // e.g. "Intermediary node extracted from the Panama Papers data."
	Match       bool
	Score       float64
	Type        string // e.g. "Entity", "Intermediary", "Officer", "Address", "Other"
}

type reconcileRequest struct {
	Queries map[string]struct {
		Query string `json:"query"`
	} `json:"queries"`
}

type reconcileResult struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Match       bool    `json:"match"`
	Score       float64 `json:"score"`
	Types       []struct {
		Name string `json:"name"`
	} `json:"types"`
}

// doPostWithRetry wraps rawPost with exponential backoff retries when
// the response is HTTP 429 (rate limited), same approach as every
// other client package in this project.
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

	req, reqErr := http.NewRequest(http.MethodPost, c.BaseURL, bytes.NewReader(payload))
	if reqErr != nil {
		return 0, nil, newClientError("building request for %s: %v", c.BaseURL, reqErr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, doErr := c.HTTPClient.Do(req)
	if doErr != nil {
		return 0, nil, newClientError("request to %s failed: %v", c.BaseURL, doErr)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return 0, nil, newClientError("reading response from %s: %v", c.BaseURL, readErr)
	}
	return resp.StatusCode, respBody, nil
}

// Search queries the reconciliation API for a single name, returning
// every candidate ICIJ has for it -- check Match.Match to see which
// ones ICIJ itself considers a strong match, rather than relying on
// Score alone (see the Match doc comment). limit caps how many
// results come back (0 returns everything ICIJ sends).
func (c *Client) Search(name string, limit int) ([]Match, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}

	req := reconcileRequest{Queries: map[string]struct {
		Query string `json:"query"`
	}{"q0": {Query: name}}}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, newClientError("building reconciliation request: %v", err)
	}

	status, body, err := c.doPostWithRetry(payload)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, newClientError("ICIJ Offshore Leaks Database API returned HTTP %d for a search of %q", status, name)
	}

	var resp map[string]struct {
		Result []reconcileResult `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing reconciliation response: %v", err)
	}

	results := resp["q0"].Result
	matches := make([]Match, 0, len(results))
	for i, r := range results {
		if limit > 0 && i >= limit {
			break
		}
		typeName := ""
		if len(r.Types) > 0 {
			typeName = r.Types[0].Name
		}
		matches = append(matches, Match{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
			Match:       r.Match,
			Score:       r.Score,
			Type:        typeName,
		})
	}
	return matches, nil
}
