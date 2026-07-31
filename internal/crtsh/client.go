// Package crtsh provides a client for crt.sh's free, keyless
// Certificate Transparency (CT) log search API. Since ~2018 every
// publicly-trusted certificate authority must submit every certificate
// it issues to public, append-only CT logs (RFC 6962) or major
// browsers won't trust it -- crt.sh (operated by Sectigo) indexes all
// of them, so a query here returns every certificate ever issued for a
// domain, regardless of which CA issued it.
//
// Confirmed live: a query returns one JSON row per (certificate, CT
// log) pair, not one row per certificate -- the same certificate can
// be logged in several different CT logs (a redundancy requirement,
// not a duplicate issuance), so multiple rows can share the same
// issuer_ca_id/serial_number. Certificates() collapses those into one
// Certificate per distinct issuer+serial pair. Also confirmed live: a
// certificate covering multiple names (Subject Alternative Names, e.g.
// "*.example.com" and "example.com" on the same cert) has all of them
// packed into one row's name_value field, newline-separated, rather
// than one row per name.
//
// This is what makes crt.sh useful for this project beyond ordinary
// subdomain enumeration: if a certificate's SAN list covers two
// genuinely different registrable domains -- not just subdomains of
// the same one -- those two domains' operators share the same TLS
// certificate, a real technical infrastructure link a shell-company
// network sharing an operator or hosting setup can leave behind even
// when nothing else (address, officer, phone) visibly overlaps.
package crtsh

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is crt.sh's search host. Overridable on Client for
// testing against a local httptest server.
const DefaultBaseURL = "https://crt.sh"

// ClientError wraps errors raised by this package.
type ClientError struct{ msg string }

func (e *ClientError) Error() string { return e.msg }

func newClientError(format string, args ...any) error {
	return &ClientError{msg: fmt.Sprintf(format, args...)}
}

// Client talks to crt.sh. No API key is needed or accepted.
type Client struct {
	HTTPClient *http.Client
	// MinInterval is a courteous default, not a documented hard limit
	// -- crt.sh publishes no rate-limit policy.
	MinInterval time.Duration
	UserAgent   string
	BaseURL     string

	// MaxRetries/RetryBaseDelay govern retry-with-backoff. Confirmed
	// live that this free service is genuinely flaky under ordinary
	// use: a transient HTTP 502 (reproduced twice in immediate
	// succession, gone on the next attempt seconds later), a transient
	// HTTP 404 for a domain that had returned real results seconds
	// earlier and did again moments after (ruled out as a legitimate
	// "no results" response -- that shape is a 200 with an empty JSON
	// array "[]", confirmed live, never a 404), and, separately, a
	// request that outright times out for a real, valid domain
	// (oxfam.org.au, live-verified against a real risk scan) -- the
	// same category of real-world flakiness internal/wayback already
	// retries around, so this retries 404/502/503/timeouts rather than
	// assuming a single status code the way most other clients in this
	// project do. HTTPClient.Timeout is deliberately shorter than this
	// project's other clients (which mostly use 15-20s) specifically
	// because of that last case: a plain timeout is retried up to
	// MaxRetries times, so a 30s-per-attempt timeout meant a single
	// unresponsive query could cost up to two minutes before giving
	// up, confirmed live to dominate an otherwise-fast scan's total
	// wall-clock time. A shorter per-attempt timeout fails faster
	// without giving up sooner on the retry budget itself.
	MaxRetries     int
	RetryBaseDelay time.Duration

	mu            sync.Mutex
	lastRequestAt time.Time
}

// NewClient builds a Client.
func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 12 * time.Second},
		MinInterval:    500 * time.Millisecond,
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

func (c *Client) get(u string) (status int, body []byte, err error) {
	delay := c.RetryBaseDelay
	for attempt := 0; ; attempt++ {
		status, body, err = c.rawGet(u)
		retryable := err != nil || status == http.StatusNotFound || status == http.StatusBadGateway || status == http.StatusServiceUnavailable
		if !retryable || attempt >= c.MaxRetries {
			return status, body, err
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func (c *Client) rawGet(u string) (int, []byte, error) {
	c.throttle()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, newClientError("building request for %s: %v", u, err)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

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

// Certificate is one distinct certificate crt.sh found for a domain
// query -- distinct meaning one issuer+serial-number pair, already
// collapsed across every CT log it happened to be submitted to (see
// the package doc comment). SANs includes every name the certificate
// covers (the queried domain and every other name on the same
// certificate, lowercased, deduplicated), not just the one that
// matched the search.
type Certificate struct {
	IssuerName   string
	CommonName   string
	SANs         []string
	NotBefore    time.Time
	NotAfter     time.Time
	SerialNumber string
	// Key uniquely identifies this certificate (issuer CA ID + serial
	// number) -- stable across repeated queries, unlike the crt.sh "id"
	// field, which identifies one CT log entry, not the certificate
	// itself (see package doc comment).
	Key string
}

type crtshRow struct {
	IssuerCAID   int    `json:"issuer_ca_id"`
	IssuerName   string `json:"issuer_name"`
	CommonName   string `json:"common_name"`
	NameValue    string `json:"name_value"`
	NotBefore    string `json:"not_before"`
	NotAfter     string `json:"not_after"`
	SerialNumber string `json:"serial_number"`
}

// crtshTimeLayout matches crt.sh's not_before/not_after format,
// confirmed live (e.g. "2026-07-29T21:22:47") -- no timezone suffix
// and no fractional seconds, unlike its entry_timestamp field (which
// this package doesn't use).
const crtshTimeLayout = "2006-01-02T15:04:05"

// Certificates searches crt.sh for every certificate ever issued
// covering domain (bare host, no scheme -- e.g. "example.com"), and
// returns the distinct set of them. An unmatched domain returns an
// empty slice and a nil error (confirmed live: crt.sh returns a valid
// empty JSON array "[]" for zero results, not an error status).
func (c *Client) Certificates(domain string) ([]Certificate, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("q", domain)
	params.Set("output", "json")

	status, body, err := c.get(c.BaseURL + "/?" + params.Encode())
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, newClientError("crt.sh returned HTTP %d for %s", status, domain)
	}

	var rows []crtshRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, newClientError("parsing crt.sh response for %s: %v", domain, err)
	}

	byKey := make(map[string]*Certificate)
	var order []string
	for _, row := range rows {
		key := fmt.Sprintf("%d:%s", row.IssuerCAID, row.SerialNumber)
		cert, ok := byKey[key]
		if !ok {
			cert = &Certificate{
				IssuerName:   row.IssuerName,
				CommonName:   row.CommonName,
				SerialNumber: row.SerialNumber,
				Key:          key,
			}
			if t, err := time.Parse(crtshTimeLayout, row.NotBefore); err == nil {
				cert.NotBefore = t
			}
			if t, err := time.Parse(crtshTimeLayout, row.NotAfter); err == nil {
				cert.NotAfter = t
			}
			byKey[key] = cert
			order = append(order, key)
		}
		for _, name := range strings.Split(row.NameValue, "\n") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if !containsString(cert.SANs, name) {
				cert.SANs = append(cert.SANs, name)
			}
		}
	}

	certs := make([]Certificate, 0, len(order))
	for _, key := range order {
		certs = append(certs, *byKey[key])
	}
	return certs, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
