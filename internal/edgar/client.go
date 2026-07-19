package edgar

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Default endpoints. Overridable on Client for testing against a local
// httptest server instead of live SEC infrastructure.
const (
	DefaultTickersURL     = "https://www.sec.gov/files/company_tickers.json"
	DefaultSubmissionsURL = "https://data.sec.gov/submissions/CIK%s.json"
	DefaultBrowseEdgarURL = "https://www.sec.gov/cgi-bin/browse-edgar"
)

// ClientError wraps errors raised by this package so callers can
// distinguish them from generic network/parsing failures if needed.
type ClientError struct {
	msg string
}

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to SEC EDGAR's public data endpoints.
//
// SEC requires every automated requester to identify itself via a
// descriptive User-Agent header (name + contact email). See
// https://www.sec.gov/os/accessing-edgar-data. NewClient refuses to
// construct a Client without one, and the client self-throttles to stay
// well under SEC's published rate guidance.
type Client struct {
	UserAgent   string
	MinInterval time.Duration
	HTTPClient  *http.Client

	TickersURL     string
	SubmissionsURL string // format string with a single %s for the 10-digit CIK
	BrowseEdgarURL string

	mu            sync.Mutex
	lastRequestAt time.Time
	tickerByCode  map[string]string // uppercase ticker -> 10-digit CIK
	tickerByName  map[string]string // lowercase name -> 10-digit CIK
}

// NewClient builds a Client. If userAgent is empty, it falls back to the
// EDGAR_USER_AGENT environment variable. Returns an error if no usable
// user agent (containing an "@") is available.
func NewClient(userAgent string) (*Client, error) {
	if userAgent == "" {
		userAgent = os.Getenv("EDGAR_USER_AGENT")
	}
	if userAgent == "" || !strings.Contains(userAgent, "@") {
		return nil, newClientError(
			"SEC EDGAR requires a descriptive User-Agent with a contact " +
				"email, e.g. 'Your Name your.email@example.com'. Set the " +
				"EDGAR_USER_AGENT environment variable or pass it explicitly. " +
				"See https://www.sec.gov/os/accessing-edgar-data",
		)
	}
	return &Client{
		UserAgent:      userAgent,
		MinInterval:    150 * time.Millisecond,
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
		TickersURL:     DefaultTickersURL,
		SubmissionsURL: DefaultSubmissionsURL,
		BrowseEdgarURL: DefaultBrowseEdgarURL,
	}, nil
}

// -- low-level -------------------------------------------------------------

func (c *Client) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.lastRequestAt)
	if elapsed < c.MinInterval {
		time.Sleep(c.MinInterval - elapsed)
	}
	c.lastRequestAt = time.Now()
}

