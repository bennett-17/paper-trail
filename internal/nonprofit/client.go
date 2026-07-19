// Package nonprofit provides a client for ProPublica's Nonprofit
// Explorer API (built on IRS Form 990 e-file data). It exists to cover
// the gap SEC EDGAR can't: churches, charities, and other 501(c)
// organizations file Form 990 with the IRS, not with the SEC, so they
// never appear as filers in internal/edgar no matter how they're
// searched. No API key is required; the API is free and public.
package nonprofit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// Default endpoints. Overridable on Client for testing against a local
// httptest server instead of the live API.
const (
	DefaultSearchURL       = "https://projects.propublica.org/nonprofits/api/v2/search.json"
	DefaultOrganizationURL = "https://projects.propublica.org/nonprofits/api/v2/organizations/%s.json"
)

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to ProPublica's Nonprofit Explorer API. Unlike SEC
// EDGAR, ProPublica doesn't require a specific User-Agent format, but
// this still self-throttles and identifies itself as a courtesy.
type Client struct {
	HTTPClient  *http.Client
	MinInterval time.Duration
	UserAgent   string

	SearchURL       string
	OrganizationURL string // format string with a single %s for the EIN (digits only)

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		HTTPClient:      &http.Client{Timeout: 15 * time.Second},
		MinInterval:     150 * time.Millisecond,
		UserAgent:       "paper-trail (https://github.com/bennett-17/paper-trail)",
		SearchURL:       DefaultSearchURL,
		OrganizationURL: DefaultOrganizationURL,
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
	return c.doGet(u, false)
}

// getTolerant404 is like get, but treats HTTP 404 as a valid response
// rather than an error. ProPublica's search endpoint returns 404 --
// with a normal JSON body reporting zero results -- for queries that
// match nothing, so a strict "any non-2xx is an error" check would
// misreport a legitimate empty result as a failure.
func (c *Client) getTolerant404(u string) ([]byte, error) {
	return c.doGet(u, true)
}

func (c *Client) doGet(u string, tolerate404 bool) ([]byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, newClientError("request to %s failed: %v", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newClientError("reading response from %s: %v", u, err)
	}
	if tolerate404 && resp.StatusCode == http.StatusNotFound {
		return body, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newClientError("ProPublica Nonprofit Explorer returned HTTP %d for %s", resp.StatusCode, u)
	}
	return body, nil
}

var nonDigitRE = regexp.MustCompile(`\D`)

// normalizeEIN strips formatting (hyphens, spaces) from an EIN so it can
// be used in an API path, e.g. "43-2050079" -> "432050079".
func normalizeEIN(ein string) string {
	return nonDigitRE.ReplaceAllString(ein, "")
}

// formatEIN renders a 9-digit EIN in its standard "XX-XXXXXXX" form.
func formatEIN(ein int64) string {
	return fmt.Sprintf("%02d-%07d", ein/10_000_000, ein%10_000_000)
}

// Organization is a 501(c) entity registered with the IRS.
type Organization struct {
	EIN      string `json:"ein"` // "XX-XXXXXXX"
	Name     string `json:"name"`
	SubName  string `json:"subName,omitempty"` // often the specific local chapter/mission name
	City     string `json:"city,omitempty"`
	State    string `json:"state,omitempty"`
	NTEECode string `json:"nteeCode,omitempty"` // National Taxonomy of Exempt Entities activity code
}

// SearchResult is one page of an organization name search.
type SearchResult struct {
	Query         string         `json:"query"`
	Page          int            `json:"page"`
	NumPages      int            `json:"numPages"`
	TotalResults  int            `json:"totalResults"`
	Organizations []Organization `json:"organizations"`
}

type searchResponse struct {
	TotalResults  int `json:"total_results"`
	NumPages      int `json:"num_pages"`
	CurPage       int `json:"cur_page"`
	Organizations []struct {
		EIN      int64  `json:"ein"`
		Strein   string `json:"strein"`
		Name     string `json:"name"`
		SubName  string `json:"sub_name"`
		City     string `json:"city"`
		State    string `json:"state"`
		NTEECode string `json:"ntee_code"`
	} `json:"organizations"`
}

// SearchOrganizations searches IRS-registered 501(c) organizations by
// name. Results are ranked by relevance, not alphabetically, and a
// well-known name (e.g. a national organization with many local
// chapters) commonly returns dozens of distinct EINs -- one per legal
// entity, not one per organization "brand". page is 1-indexed; pass 0
// or 1 for the first page.
func (c *Client) SearchOrganizations(query string, page int) (SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	if page > 1 {
		params.Set("page", fmt.Sprintf("%d", page))
	}

	body, err := c.getTolerant404(c.SearchURL + "?" + params.Encode())
	if err != nil {
		return SearchResult{}, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SearchResult{}, newClientError("parsing nonprofit search results: %v", err)
	}

	orgs := make([]Organization, 0, len(resp.Organizations))
	for _, o := range resp.Organizations {
		orgs = append(orgs, Organization{
			EIN:      formatEIN(o.EIN),
			Name:     o.Name,
			SubName:  o.SubName,
			City:     o.City,
			State:    o.State,
			NTEECode: o.NTEECode,
		})
	}
	return SearchResult{
		Query:         query,
		Page:          max(resp.CurPage, 1),
		NumPages:      resp.NumPages,
		TotalResults:  resp.TotalResults,
		Organizations: orgs,
	}, nil
}

