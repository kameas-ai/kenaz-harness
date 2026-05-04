// Package web provides URL fetching and content extraction primitives
// for the workflows subsystem (workflows-agentic-01KW2D3X WP05).
//
// Privacy invariant: log statements record the request hostname ONLY —
// never the full URL (which may contain auth tokens) or response body.
//
// Cedar gate: web_fetch and web_scrape steps emit Cedar action
// "workflow.network.fetch" before dispatching any network request.
// The default_workflows_policy.cedar bundle gates this action.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// ErrBlockedByRobots is returned when the target URL is disallowed by
// the host's robots.txt. Fail-closed: any fetch or parse error on
// robots.txt is treated as a blanket Disallow.
var ErrBlockedByRobots = errors.New("web: URL blocked by robots.txt")

// DefaultUserAgent is the User-Agent header sent when the step does
// not override it. The placeholder version is replaced at link time
// via ldflags or stays as "dev" during development.
var DefaultUserAgent = "Kenaz/dev"

// maxBodyBytes is the hard cap on response bodies (5 MiB).
const maxBodyBytes = 5 << 20

// defaultFetchTimeout is applied when the step's timeout_ms is ≤ 0.
const defaultFetchTimeout = 30 * time.Second

// robotsTTL is how long a parsed robots.txt entry is cached per host.
const robotsTTL = 24 * time.Hour

// FetchResult is the structured output of a Fetch call.
type FetchResult struct {
	// Kind is "html", "json", or "text" based on Content-Type detection.
	Kind string `json:"kind"`
	// Body is the response body. For Kind=="json" it is also decoded
	// into Parsed for downstream use.
	Body string `json:"body"`
	// Parsed holds the decoded JSON value when Kind=="json".
	Parsed any `json:"parsed,omitempty"`
	// Status is the HTTP response status code.
	Status int `json:"status"`
	// Headers holds the first value of each response header.
	Headers map[string]string `json:"headers"`
}

// FetchOptions configures a single Fetch call.
type FetchOptions struct {
	// Timeout overrides the 30s default. ≤ 0 uses the default.
	Timeout time.Duration
	// UserAgent overrides DefaultUserAgent.
	UserAgent string
	// MinInterval is the minimum time between successive requests to
	// the same host. 0 defaults to 1s.
	MinInterval time.Duration
	// SkipRobots bypasses robots.txt enforcement (for unit tests only).
	SkipRobots bool
	// ExtraHeaders are merged with the request headers (last-writer wins).
	ExtraHeaders map[string]string
}

// Fetcher is a reusable URL fetcher that honours robots.txt and a
// per-host rate limiter. It is safe for concurrent use.
type Fetcher struct {
	client *http.Client

	// robots cache
	robotsMu    sync.Mutex
	robotsCache map[string]robotsEntry

	// per-host rate limiter (last request time)
	rlMu  sync.Mutex
	rlMap map[string]time.Time
}

type robotsEntry struct {
	disallowed map[string]bool // normalised path prefixes
	fetchedAt  time.Time
}

// NewFetcher returns a Fetcher with a shared http.Client.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client: &http.Client{
			// Individual request timeouts are set via context.
			Timeout: 0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("web: too many redirects")
				}
				return nil
			},
		},
		robotsCache: make(map[string]robotsEntry),
		rlMap:       make(map[string]time.Time),
	}
}

// Fetch downloads the resource at rawURL and returns a FetchResult.
// It enforces robots.txt, per-host rate limiting, max-bytes, and
// content-type detection. The audit log only ever sees the hostname,
// never the full URL or response body.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, opts FetchOptions) (FetchResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return FetchResult{}, fmt.Errorf("web: parse URL: %w", err)
	}
	host := u.Hostname()

	// Apply per-host rate limit.
	if err := f.rateLimit(ctx, host, opts.MinInterval); err != nil {
		return FetchResult{}, err
	}

	// robots.txt check (fail closed).
	if !opts.SkipRobots {
		if blocked, err := f.isBlocked(ctx, u, opts); err != nil || blocked {
			if err != nil {
				// Treat any robots.txt error as a block (fail closed).
				return FetchResult{}, fmt.Errorf("%w: %s (robots error: %v)", ErrBlockedByRobots, host, err)
			}
			return FetchResult{}, fmt.Errorf("%w: %s", ErrBlockedByRobots, host)
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("web: build request: %w", err)
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range opts.ExtraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("web: fetch %s: %w", host, err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxBodyBytes+1)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return FetchResult{}, fmt.Errorf("web: read body from %s: %w", host, err)
	}
	if int64(len(bodyBytes)) > maxBodyBytes {
		return FetchResult{}, fmt.Errorf("web: response from %s exceeds 5 MiB cap", host)
	}

	// Harvest headers (first value only, per contract).
	headers := make(map[string]string, len(resp.Header))
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	ct := resp.Header.Get("Content-Type")
	kind := detectKind(ct)
	body := string(bodyBytes)

	result := FetchResult{
		Kind:    kind,
		Body:    body,
		Status:  resp.StatusCode,
		Headers: headers,
	}

	if kind == "json" {
		var v any
		if jerr := json.Unmarshal(bodyBytes, &v); jerr == nil {
			result.Parsed = v
		}
	}

	return result, nil
}

