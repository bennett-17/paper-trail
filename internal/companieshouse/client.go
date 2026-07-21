// Package companieshouse provides a client for the UK's Companies
// House Public Data API -- company search, company profile, and
// officer (director/secretary) lookups. Unlike the UK Charity
// Commission API elsewhere in this project, this is a single key, not
// a primary/secondary pair, and it authenticates via HTTP Basic Auth
// (the key as username, blank password) rather than a header or query
// parameter -- confirmed live, each of the three integrations in this
// project turned out to use a different auth mechanism.
package companieshouse

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

// DefaultBaseURL is the Companies House API host. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://api.company-information.service.gov.uk"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the Companies House Public Data API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval defaults to a conservative 550ms: Companies House's
	// documented limit is 600 requests per 5-minute window (confirmed
	// live via the X-Ratelimit-* response headers), i.e. one request
	// every 500ms sustained -- this pads that slightly.
	MinInterval time.Duration
	UserAgent   string
	APIKey      string
	BaseURL     string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff on 429, same
	// approach as internal/sanctions.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client. An empty apiKey falls back to the
// COMPANIES_HOUSE_API_KEY environment variable. Returns an error if
// neither is set -- like the UK Charity Commission and sanctions
// APIs, this one has no keyless path.
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("COMPANIES_HOUSE_API_KEY")
	}
	if apiKey == "" {
		return nil, newClientError(
			"the Companies House API requires a REST API key. Register for a free account at " +
				"https://developer.company-information.service.gov.uk, create an application, and " +
				"request a REST key (not Web or Streaming), then set COMPANIES_HOUSE_API_KEY to it.",
		)
	}
	return &Client{
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		MinInterval:    550 * time.Millisecond,
		UserAgent:      "paper-trail (https://github.com/bennett-17/paper-trail)",
		APIKey:         apiKey,
		BaseURL:        DefaultBaseURL,
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

// get performs a GET request with retry-with-backoff on 429, and turns
// any non-2xx status into an actionable error.
func (c *Client) get(u string) ([]byte, error) {
	status, body, err := c.doGetWithRetry(u)
	if err != nil {
		return nil, err
	}
	switch {
	case status >= 200 && status < 300:
		return body, nil
	case status == http.StatusUnauthorized:
		return nil, newClientError(
			"Companies House API returned 401 Unauthorized for %s -- check that "+
				"COMPANIES_HOUSE_API_KEY is a valid, active REST key", u,
		)
	case status == http.StatusNotFound:
		return nil, newClientError("Companies House API returned 404 Not Found for %s -- no such company number or officer ID", u)
	default:
		return nil, newClientError("Companies House API returned HTTP %d for %s", status, u)
	}
}

func (c *Client) doGetWithRetry(u string) (statusCode int, body []byte, err error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, respBody, doErr := c.doGet(u)
		if doErr != nil || status != http.StatusTooManyRequests || attempt >= c.MaxRetries {
			return status, respBody, doErr
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func (c *Client) doGet(u string) (statusCode int, body []byte, err error) {
	c.throttle()

	req, reqErr := http.NewRequest(http.MethodGet, u, nil)
	if reqErr != nil {
		return 0, nil, newClientError("building request for %s: %v", u, reqErr)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.SetBasicAuth(c.APIKey, "")

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

// Address is a UK postal address as Companies House represents it.
type Address struct {
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	Locality   string `json:"locality,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postalCode,omitempty"`
	Country    string `json:"country,omitempty"`
}

// AsSingleLine renders the address as a comma-separated single line,
// skipping empty fields.
func (a Address) AsSingleLine() string {
	parts := []string{a.Line1, a.Line2, a.Locality, a.Region, a.PostalCode, a.Country}
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, ", ")
}

// SearchHit is a single company search result.
type SearchHit struct {
	CompanyNumber  string  `json:"companyNumber"`
	Name           string  `json:"name"`
	Status         string  `json:"status,omitempty"`
	Type           string  `json:"type,omitempty"`
	IncorporatedOn string  `json:"incorporatedOn,omitempty"`
	Address        Address `json:"address,omitempty"`
}

// SearchResult is a page of company search results.
type SearchResult struct {
	Total     int         `json:"total"`
	Companies []SearchHit `json:"companies"`
}

type addressRaw struct {
	AddressLine1 string `json:"address_line_1"`
	AddressLine2 string `json:"address_line_2"`
	Locality     string `json:"locality"`
	Region       string `json:"region"`
	PostalCode   string `json:"postal_code"`
	Country      string `json:"country"`
}

func (a addressRaw) toAddress() Address {
	return Address{
		Line1:      a.AddressLine1,
		Line2:      a.AddressLine2,
		Locality:   a.Locality,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
	}
}

type searchResponse struct {
	Items []struct {
		CompanyNumber  string     `json:"company_number"`
		Title          string     `json:"title"`
		CompanyStatus  string     `json:"company_status"`
		CompanyType    string     `json:"company_type"`
		DateOfCreation string     `json:"date_of_creation"`
		Address        addressRaw `json:"address"`
	} `json:"items"`
	TotalResults int `json:"total_results"`
}

// SearchCompanies searches the Companies House register by name. limit
// caps how many results come back (0 uses the API's own default page size).
func (c *Client) SearchCompanies(name string, limit int) (SearchResult, error) {
	params := url.Values{}
	params.Set("q", name)
	if limit > 0 {
		params.Set("items_per_page", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "/search/companies?" + params.Encode())
	if err != nil {
		return SearchResult{}, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return SearchResult{}, newClientError("parsing company search results: %v", err)
	}

	hits := make([]SearchHit, 0, len(resp.Items))
	for _, item := range resp.Items {
		hits = append(hits, SearchHit{
			CompanyNumber:  item.CompanyNumber,
			Name:           item.Title,
			Status:         item.CompanyStatus,
			Type:           item.CompanyType,
			IncorporatedOn: item.DateOfCreation,
			Address:        item.Address.toAddress(),
		})
	}
	return SearchResult{Total: resp.TotalResults, Companies: hits}, nil
}

// PreviousName is one entry in a company's dated name-change history --
// confirmed live against a real example (Tesco PLC), which carries
// two: "TESCO STORES (HOLDINGS) LIMITED" (1947-1981) and "TESCO STORES
// (HOLDINGS) PUBLIC LIMITED COMPANY" (1981-1983) before its current
// name. Empty CeasedOn would mean still in use, but that can't happen
// here -- these are by definition names the company no longer uses.
type PreviousName struct {
	Name          string `json:"name"`
	EffectiveFrom string `json:"effectiveFrom,omitempty"`
	CeasedOn      string `json:"ceasedOn,omitempty"`
}

// Company is a company's public profile.
type Company struct {
	CompanyNumber    string         `json:"companyNumber"`
	Name             string         `json:"name"`
	Status           string         `json:"status,omitempty"`
	Type             string         `json:"type,omitempty"`
	IncorporatedOn   string         `json:"incorporatedOn,omitempty"`
	RegisteredOffice Address        `json:"registeredOffice,omitempty"`
	SICCodes         []string       `json:"sicCodes,omitempty"`
	PreviousNames    []PreviousName `json:"previousNames,omitempty"`
}

type companyResponse struct {
	CompanyName             string     `json:"company_name"`
	CompanyNumber           string     `json:"company_number"`
	CompanyStatus           string     `json:"company_status"`
	Type                    string     `json:"type"`
	DateOfCreation          string     `json:"date_of_creation"`
	RegisteredOfficeAddress addressRaw `json:"registered_office_address"`
	SICCodes                []string   `json:"sic_codes"`
	PreviousCompanyNames    []struct {
		Name          string `json:"name"`
		EffectiveFrom string `json:"effective_from"`
		CeasedOn      string `json:"ceased_on"`
	} `json:"previous_company_names"`
}

// zeroPadCompanyNumber left-pads a company number to Companies House's
// full 8-character form with leading zeros -- confirmed live, other
// sources in this project (e.g. the UK Charity Commission's
// CompaniesHouseNumber field) return the number without them, and the
// unpadded form 404s against this API. A number already 8 characters
// (including the 2-letter jurisdiction prefixes some company types
// use, e.g. "SC012345") is left untouched.
func zeroPadCompanyNumber(number string) string {
	number = strings.TrimSpace(number)
	for len(number) < 8 {
		number = "0" + number
	}
	return number
}

// GetCompany fetches a company's public profile by its exact company
// number (zero-padded to 8 characters automatically if needed).
func (c *Client) GetCompany(number string) (Company, error) {
	number = zeroPadCompanyNumber(number)
	body, err := c.get(c.BaseURL + "/company/" + url.PathEscape(number))
	if err != nil {
		return Company{}, err
	}

	var resp companyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Company{}, newClientError("parsing company profile: %v", err)
	}

	previousNames := make([]PreviousName, 0, len(resp.PreviousCompanyNames))
	for _, pn := range resp.PreviousCompanyNames {
		previousNames = append(previousNames, PreviousName{
			Name:          pn.Name,
			EffectiveFrom: pn.EffectiveFrom,
			CeasedOn:      pn.CeasedOn,
		})
	}

	return Company{
		CompanyNumber:    resp.CompanyNumber,
		Name:             resp.CompanyName,
		Status:           resp.CompanyStatus,
		Type:             resp.Type,
		IncorporatedOn:   resp.DateOfCreation,
		RegisteredOffice: resp.RegisteredOfficeAddress.toAddress(),
		SICCodes:         resp.SICCodes,
		PreviousNames:    previousNames,
	}, nil
}

// Officer is a single company officer (director, secretary, etc.),
// current or former.
type Officer struct {
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"`
	AppointedOn string `json:"appointedOn,omitempty"`
	ResignedOn  string `json:"resignedOn,omitempty"` // empty if currently serving
	// OfficerID is a stable per-person identifier (confirmed live via
	// each officer's links.officer.appointments field), distinct from
	// this specific appointment. Pass it to GetOfficerAppointments to
	// fan out to every other company this same person is linked to
	// across the whole register -- not just the ones an initial name
	// search happened to surface. Empty if the API didn't return a link
	// (observed for some corporate officers).
	OfficerID string `json:"officerId,omitempty"`
}

type officersResponse struct {
	Items []struct {
		Name        string `json:"name"`
		OfficerRole string `json:"officer_role"`
		AppointedOn string `json:"appointed_on"`
		ResignedOn  string `json:"resigned_on"`
		Links       struct {
			Officer struct {
				Appointments string `json:"appointments"`
			} `json:"officer"`
		} `json:"links"`
	} `json:"items"`
	TotalResults int `json:"total_results"`
}

// officerIDFromAppointmentsLink extracts the officer ID out of a
// links.officer.appointments path of the form
// "/officers/{id}/appointments". Returns "" if the path doesn't match
// that shape (e.g. no link was returned at all).
func officerIDFromAppointmentsLink(path string) string {
	const prefix, suffix = "/officers/", "/appointments"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
}

// GetOfficers fetches the officers (directors, secretaries, and other
// registrable roles, current and former) of a company by its exact
// company number. limit caps how many come back (0 uses the API's own
// default page size).
func (c *Client) GetOfficers(number string, limit int) ([]Officer, error) {
	number = zeroPadCompanyNumber(number)
	u := c.BaseURL + "/company/" + url.PathEscape(number) + "/officers"
	if limit > 0 {
		params := url.Values{}
		params.Set("items_per_page", strconv.Itoa(limit))
		u += "?" + params.Encode()
	}
	body, err := c.get(u)
	if err != nil {
		return nil, err
	}

	var resp officersResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing company officers: %v", err)
	}

	officers := make([]Officer, 0, len(resp.Items))
	for _, item := range resp.Items {
		officers = append(officers, Officer{
			Name:        item.Name,
			Role:        item.OfficerRole,
			AppointedOn: item.AppointedOn,
			ResignedOn:  item.ResignedOn,
			OfficerID:   officerIDFromAppointmentsLink(item.Links.Officer.Appointments),
		})
	}
	return officers, nil
}

// PSC is a single Person with Significant Control -- a beneficial
// owner, not necessarily a listed officer -- current or former.
// Confirmed live: an entry with no Name is a PSC "statement" (e.g. "no
// individual or entity with significant control has been identified"),
// not an actual person or company, so GetPersonsWithSignificantControl
// drops those rather than returning a blank name.
type PSC struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind,omitempty"` // e.g. "individual-person-with-significant-control", "corporate-entity-person-with-significant-control"
	NaturesOfControl []string `json:"naturesOfControl,omitempty"`
	NotifiedOn       string   `json:"notifiedOn,omitempty"`
	CeasedOn         string   `json:"ceasedOn,omitempty"` // empty if still active
}

type pscResponse struct {
	Items []struct {
		Name             string   `json:"name"`
		Kind             string   `json:"kind"`
		NaturesOfControl []string `json:"natures_of_control"`
		NotifiedOn       string   `json:"notified_on"`
		CeasedOn         string   `json:"ceased_on"`
	} `json:"items"`
	TotalResults int `json:"total_results"`
}

// GetPersonsWithSignificantControl fetches the beneficial owners (PSCs,
// current and former) of a company by its exact company number. limit
// caps how many come back (0 uses the API's own default page size).
func (c *Client) GetPersonsWithSignificantControl(number string, limit int) ([]PSC, error) {
	number = zeroPadCompanyNumber(number)
	u := c.BaseURL + "/company/" + url.PathEscape(number) + "/persons-with-significant-control"
	if limit > 0 {
		params := url.Values{}
		params.Set("items_per_page", strconv.Itoa(limit))
		u += "?" + params.Encode()
	}
	body, err := c.get(u)
	if err != nil {
		return nil, err
	}

	var resp pscResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing persons with significant control: %v", err)
	}

	pscs := make([]PSC, 0, len(resp.Items))
	for _, item := range resp.Items {
		if item.Name == "" {
			continue // a PSC "statement" (e.g. none identified), not an actual person/company
		}
		pscs = append(pscs, PSC{
			Name:             item.Name,
			Kind:             item.Kind,
			NaturesOfControl: item.NaturesOfControl,
			NotifiedOn:       item.NotifiedOn,
			CeasedOn:         item.CeasedOn,
		})
	}
	return pscs, nil
}

// DisqualifiedOfficer is a single disqualified-officer search hit.
// Confirmed live: the search endpoint has no date-of-birth or address
// filter, only a name query, so a hit here is a name-only match --
// common names collide the same way they do on a sanctions list, and
// this is a lead to verify, not a confirmed identity match.
type DisqualifiedOfficer struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"` // e.g. "Disqualified" or "Born March 1977 - Disqualified"
	Address     Address `json:"address,omitempty"`
}

