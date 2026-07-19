package edgar

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// FullTextHit is a single result from SEC's EDGAR full-text search. Unlike
// ResolveCIK/GetCompany, which look up filers by their own registered
// name, full-text search indexes the *content* of filings -- so it can
// surface a person or company mentioned inside someone else's filing
// (e.g. a stock-gift footnote naming a third party) even when that third
// party has never filed anything under its own name.
type FullTextHit struct {
	AccessionNumber string   `json:"accessionNumber"`
	DocumentFile    string   `json:"documentFile"` // filename within the filing, e.g. "doc4.xml"
	Form            string   `json:"form"`
	FiledDate       string   `json:"filedDate"`
	CIKs            []string `json:"ciks"`
	DisplayNames    []string `json:"displayNames"` // e.g. "INTUITIVE SURGICAL INC (ISRG) (CIK 0001035267)"
}

// IndexURL returns the human-readable EDGAR filing index page for this
// hit, built from its first listed CIK (the issuer, for ownership forms;
// the primary filer otherwise).
func (h FullTextHit) IndexURL() string {
	if len(h.CIKs) == 0 {
		return ""
	}
	accNoDashes := strings.ReplaceAll(h.AccessionNumber, "-", "")
	cikInt, err := strconv.Atoi(strings.TrimLeft(h.CIKs[0], "0"))
	if err != nil {
		cikInt = 0
	}
	return fmt.Sprintf(
		"https://www.sec.gov/Archives/edgar/data/%d/%s/%s-index.htm",
		cikInt, accNoDashes, h.AccessionNumber,
	)
}

type fullTextSearchResponse struct {
	Hits struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []struct {
			ID     string `json:"_id"`
			Source struct {
				CIKs         []string `json:"ciks"`
				DisplayNames []string `json:"display_names"`
				Form         string   `json:"form"`
				FileDate     string   `json:"file_date"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// SearchFullText searches the content of SEC filings for query via
// EDGAR's full-text search (a separate system from the company registry
// used elsewhere in this package). forms and ciks are optional
// comma-separated filters (e.g. "4,8-K" / "0001055919,0001035267");
// startDate/endDate are optional "YYYY-MM-DD" bounds. limit caps how
// many of the (possibly much larger) result set are returned; the total
// match count is returned separately so callers can tell when results
// were truncated.
//
// Coverage is filings filed 2001-01-01 onward only -- SEC has not
// backfilled full-text indexing for older filings.
func (c *Client) SearchFullText(query, forms, ciks, startDate, endDate string, limit int) ([]FullTextHit, int, error) {
	params := url.Values{}
	params.Set("q", query)
	if forms != "" {
		params.Set("forms", forms)
	}
	if ciks != "" {
		params.Set("ciks", ciks)
	}
	if startDate != "" {
		params.Set("startdt", startDate)
	}
	if endDate != "" {
		params.Set("enddt", endDate)
	}

	body, err := c.get(c.FullTextSearchURL + "?" + params.Encode())
	if err != nil {
		return nil, 0, err
	}

	var resp fullTextSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, newClientError("parsing full-text search results: %v", err)
	}

	hits := make([]FullTextHit, 0, min(limit, len(resp.Hits.Hits)))
	for _, h := range resp.Hits.Hits {
		if len(hits) >= limit {
			break
		}
		accession, doc, _ := strings.Cut(h.ID, ":")
		hits = append(hits, FullTextHit{
			AccessionNumber: accession,
			DocumentFile:    doc,
			Form:            h.Source.Form,
			FiledDate:       h.Source.FileDate,
			CIKs:            h.Source.CIKs,
			DisplayNames:    h.Source.DisplayNames,
		})
	}
	return hits, resp.Hits.Total.Value, nil
}
