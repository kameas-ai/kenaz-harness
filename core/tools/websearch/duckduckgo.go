package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// DuckDuckGoEndpoint is the static-HTML endpoint the backend scrapes.
// Exposed so tests (and a future SearXNG cousin) can swap it.
const DuckDuckGoEndpoint = "https://html.duckduckgo.com/html/"

// ErrRateLimited is returned when an upstream backend signals it is
// throttling us (HTTP 429). The aggregator treats it as a soft failure:
// other backends still run.
var ErrRateLimited = errors.New("websearch: rate limited")

// DuckDuckGoBackend hits html.duckduckgo.com/html/ with a plain GET and
// walks the response with golang.org/x/net/html.
//
// The parser intentionally targets two stable selectors: result rows
// have class "result" and the title anchor inside has class
// "result__a" (snippet text lives in a sibling node with class
// "result__snippet"). When DDG breaks either selector the fixture test
// fails loudly, which is the agreed update path (see plan §"Risk
// register" and the testdata/ddg_html_sample.html fixture).
type DuckDuckGoBackend struct {
	client   *http.Client
	endpoint string
}

// DuckDuckGoOption configures a DuckDuckGoBackend at construction.
type DuckDuckGoOption func(*DuckDuckGoBackend)

// WithDuckDuckGoClient overrides the http.Client. Tests pin this; the
// default uses http.DefaultClient.
func WithDuckDuckGoClient(c *http.Client) DuckDuckGoOption {
	return func(b *DuckDuckGoBackend) {
		if c != nil {
			b.client = c
		}
	}
}

// WithDuckDuckGoEndpoint overrides the URL. Tests use this to point at
// an httptest.Server.
func WithDuckDuckGoEndpoint(endpoint string) DuckDuckGoOption {
	return func(b *DuckDuckGoBackend) {
		if endpoint != "" {
			b.endpoint = endpoint
		}
	}
}

// NewDuckDuckGoBackend constructs a DuckDuckGoBackend with the default
// endpoint and http.DefaultClient unless options override.
func NewDuckDuckGoBackend(opts ...DuckDuckGoOption) *DuckDuckGoBackend {
	b := &DuckDuckGoBackend{
		client:   http.DefaultClient,
		endpoint: DuckDuckGoEndpoint,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Name implements Backend.
func (b *DuckDuckGoBackend) Name() string { return "duckduckgo" }

// Search implements Backend. Returns up to opts.MaxResults hits parsed
// from the DDG HTML response.
func (b *DuckDuckGoBackend) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("websearch/duckduckgo: empty query")
	}

	q := url.Values{}
	q.Set("q", query)
	reqURL := b.endpoint + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websearch/duckduckgo: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "text/html")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("websearch/duckduckgo: get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("websearch/duckduckgo: %w", ErrRateLimited)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("websearch/duckduckgo: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MiB cap
	if err != nil {
		return nil, fmt.Errorf("websearch/duckduckgo: read body: %w", err)
	}

	hits, err := parseDuckDuckGoHTML(body)
	if err != nil {
		return nil, err
	}

	if opts.MaxResults > 0 && len(hits) > opts.MaxResults {
		hits = hits[:opts.MaxResults]
	}
	return hits, nil
}

// parseDuckDuckGoHTML walks the parsed HTML tree and extracts the
// title anchor + snippet for every node with class "result". Malformed
// nodes are skipped silently so a partial DDG payload still yields the
// hits it can.
func parseDuckDuckGoHTML(body []byte) ([]SearchHit, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("websearch/duckduckgo: parse html: %w", err)
	}

	var hits []SearchHit
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && hasClass(n, "result") {
			hit, ok := extractDuckDuckGoHit(n)
			if ok {
				hits = append(hits, hit)
			}
			// don't recurse into a result row twice
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return hits, nil
}

// extractDuckDuckGoHit pulls the result title anchor + snippet text
// from a single .result subtree.
func extractDuckDuckGoHit(n *html.Node) (SearchHit, bool) {
	var hit SearchHit
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
			hit.URL = decodeDuckDuckGoHref(getAttr(node, "href"))
			hit.Title = strings.TrimSpace(textContent(node))
		}
		if node.Type == html.ElementNode && hasClass(node, "result__snippet") {
			hit.Snippet = strings.TrimSpace(textContent(node))
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	if hit.Title == "" || hit.URL == "" {
		return SearchHit{}, false
	}
	return hit, true
}

// decodeDuckDuckGoHref unwraps the redirect URL DDG sometimes wraps
// around a result link (e.g. /l/?uddg=https%3A%2F%2Fexample.com%2F).
// When the href is already a plain absolute URL it is returned
// unchanged.
func decodeDuckDuckGoHref(href string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	// DDG redirects look like //duckduckgo.com/l/?uddg=...
	parsed, err := url.Parse(href)
	if err != nil {
		return href
	}
	if v := parsed.Query().Get("uddg"); v != "" {
		if dec, err := url.QueryUnescape(v); err == nil {
			return dec
		}
		return v
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

func hasClass(n *html.Node, want string) bool {
	if n == nil {
		return false
	}
	classes := getAttr(n, "class")
	if classes == "" {
		return false
	}
	for _, c := range strings.Fields(classes) {
		if c == want {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}