type disqualifiedOfficersSearchResponse struct {
	Items []struct {
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Address     addressRaw `json:"address"`
	} `json:"items"`
	TotalResults int `json:"total_results"`
}

// SearchDisqualifiedOfficers searches the Companies House disqualified
// officers register by name (natural persons and corporate officers
// both, e.g. a company acting as a corporate director). limit caps how
// many results come back (0 uses the API's own default page size).
func (c *Client) SearchDisqualifiedOfficers(name string, limit int) ([]DisqualifiedOfficer, error) {
	params := url.Values{}
	params.Set("q", name)
	if limit > 0 {
		params.Set("items_per_page", strconv.Itoa(limit))
	}
	body, err := c.get(c.BaseURL + "/search/disqualified-officers?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp disqualifiedOfficersSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing disqualified officer search results: %v", err)
	}

	hits := make([]DisqualifiedOfficer, 0, len(resp.Items))
	for _, item := range resp.Items {
		hits = append(hits, DisqualifiedOfficer{
			Name:        item.Title,
			Description: item.Description,
			Address:     item.Address.toAddress(),
		})
	}
	return hits, nil
}

// Charge is a single registered charge (mortgage/debenture) against a
// company. PersonsEntitled is who benefits from the charge -- the
// lender/chargeholder -- a counterparty relationship distinct from an
// officer or PSC. Note: for a very common lender (a major clearing
// bank), the same name will show up across an enormous number of
// otherwise-unrelated companies -- see internal/risk's SharedChargees
// for how that's handled.
type Charge struct {
	ChargeCode      string   `json:"chargeCode,omitempty"`
	Classification  string   `json:"classification,omitempty"`
	Status          string   `json:"status,omitempty"` // e.g. "outstanding", "fully-satisfied", "part-satisfied"
	DeliveredOn     string   `json:"deliveredOn,omitempty"`
	CreatedOn       string   `json:"createdOn,omitempty"`
	SatisfiedOn     string   `json:"satisfiedOn,omitempty"` // empty if not (yet) satisfied
	PersonsEntitled []string `json:"personsEntitled,omitempty"`
	Particulars     string   `json:"particulars,omitempty"`
}