func (c *Client) get(url string) ([]byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, newClientError("building request for %s: %v", url, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, newClientError("request to %s failed: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newClientError("reading response from %s: %v", url, err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, newClientError(
			"SEC EDGAR rate-limited this client (HTTP 429) for %s", url,
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newClientError(
			"SEC EDGAR returned HTTP %d for %s", resp.StatusCode, url,
		)
	}
	return body, nil
}

// -- ticker / CIK resolution ------------------------------------------------

type tickerEntry struct {
	CIKStr int    `json:"cik_str"`
	Ticker string `json:"ticker"`
	Title  string `json:"title"`
}

func (c *Client) loadTickerMap() error {
	c.mu.Lock()
	if c.tickerByCode != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	body, err := c.get(c.TickersURL)
	if err != nil {
		return err
	}

	var raw map[string]tickerEntry
	if err := json.Unmarshal(body, &raw); err != nil {
		return newClientError("parsing ticker map: %v", err)
	}

	byCode := make(map[string]string, len(raw))
	byName := make(map[string]string, len(raw))
	for _, entry := range raw {
		cik10 := fmt.Sprintf("%010d", entry.CIKStr)
		byCode[strings.ToUpper(entry.Ticker)] = cik10
		lowerTitle := strings.ToLower(entry.Title)
		if _, exists := byName[lowerTitle]; !exists {
			byName[lowerTitle] = cik10
		}
	}

	c.mu.Lock()
	c.tickerByCode = byCode
	c.tickerByName = byName
	c.mu.Unlock()
	return nil
}

// ResolveCIK resolves a ticker or company name to a zero-padded 10-digit
// CIK. Tries an exact ticker match, then an exact case-insensitive name
// match, then falls back to a substring match on name. Returns an error
// if nothing matches or the match is ambiguous.
func (c *Client) ResolveCIK(query string) (string, error) {
	query = strings.TrimSpace(query)
	if err := c.loadTickerMap(); err != nil {
		return "", err
	}

	if cik, ok := c.tickerByCode[strings.ToUpper(query)]; ok {
		return cik, nil
	}
	if cik, ok := c.tickerByName[strings.ToLower(query)]; ok {
		return cik, nil
	}

	lowerQuery := strings.ToLower(query)
	candidates := map[string]string{}
	for name, cik := range c.tickerByName {
		if strings.Contains(name, lowerQuery) {
			candidates[name] = cik
		}
	}
	switch len(candidates) {
	case 0:
		return "", newClientError("no company found matching %q", query)
	case 1:
		for _, cik := range candidates {
			return cik, nil
		}
	}
	names := make([]string, 0, 5)
	for name := range candidates {
		names = append(names, name)
		if len(names) == 5 {
			break
		}
	}
	return "", newClientError(
		"%q matched %d companies (e.g. %s, ...). Be more specific or use a ticker.",
		query, len(candidates), strings.Join(names, ", "),
	)
}

// -- company profile ---------------------------------------------------------

type submissionsResponse struct {
	CIK            string       `json:"cik"`
	Name           string       `json:"name"`
	EntityType     string       `json:"entityType"`
	SIC            string       `json:"sic"`
	SICDescription string       `json:"sicDescription"`
	Tickers        []string     `json:"tickers"`
	FiscalYearEnd  string       `json:"fiscalYearEnd"`
	FormerNames    []FormerName `json:"formerNames"`
	Addresses      struct {
		Business *addressJSON `json:"business"`
		Mailing  *addressJSON `json:"mailing"`
	} `json:"addresses"`
	Filings struct {
		Recent struct {
			AccessionNumber []string `json:"accessionNumber"`
			FilingDate      []string `json:"filingDate"`
			ReportDate      []string `json:"reportDate"`
			Form            []string `json:"form"`
			PrimaryDocument []string `json:"primaryDocument"`
		} `json:"recent"`
	} `json:"filings"`
}

type addressJSON struct {
	Street1                   string `json:"street1"`
	Street2                   string `json:"street2"`
	City                      string `json:"city"`
	StateOrCountry            string `json:"stateOrCountry"`
	StateOrCountryDescription string `json:"stateOrCountryDescription"`
	ZipCode                   string `json:"zipCode"`
}

func (a *addressJSON) toAddress() *Address {
	if a == nil {
		return nil
	}
	stateOrCountry := a.StateOrCountry
	if stateOrCountry == "" {
		stateOrCountry = a.StateOrCountryDescription
	}
	return &Address{
		Street1:        a.Street1,
		Street2:        a.Street2,
		City:           a.City,
		StateOrCountry: stateOrCountry,
		ZipCode:        a.ZipCode,
	}
}

func (c *Client) fetchSubmissions(cik10 string) (*submissionsResponse, error) {
	url := fmt.Sprintf(c.SubmissionsURL, cik10)
	body, err := c.get(url)
	if err != nil {
		return nil, err
	}
	var data submissionsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, newClientError("parsing submissions for CIK %s: %v", cik10, err)
	}
	return &data, nil
}

// GetCompany fetches a filer's EDGAR profile (names, addresses, SIC code,
// entity type).
func (c *Client) GetCompany(cik string) (Company, error) {
	cik10 := zeroPadCIK(cik)
	data, err := c.fetchSubmissions(cik10)
	if err != nil {
		return Company{}, err
	}

	tickers := data.Tickers
	if tickers == nil {
		tickers = []string{}
	}
	formerNames := data.FormerNames
	if formerNames == nil {
		formerNames = []FormerName{}
	}

	return Company{
		CIK:             cik10,
		Name:            data.Name,
		Tickers:         tickers,
		SIC:             data.SIC,
		SICDescription:  data.SICDescription,
		FormerNames:     formerNames,
		BusinessAddress: data.Addresses.Business.toAddress(),
		MailingAddress:  data.Addresses.Mailing.toAddress(),
		FiscalYearEnd:   data.FiscalYearEnd,
		EntityType:      data.EntityType,
	}, nil
}

