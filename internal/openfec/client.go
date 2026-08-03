// Package openfec provides a client for the US Federal Election
// Commission's OpenFEC API (https://api.open.fec.gov), specifically
// Schedule A -- itemized individual contributions to federal
// candidates and committees. Confirmed live to work with no
// registration at all via FEC's public "DEMO_KEY" (shared, rate-
// limited), the same fallback FEC's own documentation recommends for
// quick testing; a free key registered at https://api.data.gov raises
// the rate limit substantially for real use -- see OPENFEC_API_KEY in
// .env.example.
//
// This is a person-level source, not an organization one: federal
// campaign contributions are legally attributed to the individual
// donor (by law, not to their employer), so this package is only
// useful for screening the distinct person names this project already
// finds (officers, trustees, directors), never the organization query
// terms themselves -- mirrors how internal/wikidata's PEP screen is
// scoped.
package openfec

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

// DefaultBaseURL is the OpenFEC API's live endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.open.fec.gov/v1"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the OpenFEC API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests conservatively -- the shared
	// DEMO_KEY pool is rate-limited more tightly than a registered key,
	// and this package has no way to tell which one it's holding.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string
	APIKey      string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as every other client package in this project.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. An empty apiKey falls back to the
// OPENFEC_API_KEY environment variable, then to FEC's public shared
// "DEMO_KEY" -- unlike internal/samgov, this API always has a working
// keyless path, so NewClient never errors.
func NewClient(apiKey string) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("OPENFEC_API_KEY")
	}
	if apiKey == "" {
		apiKey = "DEMO_KEY"
	}
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    time.Second,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		BaseURL:        DefaultBaseURL,
		APIKey:         apiKey,
		MaxRetries:     3,
		RetryBaseDelay: 2 * time.Second,
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
			return nil, newClientError("OpenFEC API returned HTTP %d for %s", status, u)
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
	req.Header.Set("Accept", "application/json")
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

// Contribution is one itemized Schedule A individual contribution.
type Contribution struct {
	ContributorName       string
	ContributorEmployer   string
	ContributorOccupation string
	ContributorCity       string
	ContributorState      string
	Amount                float64
	Date                  string // RFC3339, e.g. "2019-06-01T00:00:00"
	CommitteeName         string
	CommitteeID           string
	CycleEnd              int // the committee's election cycle this contribution falls under
	PDFURL                string
}

type contributionResult struct {
	ContributorName       string  `json:"contributor_name"`
	ContributorEmployer   string  `json:"contributor_employer"`
	ContributorOccupation string  `json:"contributor_occupation"`
	ContributorCity       string  `json:"contributor_city"`
	ContributorState      string  `json:"contributor_state"`
	ContributionAmount    float64 `json:"contribution_receipt_amount"`
	ContributionDate      string  `json:"contribution_receipt_date"`
	PDFURL                string  `json:"pdf_url"`
	Committee             struct {
		Name  string `json:"name"`
		ID    string `json:"committee_id"`
		Cycle int    `json:"cycle"`
	} `json:"committee"`
}

type scheduleAResponse struct {
	Results []contributionResult `json:"results"`
}

// SearchContributions searches Schedule A for individual contributions
// by (partial) contributor name, most recent first. limit caps how
// many results come back (0 uses OpenFEC's own default page size).
func (c *Client) SearchContributions(contributorName string, limit int) ([]Contribution, error) {
	params := url.Values{}
	params.Set("contributor_name", contributorName)
	params.Set("api_key", c.APIKey)
	params.Set("sort", "-contribution_receipt_date")
	if limit > 0 {
		params.Set("per_page", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "/schedules/schedule_a/?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp scheduleAResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing OpenFEC Schedule A results: %v", err)
	}
	contributions := make([]Contribution, 0, len(resp.Results))
	for _, r := range resp.Results {
		contributions = append(contributions, Contribution{
			ContributorName:       r.ContributorName,
			ContributorEmployer:   r.ContributorEmployer,
			ContributorOccupation: r.ContributorOccupation,
			ContributorCity:       r.ContributorCity,
			ContributorState:      r.ContributorState,
			Amount:                r.ContributionAmount,
			Date:                  r.ContributionDate,
			CommitteeName:         r.Committee.Name,
			CommitteeID:           r.Committee.ID,
			CycleEnd:              r.Committee.Cycle,
			PDFURL:                r.PDFURL,
		})
		if limit > 0 && len(contributions) >= limit {
			break
		}
	}
	return contributions, nil
}
