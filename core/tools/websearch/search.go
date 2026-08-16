// Package websearch implements the local-first web-search tool for the
// Kenaz harness. The package owns the Backend interface, public-search
// scrapers (DuckDuckGo HTML, Wikipedia opensearch), the multi-backend
// aggregator, page fetch + readable-text extraction, and the
// <source>-tag prompt-injection wrapper. The Tool struct stitches these
// pieces into a single Call() suitable for tool-loop dispatch.
//
// Privacy invariant (NFR-003): every outbound HTTP destination is a
// user-configured public endpoint or a user-configured proxy; the
// harness vendor never sees the search query.
//
// Per the WP01 plan only DuckDuckGo + Wikipedia backends ship here;
// SearXNG and Brave land in a follow-up WP.
package websearch

import (
	"context"
	"net/url"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// checkBackendNetwork is the shared Cedar network-gate call every
// Backend makes before issuing its query request.
//
// It exists because the gate used to live ONLY on Fetcher (the leg that
// retrieves a result page). The query leg — the one that ships the
// user's search terms to duckduckgo.com / wikipedia.org — bypassed the
// gate entirely, so a policy forbidding those hosts was silently
// ineffective for the traffic that matters most.
//
// A nil gate is ungated: cedar.CheckNetwork returns nil, preserving the
// nil-core / test posture. An unparseable endpoint is treated as
// ungated rather than blocked, matching Fetcher.Fetch, which only gates
// when url.Parse succeeds — a parse failure fails on the request build
// a few lines later anyway.
func checkBackendNetwork(ctx context.Context, g cedar.Gate, endpoint string) error {
	if g == nil {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	return cedar.CheckNetwork(ctx, g, u.Hostname())
}

// SearchHit is a single backend-returned result. Title, URL, and
// Snippet mirror the fields every public engine surfaces.
type SearchHit struct {
	Title   string
	URL     string
	Snippet string
}

// SearchOpts captures the per-call knobs the Tool entrypoint forwards
// to every backend. AllowedDomains / BlockedDomains are advisory at the
// Backend layer — final filtering happens in the Aggregator and the
// Tool itself so a backend that ignores them still respects the gate.
type SearchOpts struct {
	MaxResults     int
	AllowedDomains []string
	BlockedDomains []string
}

// Backend is the contract every search source implements. Name() is a
// stable identifier used in logs / dedupe metadata; Search() runs the
// actual query.
type Backend interface {
	Name() string
	Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error)
}