// GetFilings lists recent filings for a CIK, optionally filtered by form
// type (e.g. "10-K", "4"), capped at limit results.
func (c *Client) GetFilings(cik string, form string, limit int) ([]Filing, error) {
	cik10 := zeroPadCIK(cik)
	data, err := c.fetchSubmissions(cik10)
	if err != nil {
		return nil, err
	}

	recent := data.Filings.Recent
	filings := make([]Filing, 0, limit)
	for i := 0; i < len(recent.Form); i++ {
		if form != "" && !strings.EqualFold(recent.Form[i], form) {
			continue
		}
		f := Filing{
			AccessionNumber: recent.AccessionNumber[i],
			Form:            recent.Form[i],
			FilingDate:      recent.FilingDate[i],
			CIK:             cik10,
		}
		if i < len(recent.ReportDate) {
			f.ReportDate = recent.ReportDate[i]
		}
		if i < len(recent.PrimaryDocument) {
			f.PrimaryDocument = recent.PrimaryDocument[i]
		}
		filings = append(filings, f)
		if len(filings) >= limit {
			break
		}
	}
	return filings, nil
}

// -- relationships -----------------------------------------------------------

// GetFormerNameRelationships derives relationship edges from a company's
// own former names. This is the most reliable relationship signal
// available without an OpenCorporates key: it comes directly from the
// submissions JSON, no HTML/Atom parsing involved.
func GetFormerNameRelationships(company Company) []Relationship {
	edges := make([]Relationship, 0, len(company.FormerNames))
	for _, fn := range company.FormerNames {
		edges = append(edges, Relationship{
			SourceCIK:        company.CIK,
			SourceName:       company.Name,
			TargetCIK:        company.CIK,
			TargetName:       fn.Name,
			RelationshipType: "former_name",
		})
	}
	return edges
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Content atomEntryContent `xml:"content"`
}

type atomEntryContent struct {
	AccessionNumber string `xml:"accession-number"`
	FilingHref      string `xml:"filing-href"`
	FilingType      string `xml:"filing-type"`
}

// filingDirectoryListing mirrors the JSON SEC serves at
// "<filing-directory>/index.json" for every filing. Filer software names
// the primary document differently (form4.xml, primary_doc.xml, etc.),
// so this is used to find it by extension rather than guessing.
type filingDirectoryListing struct {
	Directory struct {
		Item []struct {
			Name string `json:"name"`
		} `json:"item"`
	} `json:"directory"`
}

// ownershipDocument is the schema of a Form 3/4/5 XML ownership document,
// trimmed to the fields needed to identify the reporting owner(s).
type ownershipDocument struct {
	ReportingOwners []struct {
		ID struct {
			CIK  string `xml:"rptOwnerCik"`
			Name string `xml:"rptOwnerName"`
		} `xml:"reportingOwnerId"`
	} `xml:"reportingOwner"`
}

type reportingOwner struct {
	cik  string
	name string
}

// GetInsiderRelationships derives relationship edges from Form 3/4/5
// insider filings tied to a CIK.
//
// insiderFormTypes controls both which filings count as evidence of an
// insider relationship and the priority order edges are recorded in when
// the same person shows up more than once (an earlier type "wins" the
// single evidence slot on Relationship), ordered most- to least-current:
// Form 4 (individual trades, the most current signal), Form 5 (annual
// catch-all for deferred/exempt transactions, filed once a year), then
// Form 3 (one-time initial ownership statement, filed once on
// appointment and potentially very old). Querying all three means an
// insider shows up even if they haven't filed a Form 4 recently.
var insiderFormTypes = []string{"4", "5", "3"}

