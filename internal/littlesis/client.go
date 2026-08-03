// Package littlesis provides a client for LittleSis
// (https://littlesis.org), a free, keyless, crowdsourced database of
// "who-knows-who" among powerful people and organizations (its own
// tagline: "a free database of who-knows-who at the heights of
// business and government"). Confirmed live that its search and
// relationships endpoints require no API key or registration.
//
// Unlike every corporate-registry source in this project, LittleSis
// isn't a government record -- entries are community-curated, sourced
// from news reporting, SEC filings, and other public records, so a
// match here is a documented allegation/connection worth checking
// against its cited sources, not an official filing the way a
// Companies House or EDGAR result is. Its distinguishing value is
// relationship data most registries don't expose at all: board
// memberships, executive positions, and family/business ties between
// named people and organizations, hand-curated specifically to surface
// the kind of influence network this project's shared_person
// cross-referencing is built to find.
package littlesis

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

// DefaultBaseURL is LittleSis's live API endpoint. Overridable on
// Client for testing against a local httptest server.
const DefaultBaseURL = "https://littlesis.org/api"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the LittleSis API.
type Client struct {
	HTTPClient *http.Client
	// MinInterval throttles requests even though LittleSis publishes no
	// documented rate limit -- errs conservative out of politeness,
	// same reasoning as internal/gleif and internal/ofsi.
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
		HTTPClient:     &http.Client{Timeout: 15 * time.Second},
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

func (c *Client) get(u string) ([]byte, error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, body, err := c.rawGet(u)
		if err != nil {
			return nil, err
		}
		if status == http.StatusTooManyRequests && attempt < c.MaxRetries {
			time.Sleep(delay)
			delay *= 2
			continue
		}
		if status < 200 || status >= 300 {
			return nil, newClientError("LittleSis API returned HTTP %d for %s", status, u)
		}
		return body, nil
	}
}

func (c *Client) rawGet(u string) (int, []byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("Accept", "application/json")
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

// Entity is one LittleSis person or organization record.
type Entity struct {
	ID         int
	Name       string
	Blurb      string
	Website    string
	PrimaryExt string // "Org" or "Person"
	Types      []string
	SECCIK     int    // 0 if this entity has no reported SEC CIK (PublicCompany extension)
	URL        string // e.g. "https://littlesis.org/org/41-Wells_Fargo_&_Company"
}

// IsOrg reports whether this entity is an organization, not a person.
func (e Entity) IsOrg() bool { return e.PrimaryExt == "Org" }

type publicCompanyExtension struct {
	SECCIK int `json:"sec_cik"`
}

type entityAttributes struct {
	ID         int                        `json:"id"`
	Name       string                     `json:"name"`
	Blurb      string                     `json:"blurb"`
	Website    string                     `json:"website"`
	PrimaryExt string                     `json:"primary_ext"`
	Types      []string                   `json:"types"`
	Extensions map[string]json.RawMessage `json:"extensions"`
}

type entityData struct {
	// LittleSis's own top-level "id" is a bare JSON number here (unlike
	// the JSON:API spec's string convention) -- deliberately not
	// parsed into a field at all, since Attributes.ID (also present,
	// and reliably an int) is what toEntity actually uses.
	Attributes entityAttributes `json:"attributes"`
	Links      struct {
		Self string `json:"self"`
	} `json:"links"`
}

func (d entityData) toEntity() Entity {
	e := Entity{
		ID:         d.Attributes.ID,
		Name:       d.Attributes.Name,
		Blurb:      d.Attributes.Blurb,
		Website:    d.Attributes.Website,
		PrimaryExt: d.Attributes.PrimaryExt,
		Types:      d.Attributes.Types,
		URL:        d.Links.Self,
	}
	if raw, ok := d.Attributes.Extensions["PublicCompany"]; ok {
		var pc publicCompanyExtension
		if json.Unmarshal(raw, &pc) == nil {
			e.SECCIK = pc.SECCIK
		}
	}
	return e
}

type entityListResponse struct {
	Data []entityData `json:"data"`
}

type entitySingleResponse struct {
	Data entityData `json:"data"`
}

// SearchByName searches LittleSis for people and organizations by
// (partial) name. limit caps how many results come back (0 uses
// LittleSis's own default page size, confirmed live to be 10).
func (c *Client) SearchByName(name string, limit int) ([]Entity, error) {
	params := url.Values{}
	params.Set("q", name)
	body, err := c.get(c.BaseURL + "/entities/search?" + params.Encode())
	if err != nil {
		return nil, err
	}

	var resp entityListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing LittleSis search results: %v", err)
	}
	entities := make([]Entity, 0, len(resp.Data))
	for _, d := range resp.Data {
		entities = append(entities, d.toEntity())
		if limit > 0 && len(entities) >= limit {
			break
		}
	}
	return entities, nil
}

