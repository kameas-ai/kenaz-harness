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

func TestWikipedia_ParseGoldenFixture(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/wikipedia_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	hits, err := parseWikipediaJSON(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 5 {
		t.Fatalf("expected 5 hits, got %d", len(hits))
	}
	if hits[0].Title != "Wails (software)" {
		t.Errorf("first title = %q", hits[0].Title)
	}
	if hits[0].URL != "https://en.wikipedia.org/wiki/Wails_(software)" {
		t.Errorf("first url = %q", hits[0].URL)
	}
	if !strings.Contains(hits[0].Snippet, "framework") {
		t.Errorf("first snippet = %q", hits[0].Snippet)
	}
}

func TestWikipedia_SearchUsesEndpoint(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/wikipedia_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("action") != "opensearch" {
			t.Errorf("action = %q", q.Get("action"))
		}
		if q.Get("search") == "" {
			t.Errorf("missing search param")
		}
		if q.Get("limit") != "3" {
			t.Errorf("limit = %q", q.Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	b := NewWikipediaBackend(WithWikipediaEndpoint(srv.URL), WithWikipediaClient(srv.Client()))
	hits, err := b.Search(context.Background(), "wails", SearchOpts{MaxResults: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 5 {
		// MaxResults is forwarded as the limit param; the fixture
		// doesn't actually honour limit so we get 5. The point of
		// the test is "limit param is forwarded", which the handler
		// asserts.
		t.Logf("note: fixture returns 5 entries regardless of limit param; got %d", len(hits))
	}
	if b.Name() != "wikipedia" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestWikipedia_RateLimited(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	b := NewWikipediaBackend(WithWikipediaEndpoint(srv.URL), WithWikipediaClient(srv.Client()))
	_, err := b.Search(context.Background(), "x", SearchOpts{})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestWikipedia_MalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := parseWikipediaJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestWikipedia_ShortResponseShape(t *testing.T) {
	t.Parallel()
	// Only two arrays — should fail with the unexpected-shape error.
	_, err := parseWikipediaJSON([]byte(`["q",["t"]]`))
	if err == nil {
		t.Fatal("expected shape error")
	}
}

func TestWikipedia_MismatchedArrays(t *testing.T) {
	t.Parallel()
	// titles:3, descriptions:1, urls:2 — we should get min(titles,urls)=2 hits.
	in := []byte(`["q",["t1","t2","t3"],["d1"],["u1","u2"]]`)
	hits, err := parseWikipediaJSON(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Snippet != "d1" {
		t.Errorf("hit0 snippet = %q", hits[0].Snippet)
	}
	if hits[1].Snippet != "" {
		t.Errorf("hit1 snippet should be empty, got %q", hits[1].Snippet)
	}
}

func TestWikipedia_EmptyQuery(t *testing.T) {
	t.Parallel()
	b := NewWikipediaBackend()
	_, err := b.Search(context.Background(), "", SearchOpts{})
	if err == nil {
		t.Fatal("expected empty-query error")
	}
}