// detectKind maps a Content-Type header to "html", "json", or "text".
func detectKind(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "html"):
		return "html"
	case strings.Contains(ct, "json"):
		return "json"
	default:
		return "text"
	}
}

// rateLimit blocks until at least minInterval has elapsed since the
// last request to host. It respects ctx cancellation.
func (f *Fetcher) rateLimit(ctx context.Context, host string, minInterval time.Duration) error {
	if minInterval <= 0 {
		minInterval = time.Second
	}
	f.rlMu.Lock()
	last := f.rlMap[host]
	f.rlMu.Unlock()

	if !last.IsZero() {
		wait := minInterval - time.Since(last)
		if wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
	}

	f.rlMu.Lock()
	f.rlMap[host] = time.Now()
	f.rlMu.Unlock()
	return nil
}

// isBlocked fetches (with caching) the target host's robots.txt and
// returns true if the given URL path is disallowed for our User-Agent.
// Any error fetching / parsing robots.txt returns (false, err) so the
// caller can fail closed.
func (f *Fetcher) isBlocked(ctx context.Context, u *url.URL, opts FetchOptions) (bool, error) {
	host := u.Hostname()

	f.robotsMu.Lock()
	entry, ok := f.robotsCache[host]
	f.robotsMu.Unlock()

	if ok && time.Since(entry.fetchedAt) < robotsTTL {
		return matchDisallow(entry.disallowed, u.Path), nil
	}

	// Fetch robots.txt (best-effort, timeout 10s).
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", u.Scheme, u.Host)
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return false, fmt.Errorf("web: build robots request for %s: %w", host, err)
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	resp, err := f.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("web: fetch robots.txt for %s: %w", host, err)
	}
	defer resp.Body.Close()

	// 404 or 410 → no restrictions.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		f.cacheRobots(host, robotsEntry{disallowed: nil, fetchedAt: time.Now()})
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("web: robots.txt for %s returned %d", host, resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, 512*1024) // 512 KiB cap
	robotsBytes, err := io.ReadAll(limited)
	if err != nil {
		return false, fmt.Errorf("web: read robots.txt for %s: %w", host, err)
	}

	disallowed := parseRobotsTxt(string(robotsBytes), ua)
	newEntry := robotsEntry{disallowed: disallowed, fetchedAt: time.Now()}
	f.cacheRobots(host, newEntry)

	return matchDisallow(disallowed, u.Path), nil
}

func (f *Fetcher) cacheRobots(host string, e robotsEntry) {
	f.robotsMu.Lock()
	f.robotsCache[host] = e
	f.robotsMu.Unlock()
}

// parseRobotsTxt is a minimal robots.txt parser. It honours the
// first matching User-agent group (exact match or "*"). Disallow
// rules are collected as path prefixes.
func parseRobotsTxt(content, userAgent string) map[string]bool {
	disallowed := make(map[string]bool)
	lines := strings.Split(content, "\n")

	// normalise UA for matching
	uaLower := strings.ToLower(strings.SplitN(userAgent, "/", 2)[0])

	var (
		inBlock   bool
		applied   bool
	)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		// strip inline comments
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			if inBlock {
				// blank line ends the block
				inBlock = false
			}
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		field := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])

		switch strings.ToLower(field) {
		case "user-agent":
			if applied {
				break
			}
			vaLower := strings.ToLower(value)
			if vaLower == "*" || strings.Contains(uaLower, vaLower) {
				inBlock = true
			} else {
				inBlock = false
			}
		case "disallow":
			if inBlock && value != "" {
				disallowed[value] = true
			}
		case "allow":
			if inBlock {
				applied = true
			}
		}
	}
	return disallowed
}

// matchDisallow returns true if path is disallowed by any prefix in
// the disallowed set.
func matchDisallow(disallowed map[string]bool, path string) bool {
	if len(disallowed) == 0 {
		return false
	}
	for prefix := range disallowed {
		if prefix == "/" || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// BodyAsHTMLNode parses the body as HTML and returns the root node.
// Used by the CSS scraper. Returns an error if the body is not HTML.
func BodyAsHTMLNode(body string) (*html.Node, error) {
	return html.Parse(strings.NewReader(body))
}
