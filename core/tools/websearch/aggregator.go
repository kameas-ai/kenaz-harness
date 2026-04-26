package websearch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Aggregator fans a single query out across multiple Backends in
// parallel, dedupes by URL, applies allow/block-domain filters, and
// returns a top-K interleaved list. A single backend failure is logged
// and skipped; only when every backend errors does Search return an
// error.
type Aggregator struct {
	backends []Backend
	logger   *slog.Logger
}

// NewAggregator constructs an Aggregator. A nil logger is replaced
// with slog.Default() so the caller need not plumb one through.
func NewAggregator(backends []Backend, logger *slog.Logger) *Aggregator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Aggregator{backends: backends, logger: logger}
}

// Search runs every backend in parallel via errgroup, then dedupes,
// filters, and round-robin interleaves the results down to
// opts.MaxResults.
func (a *Aggregator) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
	if len(a.backends) == 0 {
		return nil, errors.New("websearch/aggregator: no backends configured")
	}

	type backendResult struct {
		name string
		hits []SearchHit
		err  error
	}

	results := make([]backendResult, len(a.backends))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for i, b := range a.backends {
		i, b := i, b
		g.Go(func() error {
			hits, err := b.Search(gctx, query, opts)
			mu.Lock()
			defer mu.Unlock()
			results[i] = backendResult{name: b.Name(), hits: hits, err: err}
			return nil
		})
	}
	// errgroup.Wait will never return non-nil here because each
	// goroutine returns nil — we collect failures into results above
	// so a single backend's failure cannot poison the others.
	_ = g.Wait()

	var (
		errCount int
		errMsgs  []string
		buckets  [][]SearchHit
	)
	for _, r := range results {
		if r.err != nil {
			errCount++
			errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", r.name, r.err))
			a.logger.Warn("websearch/aggregator: backend error",
				slog.String("backend", r.name),
				slog.String("error", r.err.Error()),
			)
			continue
		}
		buckets = append(buckets, r.hits)
	}
	if errCount == len(results) {
		return nil, fmt.Errorf("websearch/aggregator: all backends failed: %s", strings.Join(errMsgs, "; "))
	}

	merged := interleave(buckets)
	merged = dedupeByURL(merged)
	merged = filterDomains(merged, opts.AllowedDomains, opts.BlockedDomains)
	if opts.MaxResults > 0 && len(merged) > opts.MaxResults {
		merged = merged[:opts.MaxResults]
	}
	return merged, nil
}

// interleave round-robins across the per-backend hit slices: first hit
// from each, then second hit from each, etc. Order across backends
// follows the slice order (i.e. the construction order of Aggregator).
func interleave(buckets [][]SearchHit) []SearchHit {
	max := 0
	total := 0
	for _, b := range buckets {
		if len(b) > max {
			max = len(b)
		}
		total += len(b)
	}
	out := make([]SearchHit, 0, total)
	for i := 0; i < max; i++ {
		for _, b := range buckets {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	return out
}

// dedupeByURL drops duplicate hits whose host+path match (case
// insensitive). Query strings are preserved on purpose: two URLs that
// differ only in `?q=foo` vs `?q=bar` typically point at distinct
// content (search results for different terms, paginated articles).
func dedupeByURL(hits []SearchHit) []SearchHit {
	seen := make(map[string]struct{}, len(hits))
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		key := dedupeKey(h.URL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, h)
	}
	return out
}

func dedupeKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return strings.ToLower(rawURL)
	}
	host := strings.ToLower(u.Host)
	path := u.Path
	if path == "" {
		path = "/"
	}
	q := u.RawQuery
	if q != "" {
		return host + path + "?" + q
	}
	return host + path
}

// filterDomains applies allow/block lists. Match is suffix-on-hostname:
// "wikipedia.org" matches "en.wikipedia.org" and "wikipedia.org". An
// empty allow-list means "allow everything"; a non-empty allow-list
// means "allow only these". Block always wins over allow.
func filterDomains(hits []SearchHit, allow, block []string) []SearchHit {
	if len(allow) == 0 && len(block) == 0 {
		return hits
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		host := hostname(h.URL)
		if domainMatchesAny(host, block) {
			continue
		}
		if len(allow) > 0 && !domainMatchesAny(host, allow) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func hostname(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func domainMatchesAny(host string, suffixes []string) bool {
	if host == "" {
		return false
	}
	for _, s := range suffixes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}
