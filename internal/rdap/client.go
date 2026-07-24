// Package rdap provides a client for the free, keyless RDAP protocol
// (RFC 7482/7483) -- the IETF-standardized successor to WHOIS -- used
// here to look up a domain's registration date. Confirmed live against
// two real domains on two different registries (google.com via
// Verisign's own RDAP server, bbc.co.uk via Nominet's) that every RDAP
// domain record exposes its registration date as an "events" entry
// with eventAction "registration".
//
// Which registry to query for a given domain is resolved via IANA's
// own public RDAP bootstrap file (confirmed live: a ~590-entry JSON
// map from top-level domain to that TLD's RDAP base URL), fetched once
// and cached for the lifetime of the Client, per IANA's own published
// guidance not to fetch it per-query.
package rdap

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultBootstrapURL is IANA's own RDAP bootstrap registry for DNS.
const DefaultBootstrapURL = "https://data.iana.org/rdap/dns.json"

// domainLookupMaxLabelsStripped bounds how many leading labels
// RegistrationDate will strip and retry -- confirmed live that RDAP
// expects the actual registered domain, not an arbitrary subdomain
// (querying "www.google.com" 404s where "google.com" succeeds), and
// this project has no public-suffix-list dependency to determine the
// exact registrable domain for every TLD's structure up front. Capped
// rather than stripped all the way to the bare TLD, so a long,
// deeply-nested hostname doesn't turn into an unbounded number of
// requests.
const domainLookupMaxLabelsStripped = 2

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to the RDAP bootstrap registry and per-TLD RDAP
// servers. No API key is needed or accepted.
type Client struct {
	HTTPClient *http.Client
	// MinInterval is a courteous default, not a documented hard limit
	// -- no per-registry rate limit was found published, but every
	// other client in this project self-throttles as good practice.
	MinInterval  time.Duration
	UserAgent    string
	BootstrapURL string

	mu            sync.Mutex
	lastRequestAt time.Time

	bootstrapOnce sync.Once
	bootstrapErr  error
	bootstrap     map[string][]string // TLD label -> RDAP base URLs, most-preferred first
}

// NewClient builds a Client.
func NewClient() *Client {
	return &Client{
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
		MinInterval:  200 * time.Millisecond,
		UserAgent:    "paper-trail (https://github.com/bennett-17/paper-trail)",
		BootstrapURL: DefaultBootstrapURL,
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

// get returns the response body and status code -- unlike every other
// client in this project, callers here need the status code itself
// (404 is a normal, meaningful "not found at this registry", not an
// error to report), not just success-or-error.
func (c *Client) get(u string) (status int, body []byte, err error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/rdap+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, newClientError("request to %s failed: %v", u, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, newClientError("reading response from %s: %v", u, err)
	}
	return resp.StatusCode, respBody, nil
}

type bootstrapFile struct {
	Services [][]any `json:"services"`
}

// loadBootstrap fetches and parses IANA's RDAP bootstrap file exactly
// once, per Client (lazy, on first use) -- same sync.Once-cached-once
// pattern as internal/unsc's whole-list fetch, since IANA's own
// guidance is to cache this rather than fetch it per query.
func (c *Client) loadBootstrap() error {
	c.bootstrapOnce.Do(func() {
		status, body, err := c.get(c.BootstrapURL)
		if err != nil {
			c.bootstrapErr = err
			return
		}
		if status < 200 || status >= 300 {
			c.bootstrapErr = newClientError("RDAP bootstrap registry returned HTTP %d", status)
			return
		}
		var f bootstrapFile
		if err := json.Unmarshal(body, &f); err != nil {
			c.bootstrapErr = newClientError("parsing RDAP bootstrap registry: %v", err)
			return
		}
		bootstrap := make(map[string][]string, len(f.Services))
		for _, entry := range f.Services {
			if len(entry) != 2 {
				continue
			}
			tlds, ok := entry[0].([]any)
			if !ok {
				continue
			}
			urlsRaw, ok := entry[1].([]any)
			if !ok {
				continue
			}
			var urls []string
			for _, u := range urlsRaw {
				if s, ok := u.(string); ok {
					urls = append(urls, s)
				}
			}
			if len(urls) == 0 {
				continue
			}
			for _, t := range tlds {
				if tld, ok := t.(string); ok {
					bootstrap[strings.ToLower(tld)] = urls
				}
			}
		}
		c.bootstrap = bootstrap
	})
	return c.bootstrapErr
}

// registryBaseURL returns the RDAP base URL for the given domain's
// top-level domain, per the bootstrap registry.
func (c *Client) registryBaseURL(domain string) (string, error) {
	if err := c.loadBootstrap(); err != nil {
		return "", err
	}
	labels := strings.Split(strings.ToLower(domain), ".")
	tld := labels[len(labels)-1]
	urls, ok := c.bootstrap[tld]
	if !ok || len(urls) == 0 {
		return "", newClientError("no RDAP registry found for top-level domain %q", tld)
	}
	return strings.TrimSuffix(urls[0], "/"), nil
}

type domainResponse struct {
	Events []struct {
		EventAction string `json:"eventAction"`
		EventDate   string `json:"eventDate"`
	} `json:"events"`
}

// RegistrationDate looks up a domain's registration date via RDAP.
// domain should be a bare host (no scheme, no path), e.g. "example.com"
// or "example.co.uk". Confirmed live that RDAP 404s for an arbitrary
// subdomain rather than the actual registered domain (see
// domainLookupMaxLabelsStripped) -- so this tries the full host first,
// then progressively strips one leading label at a time until a lookup
// succeeds or the attempt cap is reached. Returns an error if the
// domain has no RDAP record at all (nothing found after every attempt)
// or its record has no registration event.
func (c *Client) RegistrationDate(domain string) (time.Time, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return time.Time{}, newClientError("empty domain")
	}

	base, err := c.registryBaseURL(domain)
	if err != nil {
		return time.Time{}, err
	}

	labels := strings.Split(domain, ".")
	var lastErr error
	for attempt := 0; attempt <= domainLookupMaxLabelsStripped && attempt < len(labels)-1; attempt++ {
		candidate := strings.Join(labels[attempt:], ".")
		status, body, err := c.get(base + "/domain/" + candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if status == http.StatusNotFound {
			lastErr = newClientError("no RDAP record found for %s", candidate)
			continue
		}
		if status < 200 || status >= 300 {
			lastErr = newClientError("RDAP registry returned HTTP %d for %s", status, candidate)
			continue
		}

		var resp domainResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return time.Time{}, newClientError("parsing RDAP domain record for %s: %v", candidate, err)
		}
		for _, e := range resp.Events {
			if e.EventAction != "registration" {
				continue
			}
			t, err := time.Parse(time.RFC3339, e.EventDate)
			if err != nil {
				return time.Time{}, newClientError("parsing registration date %q for %s: %v", e.EventDate, candidate, err)
			}
			return t, nil
		}
		return time.Time{}, newClientError("%s has an RDAP record but no registration event", candidate)
	}
	return time.Time{}, lastErr
}
