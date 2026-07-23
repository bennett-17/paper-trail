// Package unsc provides a client for the United Nations Security
// Council's Consolidated Sanctions List -- confirmed live to be
// published as a free, keyless, publicly accessible bulk XML file
// (no registration or approval process found), updated on the UN's
// own side roughly daily. Unlike every other sanctions/screening
// source in this project (US, UK, ICIJ), the UN publishes no live
// per-query search API -- only this single ~2MB consolidated file
// covering both individuals and entities -- so this package fetches
// and parses the whole list once, caching it for the lifetime of the
// Client, and leaves per-name matching to the caller (see
// cmd/paper-trail's screenUNSanctions).
package unsc

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultURL is the Consolidated List's live XML endpoint. Overridable
// on Client for testing against a local httptest server.
const DefaultURL = "https://scsanctions.un.org/resources/xml/en/consolidated.xml"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client fetches the UN Security Council Consolidated Sanctions List.
type Client struct {
	HTTPClient *http.Client
	URL        string
	UserAgent  string

	once         sync.Once
	loadErr      error
	designations []Designation
}

// NewClient builds a Client. No API key is needed or accepted.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		URL:        DefaultURL,
		UserAgent:  "paper-trail (https://github.com/bennett-17/paper-trail)",
	}
}

// Designation is one individual or entity on the Consolidated List.
type Designation struct {
	Name            string   // primary listed name
	Aliases         []string // a.k.a./f.k.a. names, if any
	ListType        string   // the sanctions committee/regime, e.g. "DRC", "Taliban", "Al-Qaida", "Iran"
	ReferenceNumber string
	IsEntity        bool // false for an individual
}

type consolidatedListXML struct {
	Individuals struct {
		Individual []struct {
			FirstName       string `xml:"FIRST_NAME"`
			SecondName      string `xml:"SECOND_NAME"`
			ThirdName       string `xml:"THIRD_NAME"`
			FourthName      string `xml:"FOURTH_NAME"`
			UNListType      string `xml:"UN_LIST_TYPE"`
			ReferenceNumber string `xml:"REFERENCE_NUMBER"`
			Alias           []struct {
				AliasName string `xml:"ALIAS_NAME"`
			} `xml:"INDIVIDUAL_ALIAS"`
		} `xml:"INDIVIDUAL"`
	} `xml:"INDIVIDUALS"`
	Entities struct {
		Entity []struct {
			FirstName       string `xml:"FIRST_NAME"`
			UNListType      string `xml:"UN_LIST_TYPE"`
			ReferenceNumber string `xml:"REFERENCE_NUMBER"`
			Alias           []struct {
				AliasName string `xml:"ALIAS_NAME"`
			} `xml:"ENTITY_ALIAS"`
		} `xml:"ENTITY"`
	} `xml:"ENTITIES"`
}

func (c *Client) load() {
	c.once.Do(func() {
		req, reqErr := http.NewRequest(http.MethodGet, c.URL, nil)
		if reqErr != nil {
			c.loadErr = newClientError("building request for %s: %v", c.URL, reqErr)
			return
		}
		req.Header.Set("User-Agent", c.UserAgent)

		resp, doErr := c.HTTPClient.Do(req)
		if doErr != nil {
			c.loadErr = newClientError("request to %s failed: %v", c.URL, doErr)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			c.loadErr = newClientError("UN Security Council Consolidated List returned HTTP %d for %s", resp.StatusCode, c.URL)
			return
		}

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			c.loadErr = newClientError("reading response from %s: %v", c.URL, readErr)
			return
		}

		var parsed consolidatedListXML
		if err := xml.Unmarshal(body, &parsed); err != nil {
			c.loadErr = newClientError("parsing Consolidated List XML: %v", err)
			return
		}

		designations := make([]Designation, 0, len(parsed.Individuals.Individual)+len(parsed.Entities.Entity))
		for _, ind := range parsed.Individuals.Individual {
			nameParts := make([]string, 0, 4)
			for _, p := range []string{ind.FirstName, ind.SecondName, ind.ThirdName, ind.FourthName} {
				if p = strings.TrimSpace(p); p != "" {
					nameParts = append(nameParts, p)
				}
			}
			var aliases []string
			for _, a := range ind.Alias {
				if name := strings.TrimSpace(a.AliasName); name != "" {
					aliases = append(aliases, name)
				}
			}
			designations = append(designations, Designation{
				Name:            strings.Join(nameParts, " "),
				Aliases:         aliases,
				ListType:        ind.UNListType,
				ReferenceNumber: ind.ReferenceNumber,
				IsEntity:        false,
			})
		}
		for _, ent := range parsed.Entities.Entity {
			var aliases []string
			for _, a := range ent.Alias {
				if name := strings.TrimSpace(a.AliasName); name != "" {
					aliases = append(aliases, name)
				}
			}
			designations = append(designations, Designation{
				Name:            strings.TrimSpace(ent.FirstName),
				Aliases:         aliases,
				ListType:        ent.UNListType,
				ReferenceNumber: ent.ReferenceNumber,
				IsEntity:        true,
			})
		}
		c.designations = designations
	})
}

// List returns every designation on the Consolidated List. The list
// is fetched and parsed on the first call and cached for the lifetime
// of this Client -- the UN only refreshes it roughly once a day on
// their side, so there's no benefit to re-fetching the ~2MB file for
// every name checked in a single scan, and concurrent callers sharing
// one Client (safe via sync.Once) only pay for one fetch between them.
func (c *Client) List() ([]Designation, error) {
	c.load()
	if c.loadErr != nil {
		return nil, c.loadErr
	}
	return c.designations, nil
}
