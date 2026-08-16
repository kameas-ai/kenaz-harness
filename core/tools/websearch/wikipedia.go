package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// WikipediaEndpoint is the opensearch JSON endpoint the backend hits.
// Exposed so tests can swap it for an httptest.Server.
const WikipediaEndpoint = "https://en.wikipedia.org/w/api.php"

// WikipediaBackend hits Wikipedia's opensearch endpoint and parses the
// canonical four-array response: [query, [titles], [descriptions], [urls]].
//
// Like DuckDuckGoBackend, the query request is Cedar-gated via
// checkBackendNetwork before it is issued; gating only the result-page
// Fetcher left this leg open.
type WikipediaBackend struct {
	client   *http.Client
	endpoint string
	gate     cedar.Gate
}

// WikipediaOption configures a WikipediaBackend.
type WikipediaOption func(*WikipediaBackend)

// WithWikipediaClient overrides the http.Client.
func WithWikipediaClient(c *http.Client) WikipediaOption {
	return func(b *WikipediaBackend) {
		if c != nil {
			b.client = c
		}
	}
}

// WithWikipediaEndpoint overrides the base URL.
func WithWikipediaEndpoint(endpoint string) WikipediaOption {
	return func(b *WikipediaBackend) {
		if endpoint != "" {
			b.endpoint = endpoint
		}
	}
}

// WithWikipediaGate installs the Cedar network gate consulted before
// the query request is issued. nil means ungated.
func WithWikipediaGate(g cedar.Gate) WikipediaOption {
	return func(b *WikipediaBackend) {
		b.gate = g
	}
}

// NewWikipediaBackend constructs a WikipediaBackend with stdlib defaults.
func NewWikipediaBackend(opts ...WikipediaOption) *WikipediaBackend {
	b := &WikipediaBackend{
		client:   http.DefaultClient,
		endpoint: WikipediaEndpoint,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Name implements Backend.
func (b *WikipediaBackend) Name() string { return "wikipedia" }

// Search implements Backend.
func (b *WikipediaBackend) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("websearch/wikipedia: empty query")
	}

	limit := opts.MaxResults
	if limit <= 0 {
		limit = 5
	}

	q := url.Values{}
	q.Set("action", "opensearch")
	q.Set("search", query)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("format", "json")
	reqURL := b.endpoint + "?" + q.Encode()

	// Cedar network gate on the query leg — see checkBackendNetwork.
	if gerr := checkBackendNetwork(ctx, b.gate, b.endpoint); gerr != nil {
		return nil, gerr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("websearch/wikipedia: %w", ErrRateLimited)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("websearch/wikipedia: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: read body: %w", err)
	}

	return parseWikipediaJSON(body)
}

// parseWikipediaJSON parses the [query, [titles], [descriptions], [urls]]
// shape into []SearchHit. Tolerates short / mismatched arrays so a
// truncated upstream response still yields the hits it can.
func parseWikipediaJSON(body []byte) ([]SearchHit, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: parse: %w", err)
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("websearch/wikipedia: unexpected response shape (len=%d)", len(raw))
	}
	var titles, descs, urls []string
	if err := json.Unmarshal(raw[1], &titles); err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: parse titles: %w", err)
	}
	if err := json.Unmarshal(raw[2], &descs); err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: parse descriptions: %w", err)
	}
	if err := json.Unmarshal(raw[3], &urls); err != nil {
		return nil, fmt.Errorf("websearch/wikipedia: parse urls: %w", err)
	}

	n := len(titles)
	if len(urls) < n {
		n = len(urls)
	}
	hits := make([]SearchHit, 0, n)
	for i := 0; i < n; i++ {
		hit := SearchHit{Title: titles[i], URL: urls[i]}
		if i < len(descs) {
			hit.Snippet = descs[i]
		}
		hits = append(hits, hit)
	}
	return hits, nil
}