// Filing is one year's Form 990 filing.
type Filing struct {
	TaxYear       int    `json:"taxYear"`
	FormType      string `json:"formType,omitempty"` // "990", "990EZ", "990PF", or "" if unknown
	HasFinancials bool   `json:"hasFinancials"`       // true if IRS has published extracted line-item figures for this filing
	TotalRevenue  *int64 `json:"totalRevenue,omitempty"`
	TotalExpenses *int64 `json:"totalExpenses,omitempty"`
	TotalAssets   *int64 `json:"totalAssets,omitempty"`
	PDFURL        string `json:"pdfUrl,omitempty"`
}

// OrganizationProfile is a nonprofit's IRS registration details plus its
// available Form 990 filing history, newest first.
type OrganizationProfile struct {
	Organization Organization `json:"organization"`
	Filings      []Filing     `json:"filings"`
}

type organizationResponse struct {
	Organization struct {
		EIN      int64  `json:"ein"`
		Name     string `json:"name"`
		Address  string `json:"address"`
		City     string `json:"city"`
		State    string `json:"state"`
		Zipcode  string `json:"zipcode"`
		NTEECode string `json:"ntee_code"`
	} `json:"organization"`
	FilingsWithData []struct {
		TaxPrdYr     int    `json:"tax_prd_yr"`
		FormType     int    `json:"formtype"`
		TotRevenue   *int64 `json:"totrevenue"`
		TotFuncExpns *int64 `json:"totfuncexpns"`
		TotAssetsEnd *int64 `json:"totassetsend"`
		PDFURL       string `json:"pdf_url"`
	} `json:"filings_with_data"`
	FilingsWithoutData []struct {
		TaxPrdYr    int    `json:"tax_prd_yr"`
		FormTypeStr string `json:"formtype_str"`
		PDFURL      string `json:"pdf_url"`
	} `json:"filings_without_data"`
}

// formTypeName maps ProPublica's numeric form-type code to its common
// name. Undocumented codes fall back to "" rather than guessing.
func formTypeName(code int) string {
	switch code {
	case 0:
		return "990"
	case 1:
		return "990EZ"
	case 2:
		return "990PF"
	default:
		return ""
	}
}

// GetOrganization fetches a specific 501(c) entity's IRS registration
// and Form 990 filing history by EIN (with or without the "XX-XXXXXXX"
// hyphen). Note some organizations -- notably ones that file on paper
// rather than e-file, or whose filings simply haven't been processed
// yet -- have registration data here but zero filings with (or even
// without) extracted figures; that's a real absence in this data
// source, not a bug in this client.
func (c *Client) GetOrganization(ein string) (OrganizationProfile, error) {
	digits := normalizeEIN(ein)
	if digits == "" {
		return OrganizationProfile{}, newClientError("invalid EIN %q", ein)
	}

	body, err := c.get(fmt.Sprintf(c.OrganizationURL, digits))
	if err != nil {
		return OrganizationProfile{}, err
	}

	var resp organizationResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return OrganizationProfile{}, newClientError("parsing organization profile: %v", err)
	}

	filings := make([]Filing, 0, len(resp.FilingsWithData)+len(resp.FilingsWithoutData))
	for _, f := range resp.FilingsWithData {
		filings = append(filings, Filing{
			TaxYear:       f.TaxPrdYr,
			FormType:      formTypeName(f.FormType),
			HasFinancials: true,
			TotalRevenue:  f.TotRevenue,
			TotalExpenses: f.TotFuncExpns,
			TotalAssets:   f.TotAssetsEnd,
			PDFURL:        f.PDFURL,
		})
	}
	for _, f := range resp.FilingsWithoutData {
		filings = append(filings, Filing{
			TaxYear:  f.TaxPrdYr,
			FormType: f.FormTypeStr,
			PDFURL:   f.PDFURL,
		})
	}

	return OrganizationProfile{
		Organization: Organization{
			EIN:      formatEIN(resp.Organization.EIN),
			Name:     resp.Organization.Name,
			City:     resp.Organization.City,
			State:    resp.Organization.State,
			NTEECode: resp.Organization.NTEECode,
		},
		Filings: filings,
	}, nil
}
