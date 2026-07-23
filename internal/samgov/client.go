// Package samgov provides a client for the US General Services
// Administration's SAM.gov Exclusions API -- the federal government's
// list of firms, individuals, and vessels excluded (debarred,
// suspended, or otherwise ineligible) from receiving federal
// contracts or assistance. Documented at
// https://open.gsa.gov/api/exclusions-api/. Unlike every keyless
// source in this project, SAM.gov requires a free API key (same
// no-keyless-option model as UK Companies House and the Charity
// Commission) -- see SAM_GOV_API_KEY in .env.example.
package samgov

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Exclusions API's live endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.sam.gov/entity-information/v4/exclusions"

// MaxPageSize is the API's own documented cap on results per page (1
// to 10) -- SearchByName clamps limit to this rather than sending an
// out-of-range value the API would reject.
const MaxPageSize = 10

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the SAM.gov Exclusions API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though this API publishes no
	// documented per-second rate limit (only a daily quota) -- errs
	// conservative out of politeness, same reasoning as internal/ofsi.
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
// SAM_GOV_API_KEY environment variable. Returns an error if neither is
// set -- like UK Companies House and the Charity Commission, this API
// has no keyless path.
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("SAM_GOV_API_KEY")
	}
	if apiKey == "" {
		return nil, newClientError(
			"the SAM.gov Exclusions API requires a free API key. Register at " +
				"https://sam.gov, go to your Account Details page, and request a " +
				"public API key (shown once -- copy it immediately), then set " +
				"SAM_GOV_API_KEY to it.",
		)
	}
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    500 * time.Millisecond,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		BaseURL:        DefaultBaseURL,
		APIKey:         apiKey,
		MaxRetries:     3,
		RetryBaseDelay: time.Second,
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

// Exclusion is one excluded firm, individual, vessel, or special
// entity designation.
type Exclusion struct {
	Name             string `json:"name"`
	Classification   string `json:"classification"` // e.g. "Firm", "Individual", "Vessel", "Special Entity Designation"
	ExclusionType    string `json:"exclusionType"`  // e.g. "Ineligible (Proceedings)", "Prohibition/Restriction"
	ExclusionProgram string `json:"exclusionProgram,omitempty"`
	ExcludingAgency  string `json:"excludingAgency,omitempty"`
	ActivationDate   string `json:"activationDate,omitempty"`
	TerminationDate  string `json:"terminationDate,omitempty"` // empty if still in effect / no end date on record
	Country          string `json:"country,omitempty"`
}

type searchResponse struct {
	TotalRecords   int `json:"totalRecords"`
	ExcludedEntity []struct {
		ExclusionDetails struct {
			ClassificationType  string `json:"classificationType"`
			ExclusionType       string `json:"exclusionType"`
			ExclusionProgram    string `json:"exclusionProgram"`
			ExcludingAgencyName string `json:"excludingAgencyName"`
		} `json:"exclusionDetails"`
		ExclusionIdentification struct {
			EntityName string `json:"entityName"`
			FirstName  string `json:"firstName"`
			MiddleName string `json:"middleName"`
			LastName   string `json:"lastName"`
		} `json:"exclusionIdentification"`
		ExclusionActions struct {
			ListOfActions []struct {
				ActivateDate    string `json:"activateDate"`
				TerminationDate string `json:"terminationDate"`
			} `json:"listOfActions"`
		} `json:"exclusionActions"`
		ExclusionPrimaryAddress struct {
			CountryCode string `json:"countryCode"`
		} `json:"exclusionPrimaryAddress"`
	} `json:"excludedEntity"`
}

// exclusionName resolves the display name for an excluded entity --
// firms/vessels/special designations carry entityName directly;
// individuals are only given as separate first/middle/last name
// fields, joined here into one string.
func exclusionName(entityName, first, middle, last string) string {
	if strings.TrimSpace(entityName) != "" {
		return entityName
	}
	parts := make([]string, 0, 3)
	for _, p := range []string{first, middle, last} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// SearchByName searches the Exclusions list by (partial) name. limit
// caps how many results come back, clamped to MaxPageSize (the API's
// own documented per-page cap) -- 0 uses MaxPageSize.
func (c *Client) SearchByName(name string, limit int) ([]Exclusion, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}
	params := url.Values{}
	params.Set("api_key", c.APIKey)
	params.Set("exclusionName", name)
	params.Set("size", strconv.Itoa(limit))

	status, body, err := c.get(c.BaseURL + "?" + params.Encode())
	if err != nil {
		return nil, err
	}
	if status == http.StatusForbidden {
		return nil, newClientError("SAM.gov API rejected the request (HTTP 403 -- check that SAM_GOV_API_KEY is a valid, active key): %s", strings.TrimSpace(string(body)))
	}
	if status < 200 || status >= 300 {
		return nil, newClientError("SAM.gov API returned HTTP %d searching for %q: %s", status, name, strings.TrimSpace(string(body)))
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing SAM.gov search results: %v", err)
	}

	exclusions := make([]Exclusion, 0, len(resp.ExcludedEntity))
	for _, e := range resp.ExcludedEntity {
		var activationDate, terminationDate string
		if len(e.ExclusionActions.ListOfActions) > 0 {
			activationDate = e.ExclusionActions.ListOfActions[0].ActivateDate
			terminationDate = e.ExclusionActions.ListOfActions[0].TerminationDate
		}
		exclusions = append(exclusions, Exclusion{
			Name:             exclusionName(e.ExclusionIdentification.EntityName, e.ExclusionIdentification.FirstName, e.ExclusionIdentification.MiddleName, e.ExclusionIdentification.LastName),
			Classification:   e.ExclusionDetails.ClassificationType,
			ExclusionType:    e.ExclusionDetails.ExclusionType,
			ExclusionProgram: e.ExclusionDetails.ExclusionProgram,
			ExcludingAgency:  e.ExclusionDetails.ExcludingAgencyName,
			ActivationDate:   activationDate,
			TerminationDate:  terminationDate,
			Country:          e.ExclusionPrimaryAddress.CountryCode,
		})
	}
	return exclusions, nil
}
