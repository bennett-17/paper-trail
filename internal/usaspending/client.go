// Package usaspending provides a client for USAspending.gov
// (https://api.usaspending.gov), the US government's official, free,
// keyless system for tracking federal spending -- every contract,
// grant, loan, and direct payment the federal government has awarded,
// searchable by recipient name. Confirmed live to require no API key
// or registration.
//
// This is context, not a red flag on its own: an organization
// receiving federal funds is completely routine (most federal
// contractors and grantees are exactly what they claim to be) -- the
// value here is surfacing a connection this project otherwise has no
// way to see, e.g. a nonprofit or shell-looking company that turns out
// to be a significant federal contractor, or corroborating that an
// entity claiming government work actually has the awards to show for
// it.
package usaspending

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// DefaultBaseURL is the USAspending.gov API's live endpoint.
// Overridable on Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.usaspending.gov/api/v2"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the USAspending.gov API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though USAspending.gov
	// publishes no documented per-client rate limit -- errs
	// conservative out of politeness, same reasoning as internal/gleif.
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
		HTTPClient:     &http.Client{Timeout: 20 * time.Second},
		MinInterval:    300 * time.Millisecond,
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

func (c *Client) post(u string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, newClientError("encoding request body for %s: %v", u, err)
	}

	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, body, err := c.rawPost(u, encoded)
		if err != nil {
			return nil, err
		}
		if status == http.StatusTooManyRequests && attempt < c.MaxRetries {
			time.Sleep(delay)
			delay *= 2
			continue
		}
		if status < 200 || status >= 300 {
			return nil, newClientError("USAspending API returned HTTP %d for %s", status, u)
		}
		return body, nil
	}
}

func (c *Client) rawPost(u string, body []byte) (int, []byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, newClientError("request to %s failed: %v", u, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, newClientError("reading response from %s: %v", u, err)
	}
	return resp.StatusCode, respBody, nil
}

// federalAwardTypeGroups covers contracts (A-D) and grants (02, 03,
// 04, 05) -- the two award categories most likely to involve a named
// organizational recipient, as opposed to direct payments to
// individuals (e.g. Social Security, which dwarfs this numerically but
// is irrelevant to an organization name search). Confirmed live that
// USAspending rejects a request mixing codes from more than one of its
// own award-type groups ("'award_type_codes' must only contain types
// from one group") -- contracts and loans are different groups, for
// example -- so SearchAwards below issues one request per group and
// merges the results, rather than one combined request.
var federalAwardTypeGroups = [][]string{
	{"A", "B", "C", "D"},     // contracts
	{"02", "03", "04", "05"}, // grants
}

// Award is one federal contract, grant, or loan awarded to a
// recipient.
type Award struct {
	RecipientName  string
	Amount         float64
	AwardingAgency string
	AwardType      string
	StartDate      string
	EndDate        string
	Description    string
	GeneratedID    string // USAspending's own award identifier, usable to build a spending.gov URL
}

type spendingByAwardResult struct {
	RecipientName       string  `json:"Recipient Name"`
	AwardAmount         float64 `json:"Award Amount"`
	AwardingAgency      string  `json:"Awarding Agency"`
	AwardType           string  `json:"Award Type"`
	StartDate           string  `json:"Start Date"`
	EndDate             string  `json:"End Date"`
	Description         string  `json:"Description"`
	GeneratedInternalID string  `json:"generated_internal_id"`
}

type spendingByAwardResponse struct {
	Results []spendingByAwardResult `json:"results"`
}

// SearchAwards searches federal contracts and grants by (partial)
// recipient name, largest award amount first across both categories
// combined. limit caps how many results come back. USAspending's
// search only covers awards from fiscal year 2008 onward
// (2007-10-01) -- a documented limitation of this endpoint, not a bug
// in this client.
func (c *Client) SearchAwards(recipientName string, limit int) ([]Award, error) {
	if limit <= 0 {
		limit = 10
	}

	var awards []Award
	for _, group := range federalAwardTypeGroups {
		groupAwards, err := c.searchAwardsByTypeCodes(recipientName, group, limit)
		if err != nil {
			return nil, err
		}
		awards = append(awards, groupAwards...)
	}

	sort.Slice(awards, func(i, j int) bool { return awards[i].Amount > awards[j].Amount })
	if len(awards) > limit {
		awards = awards[:limit]
	}
	return awards, nil
}

func (c *Client) searchAwardsByTypeCodes(recipientName string, typeCodes []string, limit int) ([]Award, error) {
	payload := map[string]any{
		"filters": map[string]any{
			"recipient_search_text": []string{recipientName},
			"time_period": []map[string]string{
				{"start_date": "2007-10-01", "end_date": time.Now().Format("2006-01-02")},
			},
			"award_type_codes": typeCodes,
		},
		"fields": []string{
			"Recipient Name", "Award Amount", "Awarding Agency", "Award Type",
			"Start Date", "End Date", "Description",
		},
		"page":  1,
		"limit": limit,
		"sort":  "Award Amount",
		"order": "desc",
	}
	body, err := c.post(c.BaseURL+"/search/spending_by_award/", payload)
	if err != nil {
		return nil, err
	}

	var resp spendingByAwardResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing USAspending award search results: %v", err)
	}
	awards := make([]Award, 0, len(resp.Results))
	for _, r := range resp.Results {
		awards = append(awards, Award{
			RecipientName:  r.RecipientName,
			Amount:         r.AwardAmount,
			AwardingAgency: r.AwardingAgency,
			AwardType:      r.AwardType,
			StartDate:      r.StartDate,
			EndDate:        r.EndDate,
			Description:    r.Description,
			GeneratedID:    r.GeneratedInternalID,
		})
	}
	return awards, nil
}
