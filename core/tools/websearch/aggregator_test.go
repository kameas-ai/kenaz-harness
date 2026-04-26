package websearch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// stubBackend is a fixed-result Backend used for aggregator tests.
type stubBackend struct {
	name string
	hits []SearchHit
	err  error

	mu    sync.Mutex
	calls int
}

func (s *stubBackend) Name() string { return s.name }

func (s *stubBackend) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchHit, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return append([]SearchHit(nil), s.hits...), nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAggregator_Interleave(t *testing.T) {
	t.Parallel()
	a := NewAggregator([]Backend{
		&stubBackend{name: "ddg", hits: []SearchHit{
			{Title: "A1", URL: "https://a.example/1"},
			{Title: "A2", URL: "https://a.example/2"},
			{Title: "A3", URL: "https://a.example/3"},
		}},
		&stubBackend{name: "wiki", hits: []SearchHit{
			{Title: "B1", URL: "https://b.example/1"},
			{Title: "B2", URL: "https://b.example/2"},
		}},
	}, discardLogger())

	hits, err := a.Search(context.Background(), "q", SearchOpts{MaxResults: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	wantOrder := []string{"A1", "B1", "A2", "B2", "A3"}
	if len(hits) != len(wantOrder) {
		t.Fatalf("got %d hits, want %d", len(hits), len(wantOrder))
	}
	for i, want := range wantOrder {
		if hits[i].Title != want {
			t.Errorf("hits[%d] = %q, want %q", i, hits[i].Title, want)
		}
	}
}

func TestAggregator_MaxResultsCap(t *testing.T) {
	t.Parallel()
	a := NewAggregator([]Backend{
		&stubBackend{name: "ddg", hits: []SearchHit{
			{URL: "https://a.example/1", Title: "A1"},
			{URL: "https://a.example/2", Title: "A2"},
		}},
	}, discardLogger())

	hits, err := a.Search(context.Background(), "q", SearchOpts{MaxResults: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
}

func TestAggregator_DedupeByHostPath(t *testing.T) {
	t.Parallel()
	a := NewAggregator([]Backend{
		&stubBackend{name: "ddg", hits: []SearchHit{
			{Title: "A", URL: "HTTPS://Example.COM/foo"},
			{Title: "B", URL: "https://example.com/foo?utm=1"}, // distinct: query differs
		}},
		&stubBackend{name: "wiki", hits: []SearchHit{
			{Title: "C", URL: "https://example.com/foo"}, // dupe of A
		}},
	}, discardLogger())

	hits, err := a.Search(context.Background(), "q", SearchOpts{MaxResults: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits after dedupe, got %d (%+v)", len(hits), hits)
	}
}

func TestAggregator_OneBackendErrorContinues(t *testing.T) {
	t.Parallel()
	good := &stubBackend{name: "good", hits: []SearchHit{
		{Title: "A", URL: "https://a.example/1"},
	}}
	bad := &stubBackend{name: "bad", err: ErrRateLimited}

	a := NewAggregator([]Backend{bad, good}, discardLogger())
	hits, err := a.Search(context.Background(), "q", SearchOpts{MaxResults: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit (bad backend skipped), got %d", len(hits))
	}
	if bad.calls != 1 || good.calls != 1 {
		t.Errorf("expected each backend called once, bad=%d good=%d", bad.calls, good.calls)
	}
}

func TestAggregator_AllBackendsErrorReturnsError(t *testing.T) {
	t.Parallel()
	a := NewAggregator([]Backend{
		&stubBackend{name: "x", err: errors.New("x failed")},
		&stubBackend{name: "y", err: errors.New("y failed")},
	}, discardLogger())
	_, err := a.Search(context.Background(), "q", SearchOpts{})
	if err == nil {
		t.Fatal("expected combined error")
	}
	if !strings.Contains(err.Error(), "x failed") || !strings.Contains(err.Error(), "y failed") {
		t.Errorf("error did not mention both backends: %v", err)
	}
}

func TestAggregator_NoBackends(t *testing.T) {
	t.Parallel()
	a := NewAggregator(nil, discardLogger())
	_, err := a.Search(context.Background(), "q", SearchOpts{})
	if err == nil {
		t.Fatal("expected error for empty backend list")
	}
}

func TestAggregator_DomainFilters(t *testing.T) {
	t.Parallel()
	a := NewAggregator([]Backend{
		&stubBackend{name: "x", hits: []SearchHit{
			{Title: "a", URL: "https://en.wikipedia.org/wiki/Foo"},
			{Title: "b", URL: "https://github.com/wailsapp/wails"},
			{Title: "c", URL: "https://news.ycombinator.com/item?id=1"},
		}},
	}, discardLogger())

	t.Run("blocked", func(t *testing.T) {
		hits, err := a.Search(context.Background(), "q", SearchOpts{
			MaxResults:     10,
			BlockedDomains: []string{"ycombinator.com"},
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, h := range hits {
			if strings.Contains(h.URL, "ycombinator.com") {
				t.Errorf("ycombinator.com leaked through: %q", h.URL)
			}
		}
		if len(hits) != 2 {
			t.Errorf("expected 2 hits, got %d", len(hits))
		}
	})

	t.Run("allowed-and-blocked", func(t *testing.T) {
		hits, err := a.Search(context.Background(), "q", SearchOpts{
			MaxResults:     10,
			AllowedDomains: []string{"wikipedia.org", "github.com"},
			BlockedDomains: []string{"github.com"},
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("expected 1 hit (only wikipedia survives both filters), got %d", len(hits))
		}
		if !strings.Contains(hits[0].URL, "wikipedia.org") {
			t.Errorf("survivor not wikipedia: %q", hits[0].URL)
		}
	})

	t.Run("allowed-suffix-match", func(t *testing.T) {
		hits, err := a.Search(context.Background(), "q", SearchOpts{
			MaxResults:     10,
			AllowedDomains: []string{"wikipedia.org"},
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(hits) != 1 {
			t.Errorf("expected 1 hit (en.wikipedia.org suffix-matches wikipedia.org), got %d", len(hits))
		}
	})
}

func TestAggregator_DedupeKey(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://EXAMPLE.com/Foo":          "example.com/Foo",
		"http://example.com":               "example.com/",
		"https://example.com?q=1":          "example.com/?q=1",
		"https://example.com/path?utm=abc": "example.com/path?utm=abc",
	}
	for in, want := range cases {
		got := dedupeKey(in)
		if got != want {
			t.Errorf("dedupeKey(%q) = %q, want %q", in, got, want)
		}
	}
}