// SEC's Atom feed for these filings no longer carries the reporting
// owner's name in its <title> (just the form's boilerplate description),
// so for each filing this fetches the filing's own directory listing to
// find its primary XML document, then reads the reporting owner(s)
// straight out of that document. That's two extra requests per filing on
// top of each feed fetch, throttled the same as every other request —
// querying three form types roughly triples total request count versus
// Form 4 alone.
func (c *Client) GetInsiderRelationships(cik, companyName string, limit int) ([]Relationship, error) {
	cik10 := zeroPadCIK(cik)
	edges := make([]Relationship, 0, limit)
	seen := make(map[string]bool)

	for _, formType := range insiderFormTypes {
		url := fmt.Sprintf(
			"%s?action=getcompany&CIK=%s&type=%s&dateb=&owner=include&count=%d&output=atom",
			c.BrowseEdgarURL, cik10, formType, limit,
		)
		body, err := c.get(url)
		if err != nil {
			return nil, err
		}

		var feed atomFeed
		dec := xml.NewDecoder(bytes.NewReader(body))
		dec.CharsetReader = charsetReader
		if err := dec.Decode(&feed); err != nil {
			return nil, newClientError("parsing insider filings (type %s) atom feed: %v", formType, err)
		}

		for _, entry := range feed.Entries {
			if entry.Content.FilingHref == "" {
				continue
			}
			owners, err := c.fetchReportingOwners(entry.Content.FilingHref)
			if err != nil {
				continue // one unreadable filing shouldn't sink the whole graph
			}
			form := entry.Content.FilingType
			if form == "" {
				form = formType
			}
			for _, owner := range owners {
				key := owner.cik + "|" + owner.name
				if seen[key] {
					continue // already recorded via an earlier (higher-priority) filing
				}
				seen[key] = true
				edges = append(edges, Relationship{
					SourceCIK:               cik10,
					SourceName:              companyName,
					TargetCIK:               owner.cik,
					TargetName:              owner.name,
					RelationshipType:        "insider_filer",
					EvidenceForm:            form,
					EvidenceAccessionNumber: entry.Content.AccessionNumber,
				})
			}
		}
	}
	return edges, nil
}

// fetchReportingOwners looks up the reporting owner(s) named in the Form
// 3/4/5 filing at filingHref (an absolute URL to that filing's -index.htm
// page, as given in SEC's Atom feed).
func (c *Client) fetchReportingOwners(filingHref string) ([]reportingOwner, error) {
	i := strings.LastIndex(filingHref, "/")
	if i < 0 {
		return nil, newClientError("malformed filing href %q", filingHref)
	}
	dir := filingHref[:i+1]

	listingBody, err := c.get(dir + "index.json")
	if err != nil {
		return nil, err
	}
	var listing filingDirectoryListing
	if err := json.Unmarshal(listingBody, &listing); err != nil {
		return nil, newClientError("parsing filing directory listing: %v", err)
	}

	var xmlName string
	for _, item := range listing.Directory.Item {
		if strings.HasSuffix(strings.ToLower(item.Name), ".xml") {
			xmlName = item.Name
			break
		}
	}
	if xmlName == "" {
		return nil, newClientError("no XML document found in filing directory %s", dir)
	}

	docBody, err := c.get(dir + xmlName)
	if err != nil {
		return nil, err
	}
	var doc ownershipDocument
	dec := xml.NewDecoder(bytes.NewReader(docBody))
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&doc); err != nil {
		return nil, newClientError("parsing ownership document %s: %v", dir+xmlName, err)
	}

	owners := make([]reportingOwner, 0, len(doc.ReportingOwners))
	for _, ro := range doc.ReportingOwners {
		name := strings.TrimSpace(ro.ID.Name)
		cikVal := strings.TrimSpace(ro.ID.CIK)
		if name == "" || cikVal == "" {
			continue
		}
		owners = append(owners, reportingOwner{cik: zeroPadCIK(cikVal), name: name})
	}
	return owners, nil
}

func zeroPadCIK(cik string) string {
	cik = strings.TrimSpace(cik)
	for len(cik) < 10 {
		cik = "0" + cik
	}
	return cik
}

// charsetReader lets encoding/xml decode SEC's Atom feeds, which are
// served as ISO-8859-1 (Latin-1) rather than UTF-8. Handled manually
// rather than pulling in golang.org/x/text/encoding/charmap: Latin-1 is
// a direct byte-to-codepoint mapping, so the conversion is a few lines.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "latin1", "latin-1":
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return strings.NewReader(string(runes)), nil
	case "", "utf-8", "us-ascii":
		return input, nil
	default:
		// Unknown charset: hand the bytes back as-is rather than failing
		// outright; most feeds that reach here will still be ASCII-safe.
		return input, nil
	}
}
