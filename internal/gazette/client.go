// Package gazette provides a client for The Gazette
// (https://www.thegazette.co.uk), the UK's official public record --
// operated by The Stationery Office on behalf of HM Government, and
// the statutory publication of record for UK insolvency notices
// (liquidation, administration, company voluntary arrangements,
// bankruptcy) under the Insolvency Act 1986. Confirmed live that its
// JSON search API requires no API key or registration.
//
// Unlike this project's other UK sources (Companies House's own
// status field, or a Charity Commission record), a Gazette notice is
// the original statutory publication those downstream records
// summarize -- the primary source, not a derived status flag. Scoped
// to the insolvency notice category specifically (not the Gazette's
// much broader corpus of company/personal/property notices), since
// that's the category with the clearest, most consistent bearing on
// this project's risk model.
package gazette

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

// DefaultBaseURL is The Gazette's live search endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://www.thegazette.co.uk"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to The Gazette's search API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though The Gazette publishes
	// no documented rate limit -- errs conservative out of politeness,
	// same reasoning as internal/gleif and internal/crtsh.
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
			return nil, newClientError("The Gazette API returned HTTP %d for %s", status, u)
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
	// Deliberately no Accept header: confirmed live that The Gazette's
	// server returns a bare HTTP 500 when one is sent, regardless of
	// value -- the ".json" URL suffix alone is what selects the
	// response format, content negotiation via Accept isn't supported
	// (or trips some WAF/bot-detection rule; either way, sending it
	// breaks every request).
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

// Notice is one published insolvency notice.
type Notice struct {
	Title     string // the company/individual name as published
	Category  string // e.g. "Winding-up Order", "Notices to Creditors", "Final Meetings"
	Published string // RFC3339-ish, e.g. "2011-03-21T00:00:00"
	Status    string // e.g. "published"
	URL       string // human-readable notice page
}

type gazetteLink struct {
	Href string `json:"@href"`
	Rel  string `json:"@rel"`
}

type gazetteEntry struct {
	Status    string `json:"f:status"`
	Title     string `json:"title"`
	Published string `json:"published"`
	Category  struct {
		Term string `json:"@term"`
	} `json:"category"`
	Link []gazetteLink `json:"link"`
}

func (e gazetteEntry) toNotice() Notice {
	n := Notice{
		Title:     e.Title,
		Category:  e.Category.Term,
		Published: e.Published,
		Status:    e.Status,
	}
	for _, l := range e.Link {
		if l.Rel == "" { // the bare, no-@rel link is the human-readable notice page
			n.URL = l.Href
			break
		}
	}
	return n
}

type gazetteSearchResponse struct {
	Total string         `json:"f:total"`
	Entry []gazetteEntry `json:"entry"`
}

// SearchInsolvencyNotices searches The Gazette's insolvency notice
// category for the given text (typically a company or individual
// name). limit caps how many results come back (0 uses The Gazette's
// own default page size, confirmed live to be 10).
// Category is one of The Gazette's notice categories. The tool searched
// only insolvency for a long time, which turned out to be a small
// fraction of what The Gazette actually publishes about a name --
// confirmed live on one query: 190 insolvency notices against 2,593
// across all notices, so roughly 7% of the available record.
type Category string

const (
	// CategoryInsolvency is the original, narrow search: liquidation,
	// administration, CVAs and personal bankruptcy.
	CategoryInsolvency Category = "insolvency"
	// CategoryCompanies covers company notices generally -- strike-off
	// notices, name changes and other statutory publications. Confirmed
	// live to return the same count as all-notices for a company name,
	// so it is the useful superset for this project's subjects without
	// pulling in unrelated categories.
	CategoryCompanies Category = "companies"
	// CategoryAll is every category The Gazette publishes.
	CategoryAll Category = "all-notices"
	//
	// NOTHING IN THIS PROJECT CALLS THE BROADER CATEGORIES YET, and the
	// measurement that was supposed to justify wiring them did the
	// opposite. On an identical query the insolvency feed returned 190
	// results and all-notices returned 2,593 -- a 13x difference that
	// looked like the tool was seeing 7% of the available signal. It is
	// not. Inspecting the extra rows showed they are overwhelmingly
	// SCANNED PDF PAGES: empty category, a title like "The London
	// Gazette, Supplement 825537, Page 1527", and OCR'd content listing
	// dozens of unrelated company names that happen to share a page
	// with the searched one. Matching on those would attach a company
	// to every other company printed near it.
	//
	// The count was real and the inference from it was wrong -- more
	// rows are not more signal, which is the same mistake the
	// indicator-threshold calibration in this project exists to correct.
	// The category plumbing is kept because it is correct and cheap, but
	// a broader feed should be wired only once something distinguishes a
	// structured notice from a scanned page.
)

// SearchNotices searches one Gazette category for the given text.
// Deliberately kept separate from SearchInsolvencyNotices rather than
// replacing it: an insolvency notice and a strike-off notice mean
// different things, and collapsing every category into one "mentioned
// in the Gazette" result would lose exactly the distinction that makes
// the insolvency signal worth having.
func (c *Client) SearchNotices(category Category, text string, limit int) ([]Notice, error) {
	return c.searchNotices(category, text, limit)
}

func (c *Client) SearchInsolvencyNotices(text string, limit int) ([]Notice, error) {
	return c.searchNotices(CategoryInsolvency, text, limit)
}

func (c *Client) searchNotices(category Category, text string, limit int) ([]Notice, error) {
	params := url.Values{}
	params.Set("text", text)
	params.Set("results-page", "1")
	if limit > 0 {
		params.Set("results-page-size", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "/" + string(category) + "/notice/data.json?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp gazetteSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing The Gazette insolvency notice results: %v", err)
	}
	notices := make([]Notice, 0, len(resp.Entry))
	for _, e := range resp.Entry {
		notices = append(notices, e.toNotice())
		if limit > 0 && len(notices) >= limit {
			break
		}
	}
	return notices, nil
}
