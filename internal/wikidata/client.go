// Package wikidata provides a minimal client for Wikidata's public,
// keyless action API (wbsearchentities and wbgetentities) -- used to
// screen person names against Wikidata's own "politician" occupation
// tag, a Politically Exposed Person (PEP) screen this project didn't
// have before.
//
// Confirmed live: matching purely by SPARQL "rdfs:label \"Name\"@en"
// (an obvious first approach) silently misses a well-known real
// example -- Angela Merkel's own canonical Wikidata item (Q567) has no
// English-tagged label at all; her name is instead stored under the
// "mul" (language-independent) tag, a genuine, current Wikidata
// modeling detail for names that don't vary by language. wbsearchentities
// resolves this correctly (it's Wikidata's own entity-linking
// autocomplete, used by their editing UI), so this package uses that
// instead of raw SPARQL.
package wikidata

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultAPIURL is Wikidata's action API endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultAPIURL = "https://www.wikidata.org/w/api.php"

// PoliticianOccupationQID is Wikidata's item for the occupation
// "politician" -- confirmed live on a real example (Angela Merkel,
// Q567, occupation P106 includes this).
const PoliticianOccupationQID = "Q82955"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to Wikidata's public action API. No API key is needed
// or accepted.
type Client struct {
	HTTPClient *http.Client
	// MinInterval is a courteous default, not a documented hard limit
	// the way GDELT's or Companies House's are -- Wikidata's own
	// etiquette guidance asks for a descriptive User-Agent and
	// reasonable pacing, not a specific number.
	MinInterval time.Duration
	UserAgent   string
	APIURL      string

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client.
func NewClient() *Client {
	return &Client{
		HTTPClient:  &http.Client{Timeout: 15 * time.Second},
		MinInterval: 200 * time.Millisecond,
		UserAgent:   "paper-trail (https://github.com/bennett-17/paper-trail)",
		APIURL:      DefaultAPIURL,
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newClientError("Wikidata API returned HTTP %d for %s", resp.StatusCode, u)
	}
	return body, nil
}

// PersonCandidate is one entity Wikidata's search returned for a name
// query -- not necessarily a politician, or even a human at all (a
// common name can match a company, book, or other named entity too);
// callers filter further (see Occupations).
type PersonCandidate struct {
	QID         string
	Label       string
	Description string
}

type searchResponse struct {
	Search []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"search"`
}

// SearchPeople searches Wikidata by name (wbsearchentities) and
// returns every candidate entity found, most-relevant first (Wikidata's
// own ranking). limit caps how many come back (0 uses Wikidata's own
// default page size). Confirmed live that a name with no match at all
// returns a valid empty "search" array, not an error.
func (c *Client) SearchPeople(name string, limit int) ([]PersonCandidate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("action", "wbsearchentities")
	params.Set("search", name)
	params.Set("language", "en")
	params.Set("type", "item")
	params.Set("format", "json")
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	body, err := c.get(c.APIURL + "?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing Wikidata search results: %v", err)
	}
	candidates := make([]PersonCandidate, 0, len(resp.Search))
	for _, s := range resp.Search {
		candidates = append(candidates, PersonCandidate{QID: s.ID, Label: s.Label, Description: s.Description})
	}
	return candidates, nil
}

// getEntitiesResponse leaves each entity's claims as raw JSON rather
// than a fixed struct -- confirmed live that a real entity's claims map
// mixes many different Wikidata value types across its properties
// (strings, quantities, dates, coordinates, entity references...), so
// a single fixed shape applied to the whole map fails to unmarshal the
// moment any *other* property (not P106) uses a different value type.
// Only the P106 (occupation) entry actually needs decoding here.
type getEntitiesResponse struct {
	Entities map[string]struct {
		Claims map[string]json.RawMessage `json:"claims"`
	} `json:"entities"`
}

type occupationClaims []struct {
	MainSnak struct {
		SnakType  string `json:"snaktype"`
		DataValue struct {
			Value struct {
				ID string `json:"id"`
			} `json:"value"`
		} `json:"datavalue"`
	} `json:"mainsnak"`
}

// Occupations batch-fetches occupation (P106) values for every given
// QID in a single request (wbgetentities accepts a "|"-joined ids
// list), returning a map from QID to its occupation QIDs. A QID with
// no P106 claims at all (not a human, or a human Wikidata has no
// occupation data for) comes back with an empty slice, not an error --
// confirmed live against a real non-politician homonym (an unrelated
// "Angela Merkel" board member, Q94746073, with zero P106 claims).
// Returns nil for an empty qids.
func (c *Client) Occupations(qids []string) (map[string][]string, error) {
	if len(qids) == 0 {
		return nil, nil
	}

	params := url.Values{}
	params.Set("action", "wbgetentities")
	params.Set("ids", strings.Join(qids, "|"))
	params.Set("props", "claims")
	params.Set("format", "json")

	body, err := c.get(c.APIURL + "?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp getEntitiesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing Wikidata entities: %v", err)
	}
	out := make(map[string][]string, len(resp.Entities))
	for qid, ent := range resp.Entities {
		var occupations []string
		if raw, ok := ent.Claims["P106"]; ok {
			var claims occupationClaims
			if err := json.Unmarshal(raw, &claims); err != nil {
				return nil, newClientError("parsing %s occupation (P106) claims: %v", qid, err)
			}
			for _, claim := range claims {
				if claim.MainSnak.SnakType != "value" {
					continue // "somevalue"/"novalue" -- no datavalue at all
				}
				if id := claim.MainSnak.DataValue.Value.ID; id != "" {
					occupations = append(occupations, id)
				}
			}
		}
		out[qid] = occupations
	}
	return out, nil
}