type chargesResponse struct {
	Items []struct {
		ChargeCode     string `json:"charge_code"`
		Classification struct {
			Description string `json:"description"`
		} `json:"classification"`
		Status      string `json:"status"`
		DeliveredOn string `json:"delivered_on"`
		CreatedOn   string `json:"created_on"`
		SatisfiedOn string `json:"satisfied_on"`
		Particulars struct {
			Description string `json:"description"`
		} `json:"particulars"`
		PersonsEntitled []struct {
			Name string `json:"name"`
		} `json:"persons_entitled"`
	} `json:"items"`
	TotalCount int `json:"total_count"`
}

// GetCharges fetches the registered charges (mortgages/debentures)
// against a company by its exact company number (zero-padded
// automatically). limit caps how many come back (0 uses the API's own
// default page size).
func (c *Client) GetCharges(number string, limit int) ([]Charge, error) {
	number = zeroPadCompanyNumber(number)
	u := c.BaseURL + "/company/" + url.PathEscape(number) + "/charges"
	if limit > 0 {
		params := url.Values{}
		params.Set("items_per_page", strconv.Itoa(limit))
		u += "?" + params.Encode()
	}
	body, err := c.get(u)
	if err != nil {
		return nil, err
	}

	var resp chargesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing company charges: %v", err)
	}

	charges := make([]Charge, 0, len(resp.Items))
	for _, item := range resp.Items {
		entitled := make([]string, 0, len(item.PersonsEntitled))
		for _, pe := range item.PersonsEntitled {
			if pe.Name != "" {
				entitled = append(entitled, pe.Name)
			}
		}
		charges = append(charges, Charge{
			ChargeCode:      item.ChargeCode,
			Classification:  item.Classification.Description,
			Status:          item.Status,
			DeliveredOn:     item.DeliveredOn,
			CreatedOn:       item.CreatedOn,
			SatisfiedOn:     item.SatisfiedOn,
			PersonsEntitled: entitled,
			Particulars:     item.Particulars.Description,
		})
	}
	return charges, nil
}