// GetEntity fetches one entity's full record by its numeric ID.
func (c *Client) GetEntity(id int) (Entity, error) {
	body, err := c.get(c.BaseURL + "/entities/" + strconv.Itoa(id))
	if err != nil {
		return Entity{}, err
	}
	var resp entitySingleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Entity{}, newClientError("parsing LittleSis entity %d: %v", id, err)
	}
	return resp.Data.toEntity(), nil
}

// Relationship is one documented connection between two LittleSis
// entities, oriented from the entity the caller asked Relationships
// for (see OtherEntityID/OtherEntityName below) -- the counterparty on
// the other end.
type Relationship struct {
	ID              int
	OtherEntityID   int
	OtherEntityName string
	OtherEntityURL  string
	// IsBoard/IsExecutive reflect LittleSis's own category_attributes
	// for a "Position" relationship -- a plain (non-officer,
	// non-board) employee relationship leaves both false.
	IsBoard     bool
	IsExecutive bool
	IsCurrent   bool
	Description string
}

type relationshipAttributes struct {
	ID            int    `json:"id"`
	Entity1ID     int    `json:"entity1_id"`
	Entity2ID     int    `json:"entity2_id"`
	IsCurrent     bool   `json:"is_current"`
	Description   string `json:"description"`
	CategoryAttrs struct {
		IsBoard     *bool `json:"is_board"`
		IsExecutive *bool `json:"is_executive"`
	} `json:"category_attributes"`
}

type relationshipData struct {
	Attributes relationshipAttributes `json:"attributes"`
	// Entity/Related are confirmed live to sit directly on the
	// relationship object itself, not nested under a "links" key the
	// way an entity search/detail result's own self-link is.
	Entity  string `json:"entity"`  // entity1's LittleSis page
	Related string `json:"related"` // entity2's LittleSis page
}

type relationshipListResponse struct {
	Data []relationshipData `json:"data"`
}

// entityNameFromURL recovers a human-readable name from a LittleSis
// entity page URL, e.g. "https://littlesis.org/person/459957-John_W._Morrison"
// -> "John W. Morrison". LittleSis's relationships endpoint gives the
// counterparty's identity only as this URL (plus a bare numeric ID),
// not a name field -- confirmed live this slug format
// ("<id>-<url-encoded, underscore-for-space name>") is stable across
// both person and org pages.
func entityNameFromURL(u string) string {
	slug := u
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		slug = slug[i+1:]
	}
	if i := strings.Index(slug, "-"); i >= 0 {
		slug = slug[i+1:]
	}
	slug = strings.ReplaceAll(slug, "_", " ")
	if decoded, err := url.QueryUnescape(slug); err == nil {
		slug = decoded
	}
	return strings.TrimSpace(slug)
}

// Relationships fetches every documented relationship for entityID,
// oriented so OtherEntityID/OtherEntityName always describe the
// counterparty, regardless of which side of LittleSis's own
// entity1/entity2 pairing entityID happens to fall on. limit caps how
// many relationships come back (0 uses LittleSis's own default page
// size).
func (c *Client) Relationships(entityID int, limit int) ([]Relationship, error) {
	body, err := c.get(c.BaseURL + "/entities/" + strconv.Itoa(entityID) + "/relationships")
	if err != nil {
		return nil, err
	}

	var resp relationshipListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, newClientError("parsing LittleSis relationships for entity %d: %v", entityID, err)
	}

	rels := make([]Relationship, 0, len(resp.Data))
	for _, d := range resp.Data {
		a := d.Attributes
		r := Relationship{
			ID:          a.ID,
			IsCurrent:   a.IsCurrent,
			Description: a.Description,
		}
		if a.CategoryAttrs.IsBoard != nil {
			r.IsBoard = *a.CategoryAttrs.IsBoard
		}
		if a.CategoryAttrs.IsExecutive != nil {
			r.IsExecutive = *a.CategoryAttrs.IsExecutive
		}
		if a.Entity1ID == entityID {
			r.OtherEntityID = a.Entity2ID
			r.OtherEntityURL = d.Related
		} else {
			r.OtherEntityID = a.Entity1ID
			r.OtherEntityURL = d.Entity
		}
		r.OtherEntityName = entityNameFromURL(r.OtherEntityURL)
		rels = append(rels, r)
		if limit > 0 && len(rels) >= limit {
			break
		}
	}
	return rels, nil
}
