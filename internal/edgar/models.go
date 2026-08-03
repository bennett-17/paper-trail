// Package edgar provides a client for SEC EDGAR's public JSON/Atom
// endpoints and the data types used throughout the tool.
package edgar

import (
	"fmt"
	"strconv"
	"strings"
)

// FormerName is a previous legal name a filer registered with SEC.
type FormerName struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Address is a mailing or business address as recorded in EDGAR.
type Address struct {
	Street1        string `json:"street1,omitempty"`
	Street2        string `json:"street2,omitempty"`
	City           string `json:"city,omitempty"`
	StateOrCountry string `json:"stateOrCountry,omitempty"`
	ZipCode        string `json:"zipCode,omitempty"`
}

// AsSingleLine renders the address as a comma-separated single line,
// skipping empty fields.
func (a Address) AsSingleLine() string {
	parts := []string{a.Street1, a.Street2, a.City, a.StateOrCountry, a.ZipCode}
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, ", ")
}

// Company is a filer's EDGAR profile.
type Company struct {
	CIK             string       `json:"cik"`
	Name            string       `json:"name"`
	Tickers         []string     `json:"tickers,omitempty"`
	SIC             string       `json:"sic,omitempty"`
	SICDescription  string       `json:"sicDescription,omitempty"`
	FormerNames     []FormerName `json:"formerNames,omitempty"`
	BusinessAddress *Address     `json:"businessAddress,omitempty"`
	MailingAddress  *Address     `json:"mailingAddress,omitempty"`
	FiscalYearEnd   string       `json:"fiscalYearEnd,omitempty"`
	EntityType      string       `json:"entityType,omitempty"`
}

// Filing is a single SEC filing entry from a company's submissions history.
type Filing struct {
	AccessionNumber string `json:"accessionNumber"`
	Form            string `json:"form"`
	FilingDate      string `json:"filingDate"`
	ReportDate      string `json:"reportDate,omitempty"`
	PrimaryDocument string `json:"primaryDocument,omitempty"`
	CIK             string `json:"cik"`
	// Items is the comma-separated 8-K item codes disclosed in this
	// filing (e.g. "2.02,7.01,9.01"), empty for every non-8-K form.
	Items string `json:"items,omitempty"`
}

// HasItem reports whether an 8-K filing discloses the given item code
// (e.g. "4.02", the Item 4.02 non-reliance-on-previously-issued-
// financial-statements disclosure -- a company's own admission that
// prior financials can no longer be relied on, i.e. a restatement).
// Always false for a non-8-K filing, since Items is only ever
// populated for that form.
func (f Filing) HasItem(code string) bool {
	for _, item := range strings.Split(f.Items, ",") {
		if strings.TrimSpace(item) == code {
			return true
		}
	}
	return false
}

// IndexURL returns the human-readable EDGAR filing index page for this filing.
func (f Filing) IndexURL() string {
	accNoDashes := strings.ReplaceAll(f.AccessionNumber, "-", "")
	cikInt, err := strconv.Atoi(strings.TrimLeft(f.CIK, "0"))
	if err != nil || f.CIK == "" {
		cikInt = 0
	}
	return fmt.Sprintf(
		"https://www.sec.gov/Archives/edgar/data/%d/%s/%s-index.htm",
		cikInt, accNoDashes, f.AccessionNumber,
	)
}

// RelatedEntity is another CIK whose current or former legal name
// exactly matches (after normalization) one of a queried company's own
// current/former names -- evidence that a single business's SEC filing
// history has been split across two filer identities by a corporate
// restructuring (holdco reorganization, ticker migration, spin-off).
type RelatedEntity struct {
	CIK         string       `json:"cik"`
	Name        string       `json:"name"`
	FormerNames []FormerName `json:"formerNames,omitempty"`
}

// Relationship is an edge between a company and a related filer
// (e.g. an insider, or a former name of the same entity).
type Relationship struct {
	SourceCIK               string `json:"sourceCik"`
	SourceName              string `json:"sourceName"`
	TargetCIK               string `json:"targetCik"`
	TargetName              string `json:"targetName"`
	RelationshipType        string `json:"relationshipType"` // e.g. "insider_filer", "former_name"
	EvidenceForm            string `json:"evidenceForm,omitempty"`
	EvidenceAccessionNumber string `json:"evidenceAccessionNumber,omitempty"`
}
