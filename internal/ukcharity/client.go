// Package ukcharity provides a client for the Charity Commission for
// England and Wales's official Register of Charities API. Unlike SEC
// EDGAR, ProPublica, and the Australian ACNC integrations elsewhere in
// this project, this API requires a registered subscription key -- free
// to obtain (sign up at https://api-portal.charitycommission.gov.uk and
// subscribe to the "Register of Charities" product), but not a live
// keyless option the way the others are. There is no bulk-download-free
// live query API for UK charities, unlike Australia's ACNC data via
// data.gov.au.
package ukcharity

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Charity Commission's API host. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.charitycommission.gov.uk/register/api"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the Charity Commission's Register of Charities API.
//
// Azure API Management (which the Commission uses) issues every
// subscription two equally-valid keys, primary and secondary, so a key
// can be rotated without downtime: regenerate one while the other still
// works, then swap. PrimaryKey is tried first on every request;
// SecondaryKey is only used as an automatic fallback if a request with
// PrimaryKey comes back 401 (e.g. mid-rotation, or the primary was
// regenerated and the .env file hasn't been updated yet).
type Client struct {
	HTTPClient   *http.Client
	MinInterval  time.Duration
	UserAgent    string
	PrimaryKey   string
	SecondaryKey string

	SearchURL string // format string with a single %s for the URL-escaped charity name
	DetailURL string // format string with two %d: registered number, then suffix

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. Empty arguments fall back to the
// UK_CHARITY_API_KEY_PRIMARY / UK_CHARITY_API_KEY_SECONDARY environment
// variables. Returns an error if neither a primary nor a secondary key
// is available -- unlike this project's other integrations, the Charity
// Commission's API requires one; there's no keyless path.
func NewClient(primaryKey, secondaryKey string) (*Client, error) {
	if primaryKey == "" {
		primaryKey = os.Getenv("UK_CHARITY_API_KEY_PRIMARY")
	}
	if secondaryKey == "" {
		secondaryKey = os.Getenv("UK_CHARITY_API_KEY_SECONDARY")
	}
	if primaryKey == "" && secondaryKey == "" {
		return nil, newClientError(
			"the Charity Commission API requires a subscription key. Register for a " +
				"free account and subscribe to the \"Register of Charities\" product at " +
				"https://api-portal.charitycommission.gov.uk, then set " +
				"UK_CHARITY_API_KEY_PRIMARY (and, optionally, UK_CHARITY_API_KEY_SECONDARY) " +
				"to the keys shown on your subscription.",
		)
	}
	return &Client{
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
		MinInterval:  150 * time.Millisecond,
		UserAgent:    "paper-trail (https://github.com/bennett-17/paper-trail)",
		PrimaryKey:   primaryKey,
		SecondaryKey: secondaryKey,
		SearchURL:    DefaultBaseURL + "/searchCharityName/%s",
		DetailURL:    DefaultBaseURL + "/allcharitydetailsV2/%d/%d",
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

func (c *Client) get(u string) ([]byte, error) {
	return c.getWithFallback(u, false)
}

// getTolerant404 is like get, but treats HTTP 404 as a valid empty
// response rather than an error. Confirmed live: the search endpoint
// returns 404 with an empty body for a query that matches nothing in
// the live England & Wales register -- the same not-quite-RESTful
// convention ProPublica's API uses for empty US nonprofit searches (see
// internal/nonprofit). A strict "any non-2xx is an error" check would
// misreport that as a request failure instead of a genuinely empty
// result.
func (c *Client) getTolerant404(u string) ([]byte, error) {
	return c.getWithFallback(u, true)
}

// getWithFallback tries PrimaryKey first and SecondaryKey only if the
// primary comes back 401 (see the Client doc comment). tolerate404
// controls whether a 404 response is treated as success (with whatever
// body came back, e.g. "") or as an error.
func (c *Client) getWithFallback(u string, tolerate404 bool) ([]byte, error) {
	keys := make([]string, 0, 2)
	if c.PrimaryKey != "" {
		keys = append(keys, c.PrimaryKey)
	}
	if c.SecondaryKey != "" {
		keys = append(keys, c.SecondaryKey)
	}

	var lastErr error
	for _, key := range keys {
		status, body, err := c.doGet(u, key)
		if err != nil {
			return nil, err
		}
		switch {
		case status >= 200 && status < 300:
			return body, nil
		case tolerate404 && status == http.StatusNotFound:
			return body, nil
		case status == http.StatusUnauthorized:
			lastErr = newClientError(
				"Charity Commission API returned 401 Unauthorized for %s -- check that "+
					"UK_CHARITY_API_KEY_PRIMARY / UK_CHARITY_API_KEY_SECONDARY are set to "+
					"valid, active subscription keys", u,
			)
			continue // try the next key, if any
		default:
			return nil, newClientError("Charity Commission API returned HTTP %d for %s", status, u)
		}
	}
	return nil, lastErr
}

// doGet performs a single request with the given subscription key,
// returning the raw status code and body so the caller decides how to
// interpret them (e.g. retrying with a different key on 401, or
// treating 404 as an empty result rather than a failure).
func (c *Client) doGet(u, key string) (statusCode int, body []byte, err error) {
	c.throttle()

	req, reqErr := http.NewRequest(http.MethodGet, u, nil)
	if reqErr != nil {
		return 0, nil, newClientError("building request for %s: %v", u, reqErr)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	// Header name confirmed live: an unauthenticated request to this API
	// returns 401 with WWW-Authenticate naming "Ocp-Apim-Subscription-Key"
	// as the expected header (standard Azure API Management convention).
	req.Header.Set("Ocp-Apim-Subscription-Key", key)

	resp, doErr := c.HTTPClient.Do(req)
	if doErr != nil {
		return 0, nil, newClientError("request to %s failed: %v", u, doErr)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return 0, nil, newClientError("reading response from %s: %v", u, readErr)
	}
	return resp.StatusCode, respBody, nil
}

// Charity is a single search-result match: identity and registration
// status only, not the full profile (see CharityDetail).
type Charity struct {
	OrganisationNumber int    `json:"organisationNumber"` // stable per-entity ID, unlike RegisteredNumber which can be shared across linked charities
	RegisteredNumber   int    `json:"registeredNumber"`
	Suffix             int    `json:"suffix"` // 0 = main charity; >0 = a subsidiary/linked charity under the same registered number
	Name               string `json:"name"`
	Status             string `json:"status,omitempty"` // "R" (registered) or "RM" (removed)
	RegistrationDate   string `json:"registrationDate,omitempty"`
	RemovalDate        string `json:"removalDate,omitempty"`
}

type searchRecord struct {
	OrganisationNumber int    `json:"organisation_number"`
	RegCharityNumber   int    `json:"reg_charity_number"`
	GroupSubsidSuffix  int    `json:"group_subsid_suffix"`
	CharityName        string `json:"charity_name"`
	RegStatus          string `json:"reg_status"`
	DateOfRegistration string `json:"date_of_registration"`
	DateOfRemoval      string `json:"date_of_removal"`
}

// SearchCharities searches the Register of Charities by name (GetSearchCharityByName).
func (c *Client) SearchCharities(name string) ([]Charity, error) {
	u := fmt.Sprintf(c.SearchURL, url.PathEscape(name))
	body, err := c.getTolerant404(u)
	if err != nil {
		return nil, err
	}

	// A tolerated 404 (zero matches) comes back with an empty body,
	// which isn't valid JSON to unmarshal -- that's a normal empty
	// result, not a parse failure.
	if len(strings.TrimSpace(string(body))) == 0 {
		return []Charity{}, nil
	}

	var records []searchRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, newClientError("parsing charity search results: %v", err)
	}

	charities := make([]Charity, 0, len(records))
	for _, r := range records {
		charities = append(charities, Charity{
			OrganisationNumber: r.OrganisationNumber,
			RegisteredNumber:   r.RegCharityNumber,
			Suffix:             r.GroupSubsidSuffix,
			Name:               r.CharityName,
			Status:             r.RegStatus,
			RegistrationDate:   r.DateOfRegistration,
			RemovalDate:        r.DateOfRemoval,
		})
	}
	return charities, nil
}

// CharityDetail is a charity's full registration profile (GetAllCharityDetailsV2).
type CharityDetail struct {
	OrganisationNumber   int      `json:"organisationNumber"`
	RegisteredNumber     int      `json:"registeredNumber"`
	Suffix               int      `json:"suffix"`
	Name                 string   `json:"name"`
	CharityType          string   `json:"charityType,omitempty"`
	Status               string   `json:"status,omitempty"`
	RegistrationDate     string   `json:"registrationDate,omitempty"`
	RemovalDate          string   `json:"removalDate,omitempty"`
	LatestIncome         *int64   `json:"latestIncome,omitempty"`
	LatestExpenditure    *int64   `json:"latestExpenditure,omitempty"`
	Address              string   `json:"address,omitempty"` // address lines joined into one string
	Postcode             string   `json:"postcode,omitempty"`
	Phone                string   `json:"phone,omitempty"`
	Email                string   `json:"email,omitempty"`
	Website              string   `json:"website,omitempty"`
	CompaniesHouseNumber string   `json:"companiesHouseNumber,omitempty"` // set if also registered as a company
	Trustees             []string `json:"trustees,omitempty"`
}

type detailResponse struct {
	OrganisationNumber int    `json:"organisation_number"`
	RegCharityNumber   int    `json:"reg_charity_number"`
	GroupSubsidSuffix  int    `json:"group_subsid_suffix"`
	CharityName        string `json:"charity_name"`
	CharityType        string `json:"charity_type"`
	RegStatus          string `json:"reg_status"`
	DateOfRegistration string `json:"date_of_registration"`
	DateOfRemoval      string `json:"date_of_removal"`
	// The live API serializes these as JSON floats even for whole-pound
	// amounts (e.g. 12330.0), unlike the plain-integer numeric fields
	// elsewhere in this response -- confirmed live, and it broke a
	// strict *int64 unmarshal immediately on real data.
	LatestIncome       *float64 `json:"latest_income"`
	LatestExpenditure  *float64 `json:"latest_expenditure"`
	AddressLineOne     string   `json:"address_line_one"`
	AddressLineTwo     string   `json:"address_line_two"`
	AddressLineThree   string   `json:"address_line_three"`
	AddressLineFour    string   `json:"address_line_four"`
	AddressLineFive    string   `json:"address_line_five"`
	AddressPostCode    string   `json:"address_post_code"`
	Phone              string   `json:"phone"`
	Email              string   `json:"email"`
	Web                string   `json:"web"`
	CharityCoRegNumber string   `json:"charity_co_reg_number"`
	TrusteeNames       []struct {
		TrusteeName string `json:"trustee_name"`
	} `json:"trustee_names"`
}

// roundToInt64 converts a possibly-nil float64 pointer to an int64
// pointer, rounding to the nearest whole unit (these fields are
// whole-currency amounts that the API just happens to serialize with a
// trailing ".0").
func roundToInt64(f *float64) *int64 {
	if f == nil {
		return nil
	}
	v := int64(math.Round(*f))
	return &v
}

// GetCharityDetail fetches a charity's full profile by registered number
// and suffix (0 for the main charity; a value greater than zero for a
// specific subsidiary/linked charity sharing that registered number).
func (c *Client) GetCharityDetail(registeredNumber, suffix int) (CharityDetail, error) {
	u := fmt.Sprintf(c.DetailURL, registeredNumber, suffix)
	body, err := c.get(u)
	if err != nil {
		return CharityDetail{}, err
	}

	var d detailResponse
	if err := json.Unmarshal(body, &d); err != nil {
		return CharityDetail{}, newClientError("parsing charity detail: %v", err)
	}

	addrParts := []string{d.AddressLineOne, d.AddressLineTwo, d.AddressLineThree, d.AddressLineFour, d.AddressLineFive}
	nonEmpty := make([]string, 0, len(addrParts))
	for _, p := range addrParts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}

	trustees := make([]string, 0, len(d.TrusteeNames))
	for _, t := range d.TrusteeNames {
		if t.TrusteeName != "" {
			trustees = append(trustees, t.TrusteeName)
		}
	}

	return CharityDetail{
		OrganisationNumber:   d.OrganisationNumber,
		RegisteredNumber:     d.RegCharityNumber,
		Suffix:               d.GroupSubsidSuffix,
		Name:                 d.CharityName,
		CharityType:          d.CharityType,
		Status:               d.RegStatus,
		RegistrationDate:     d.DateOfRegistration,
		RemovalDate:          d.DateOfRemoval,
		LatestIncome:         roundToInt64(d.LatestIncome),
		LatestExpenditure:    roundToInt64(d.LatestExpenditure),
		Address:              strings.Join(nonEmpty, ", "),
		Postcode:             d.AddressPostCode,
		Phone:                d.Phone,
		Email:                d.Email,
		Website:              d.Web,
		CompaniesHouseNumber: d.CharityCoRegNumber,
		Trustees:             trustees,
	}, nil
}
