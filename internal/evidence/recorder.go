// Package evidence records the raw HTTP responses behind a scan into a
// dated, self-describing bundle on disk -- so a claim made from a
// report has a receipt showing what the register actually said on the
// day it was queried.
//
// This exists because public registers mutate. Companies dissolve,
// officers resign, PSC records get amended, and sanctions designations
// are added and lifted. A report generated today can become
// unreproducible tomorrow through no fault of anyone's, which is a
// problem if the report is the basis of something published: the
// obvious challenge to any finding is "the register doesn't say that",
// and without a contemporaneous capture there's no answer to it.
//
// Deliberately opt-in (--evidence-dir): recording every response costs
// disk and, more importantly, writes third-party data to a file the
// user then becomes responsible for. That should be a choice, never a
// default.
package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// redactedValue replaces any credential found in a captured URL. The
// bundle is a file the user may hand to an editor, a lawyer, or a
// court; it must never carry their API keys.
const redactedValue = "REDACTED"

// secretParamRE matches query-parameter names that carry credentials
// across the APIs this project talks to -- SAM.gov and the US
// Consolidated Screening List both put the key in the query string
// (api_key), and the pattern is written broadly rather than as an
// exact allowlist so a future source that uses "token" or
// "subscription-key" is redacted by default rather than leaking until
// someone remembers to add it.
//
// Erring toward over-redaction is the right failure direction here: a
// redacted parameter that wasn't secret costs a reader nothing, while
// the reverse writes a live credential to disk.
var secretParamRE = regexp.MustCompile(`(?i)(api[-_]?key|apikey|^key$|token|secret|password|subscription[-_]?key|access[-_]?token)`)

// unsafeFilenameRE reduces a URL to something safe to use as a
// filename on any filesystem.
var unsafeFilenameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Entry is one recorded response's manifest row.
type Entry struct {
	Seq         int    `json:"seq"`
	RecordedAt  string `json:"recordedAt"` // RFC3339, when the response was received
	Method      string `json:"method"`
	URL         string `json:"url"` // credentials redacted -- see secretParamRE
	Status      int    `json:"status"`
	File        string `json:"file"`   // path relative to the bundle directory
	Bytes       int    `json:"bytes"`  // response body length as recorded
	SHA256      string `json:"sha256"` // of the recorded body, so tampering is detectable
	ContentType string `json:"contentType,omitempty"`
}

// Manifest describes a whole bundle.
type Manifest struct {
	Tool      string   `json:"tool"`
	StartedAt string   `json:"startedAt"`
	EndedAt   string   `json:"endedAt"`
	Queries   []string `json:"queries,omitempty"`
	Note      string   `json:"note"`
	Entries   []Entry  `json:"entries"`
}

// Recorder captures responses into a directory. Safe for concurrent
// use: a scan runs many sources and query terms in parallel, so every
// capture goes through one mutex.
type Recorder struct {
	dir     string
	started time.Time
	queries []string

	mu      sync.Mutex
	seq     int
	entries []Entry
}

// NewRecorder creates (or reuses) dir and prepares it for capture.
func NewRecorder(dir string, queries []string) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Join(dir, "responses"), 0o755); err != nil {
		return nil, fmt.Errorf("creating evidence directory: %w", err)
	}
	return &Recorder{dir: dir, started: time.Now(), queries: queries}, nil
}

// Dir reports where the bundle is being written.
func (r *Recorder) Dir() string { return r.dir }

// Count reports how many responses have been captured so far.
func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// RedactURL strips credentials out of a URL for recording. Exported
// for testing, since getting this wrong writes live API keys to disk.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable: return something obviously useless rather than
		// the raw string, which might still contain a key.
		return redactedValue
	}
	if u.User != nil {
		// Companies House-style basic auth embedded in the URL.
		u.User = url.User(redactedValue)
	}
	q := u.Query()
	for name := range q {
		if secretParamRE.MatchString(name) {
			q.Set(name, redactedValue)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// record writes one response body and appends its manifest row.
func (r *Recorder) record(req *http.Request, resp *http.Response, body []byte) {
	r.mu.Lock()
	r.seq++
	seq := r.seq
	r.mu.Unlock()

	safe := unsafeFilenameRE.ReplaceAllString(req.URL.Host+req.URL.Path, "-")
	safe = strings.Trim(safe, "-")
	if len(safe) > 80 {
		safe = safe[:80]
	}
	name := fmt.Sprintf("%04d-%s", seq, safe)
	if ext := extensionFor(resp.Header.Get("Content-Type")); ext != "" {
		name += ext
	}
	rel := filepath.Join("responses", name)

	// A failed write must never break the scan that triggered it --
	// evidence capture is an addition to the run, not a precondition
	// for it. The manifest still records the attempt.
	if err := os.WriteFile(filepath.Join(r.dir, rel), body, 0o644); err != nil {
		rel = ""
	}

	sum := sha256.Sum256(body)
	entry := Entry{
		Seq:         seq,
		RecordedAt:  time.Now().Format(time.RFC3339),
		Method:      req.Method,
		URL:         RedactURL(req.URL.String()),
		Status:      resp.StatusCode,
		File:        rel,
		Bytes:       len(body),
		SHA256:      hex.EncodeToString(sum[:]),
		ContentType: resp.Header.Get("Content-Type"),
	}

	r.mu.Lock()
	r.entries = append(r.entries, entry)
	r.mu.Unlock()
}

func extensionFor(contentType string) string {
	switch {
	case strings.Contains(contentType, "json"):
		return ".json"
	case strings.Contains(contentType, "xml"):
		return ".xml"
	case strings.Contains(contentType, "html"):
		return ".html"
	case strings.Contains(contentType, "text"):
		return ".txt"
	default:
		return ""
	}
}

// Save writes the manifest. Call once, after the scan finishes.
func (r *Recorder) Save() error {
	r.mu.Lock()
	entries := make([]Entry, len(r.entries))
	copy(entries, r.entries)
	r.mu.Unlock()

	m := Manifest{
		Tool:      "paper-trail",
		StartedAt: r.started.Format(time.RFC3339),
		EndedAt:   time.Now().Format(time.RFC3339),
		Queries:   r.queries,
		Note: "Raw HTTP responses captured during one paper-trail scan, as returned by each " +
			"source at the time shown. Credentials are redacted from recorded URLs. Each entry's " +
			"sha256 is of the stored file, so any later alteration is detectable. Public registers " +
			"change over time: this bundle is a contemporaneous record of what they said, not a " +
			"claim that they still say it.",
		Entries: entries,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding evidence manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(r.dir, "manifest.json"), data, 0o644)
}

// Transport wraps base so every response passing through it is
// recorded. A nil base uses http.DefaultTransport.
func (r *Recorder) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &capturingTransport{base: base, rec: r}
}

type capturingTransport struct {
	base http.RoundTripper
	rec  *Recorder
}

// RoundTrip records the response body, then hands the caller an
// identical, unread copy -- clients downstream must see exactly what
// they would have seen without recording enabled.
func (t *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		// Hand back an error body rather than a silently truncated one.
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp, readErr
	}
	t.rec.record(req, resp, body)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}
