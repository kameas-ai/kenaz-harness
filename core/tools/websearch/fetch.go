package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultFetchTimeout    = 8 * time.Second
	defaultFetchRedirects  = 5
	defaultFetchBodyMaxMiB = 5
)

// ErrBodyTruncated is returned when the upstream response exceeds the
// fetcher's body cap. The caller still receives the partial body so
// the extractor can salvage what it can.
var ErrBodyTruncated = errors.New("websearch: body truncated at cap")

// FetcherOpts configures a Fetcher.
type FetcherOpts struct {
	// ProxyURL is an optional proxy (e.g. "socks5://localhost:9050"
	// or "http://127.0.0.1:8080"). Empty disables proxying.
	ProxyURL string
	// Timeout applies to the entire request (including reading the
	// body). Zero means defaultFetchTimeout.
	Timeout time.Duration
	// MaxRedirects is the cap. Zero means defaultFetchRedirects.
	MaxRedirects int
	// MaxBodyBytes is the cap on response body size in bytes. Zero
	// means 5 MiB.
	MaxBodyBytes int64
	// Transport is an optional override; tests pin this to inject
	// httptest.Server round-trippers. Mutually exclusive with
	// ProxyURL — when both are set, Transport wins.
	Transport http.RoundTripper
}

// Fetcher wraps an http.Client with the harness's fetch policy: proxy,
// per-request timeout, redirect cap, body cap, branded UA.
type Fetcher struct {
	client       *http.Client
	maxBodyBytes int64
}

// NewFetcher constructs a Fetcher. Returns an error if the proxy URL
// is malformed.
func NewFetcher(opts FetcherOpts) (*Fetcher, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	redirects := opts.MaxRedirects
	if redirects <= 0 {
		redirects = defaultFetchRedirects
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = int64(defaultFetchBodyMaxMiB) << 20
	}

	var rt http.RoundTripper = opts.Transport
	if rt == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		if opts.ProxyURL != "" {
			pu, err := url.Parse(opts.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("websearch/fetch: parse proxy: %w", err)
			}
			t.Proxy = http.ProxyURL(pu)
		}
		rt = t
	}

	client := &http.Client{
		Transport: rt,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= redirects {
				return fmt.Errorf("websearch/fetch: redirect cap (%d) exceeded", redirects)
			}
			return nil
		},
	}
	return &Fetcher{client: client, maxBodyBytes: maxBody}, nil
}

// Fetch GETs url and returns up to maxBodyBytes of its body plus the
// Content-Type header. If the body is truncated to the cap, the
// returned error is ErrBodyTruncated and body still contains the
// partial content (the caller decides whether to extract).
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, string, error) {
	if rawURL == "" {
		return nil, "", fmt.Errorf("websearch/fetch: empty url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("websearch/fetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("websearch/fetch: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, resp.Header.Get("Content-Type"),
			fmt.Errorf("websearch/fetch: status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, f.maxBodyBytes+1)
	body, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, resp.Header.Get("Content-Type"),
			fmt.Errorf("websearch/fetch: read: %w", readErr)
	}

	contentType := resp.Header.Get("Content-Type")
	if int64(len(body)) > f.maxBodyBytes {
		return body[:f.maxBodyBytes], contentType, ErrBodyTruncated
	}
	return body, contentType, nil
}
