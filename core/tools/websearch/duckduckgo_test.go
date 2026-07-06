package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDuckDuckGo_ParseGoldenFixture(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/ddg_html_sample.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	hits, err := parseDuckDuckGoHTML(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 5 {
		t.Fatalf("expected 5 hits, got %d", len(hits))
	}

	first := hits[0]
	if first.Title != "Releases · wailsapp/wails · GitHub" {
		t.Errorf("first hit title = %q", first.Title)
	}
	if first.URL != "https://github.com/wailsapp/wails/releases" {
		t.Errorf("first hit url = %q", first.URL)
	}
	if !strings.Contains(first.Snippet, "GitHub releases page") {
		t.Errorf("first hit snippet = %q", first.Snippet)
	}

	// Second hit's href is a //duckduckgo.com/l/?uddg=... redirect; we
	// should have unwrapped it.
	if hits[1].URL != "https://wails.io/blog/release-notes/" {
		t.Errorf("redirect unwrap failed: url = %q", hits[1].URL)
	}
}

func TestDuckDuckGo_SearchUsesEndpoint(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/ddg_html_sample.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			t.Errorf("missing q param")
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "kenaz-harness/") {
			t.Errorf("missing UA: %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	b := NewDuckDuckGoBackend(WithDuckDuckGoEndpoint(srv.URL), WithDuckDuckGoClient(srv.Client()))
	hits, err := b.Search(context.Background(), "wails", SearchOpts{MaxResults: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits (capped), got %d", len(hits))
	}
	if b.Name() != "duckduckgo" {
		t.Errorf("Name = %q, want duckduckgo", b.Name())
	}
}

func TestDuckDuckGo_RateLimited(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	b := NewDuckDuckGoBackend(WithDuckDuckGoEndpoint(srv.URL), WithDuckDuckGoClient(srv.Client()))
	_, err := b.Search(context.Background(), "anything", SearchOpts{})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestDuckDuckGo_NetworkError(t *testing.T) {
	t.Parallel()
	// Point at a closed server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closed := srv.URL
	srv.Close()

	b := NewDuckDuckGoBackend(WithDuckDuckGoEndpoint(closed))
	_, err := b.Search(context.Background(), "x", SearchOpts{})
	if err == nil {
		t.Fatal("expected network error")
	}
	if !strings.Contains(err.Error(), "websearch/duckduckgo") {
		t.Errorf("error not wrapped: %v", err)
	}
}

func TestDuckDuckGo_EmptyQuery(t *testing.T) {
	t.Parallel()
	b := NewDuckDuckGoBackend()
	_, err := b.Search(context.Background(), "  ", SearchOpts{})
	if err == nil {
		t.Fatal("expected empty-query error")
	}
}

func TestDuckDuckGo_MalformedHTML(t *testing.T) {
	t.Parallel()
	// Random bytes that html.Parse will still tolerate; expect an
	// empty hit slice rather than a panic.
	hits, err := parseDuckDuckGoHTML([]byte("<<<<not really html>>>>>"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits, got %d", len(hits))
	}
}

func TestDuckDuckGo_PartialResultRow(t *testing.T) {
	t.Parallel()
	// One row missing the title anchor; parser should skip it and
	// return the well-formed neighbour.
	in := []byte(`
<html><body>
  <div class="result"><div>broken row, no anchor</div></div>
  <div class="result">
    <a class="result__a" href="https://example.com/">Example</a>
    <span class="result__snippet">Example domain.</span>
  </div>
</body></html>`)
	hits, err := parseDuckDuckGoHTML(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].URL != "https://example.com/" {
		t.Errorf("hit url = %q", hits[0].URL)
	}
}