// CountCompaniesAtLocation returns how many companies register-wide
// have a registered office matching the given location text (a
// postcode works well and is what this project uses). This is a
// density check, not a specific-company lookup: confirmed live, a
// known company-formation-agent mail-drop address (71-75 Shelton
// Street, WC2H 9JQ) returns roughly 190,000 companies, versus 5-70
// for ordinary single-business addresses -- a real, well-known
// shell-company tell (registered-agent address clustering), distinct
// from this project's shared_address check, which needs two entities
// already found at the same address rather than flagging one
// entity's address in isolation. A location with no matches returns 0
// (confirmed live: the API 404s rather than returning a clean empty
// result, unlike its own /search/companies endpoint).
func (c *Client) CountCompaniesAtLocation(location string) (int, error) {
	u := c.BaseURL + "/advanced-search/companies?" + url.Values{
		"location": {location},
		"size":     {"1"}, // only the total count is needed, not the matches themselves
	}.Encode()

	status, body, err := c.doGetWithRetry(u)
	if err != nil {
		return 0, err
	}
	switch {
	case status >= 200 && status < 300:
		// fall through to parse below
	case status == http.StatusNotFound:
		return 0, nil
	case status == http.StatusUnauthorized:
		return 0, newClientError(
			"Companies House API returned 401 Unauthorized for %s -- check that "+
				"COMPANIES_HOUSE_API_KEY is a valid, active REST key", u,
		)
	default:
		return 0, newClientError("Companies House API returned HTTP %d for %s", status, u)
	}

	var resp struct {
		Hits int `json:"hits"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, newClientError("parsing advanced search results: %v", err)
	}
	return resp.Hits, nil
}

// Appointment is one company appointment held by a single officer,
// as returned by GetOfficerAppointments -- this is how paper-trail
// follows a director from one company to every other company they're
// linked to across the whole register, not just the ones an initial
// name search happened to surface.
type Appointment struct {
	CompanyName   string `json:"companyName"`
	CompanyNumber string `json:"companyNumber"`
	CompanyStatus string `json:"companyStatus,omitempty"`
	Role          string `json:"role,omitempty"`
	AppointedOn   string `json:"appointedOn,omitempty"`
	ResignedOn    string `json:"resignedOn,omitempty"` // empty if still serving
}

type appointmentsResponse struct {
	Items []struct {
		OfficerRole string `json:"officer_role"`
		AppointedOn string `json:"appointed_on"`
		ResignedOn  string `json:"resigned_on"`
		AppointedTo struct {
			CompanyName   string `json:"company_name"`
			CompanyNumber string `json:"company_number"`
			CompanyStatus string `json:"company_status"`
		} `json:"appointed_to"`
	} `json:"items"`
	TotalResults int `json:"total_results"`
}

// GetOfficerAppointments fetches every company appointment held by a
// single officer, identified by the stable OfficerID from an Officer
// returned by GetOfficers (not a company number). limit caps how many
// come back (0 uses the API's own default page size).
func (c *Client) GetOfficerAppointments(officerID string, limit int) ([]Appointment, error) {
	u := c.BaseURL + "/officers/" + url.PathEscape(officerID) + "/appointments"
	if limit > 0 {
		params := url.Values{}
		params.Set("items_per_page", strconv.Itoa(limit))
		u += "?" + params.Encode()
	}
	body, err := c.get(u)
	if err != nil {
		return nil, err
	}

	var resp appointmentsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing officer appointments: %v", err)
	}

	appointments := make([]Appointment, 0, len(resp.Items))
	for _, item := range resp.Items {
		appointments = append(appointments, Appointment{
			CompanyName:   item.AppointedTo.CompanyName,
			CompanyNumber: item.AppointedTo.CompanyNumber,
			CompanyStatus: item.AppointedTo.CompanyStatus,
			Role:          item.OfficerRole,
			AppointedOn:   item.AppointedOn,
			ResignedOn:    item.ResignedOn,
		})
	}
	return appointments, nil
}
