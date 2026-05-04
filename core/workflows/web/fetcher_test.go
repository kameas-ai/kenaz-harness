package web_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/workflows/web"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestServer returns an httptest server that serves a fixed body with
// the given content-type. The caller must defer srv.Close().
func newTestServer(t *testing.T, body, contentType string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}))
}

// ---------------------------------------------------------------------------
// Fetch: happy paths
// ---------------------------------------------------------------------------

func TestFetch_HTMLPage(t *testing.T) {
	srv := newTestServer(t, "<html><h1>Hello</h1></html>", "text/html; charset=utf-8")
	defer srv.Close()

	f := web.NewFetcher()
	res, err := f.Fetch(t.Context(), srv.URL+"/page", web.FetchOptions{SkipRobots: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Kind != "html" {
		t.Errorf("kind: got %q want %q", res.Kind, "html")
	}
	if res.Status != 200 {
		t.Errorf("status: got %d want 200", res.Status)
	}
	if res.Body == "" {
		t.Error("body empty")
	}
}

func TestFetch_JSONPayload(t *testing.T) {
	srv := newTestServer(t, `{"hello":"world"}`, "application/json")
	defer srv.Close()

	f := web.NewFetcher()
	res, err := f.Fetch(t.Context(), srv.URL+"/api", web.FetchOptions{SkipRobots: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Kind != "json" {
		t.Errorf("kind: got %q want %q", res.Kind, "json")
	}
	if res.Parsed == nil {
		t.Error("parsed JSON is nil")
	}
}

func TestFetch_TextPayload(t *testing.T) {
	srv := newTestServer(t, "plain text", "text/plain")
	defer srv.Close()

	f := web.NewFetcher()
	res, err := f.Fetch(t.Context(), srv.URL+"/txt", web.FetchOptions{SkipRobots: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Kind != "text" {
		t.Errorf("kind: got %q want %q", res.Kind, "text")
	}
}

func TestFetch_HeadersPopulated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "hello")
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	f := web.NewFetcher()
	res, err := f.Fetch(t.Context(), srv.URL, web.FetchOptions{SkipRobots: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Headers["X-Custom"] != "hello" {
		t.Errorf("X-Custom header: got %q want %q", res.Headers["X-Custom"], "hello")
	}
}

// ---------------------------------------------------------------------------
// robots.txt enforcement
// ---------------------------------------------------------------------------

func TestFetch_BlockedByRobotsTxt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
		default:
			fmt.Fprint(w, "should not reach here")
		}
	}))
	defer srv.Close()

	f := web.NewFetcher()
	_, err := f.Fetch(t.Context(), srv.URL+"/secret", web.FetchOptions{})
	if err == nil {
		t.Fatal("expected web.ErrBlockedByRobots, got nil")
	}
	if !errors.Is(err, web.ErrBlockedByRobots) {
		t.Errorf("error type: got %v want web.ErrBlockedByRobots", err)
	}
}

func TestFetch_RobotsTxtAllowsOtherPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			// Only /admin/ is blocked.
			fmt.Fprint(w, "User-agent: *\nDisallow: /admin/\n")
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>ok</html>")
		}
	}))
	defer srv.Close()

	f := web.NewFetcher()
	_, err := f.Fetch(t.Context(), srv.URL+"/public", web.FetchOptions{})
	if err != nil {
		t.Fatalf("expected /public to be allowed: %v", err)
	}
}

func TestFetch_RobotsTxt404MeansNoRestrictions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>ok</html>")
	}))
	defer srv.Close()

	f := web.NewFetcher()
	_, err := f.Fetch(t.Context(), srv.URL+"/page", web.FetchOptions{})
	if err != nil {
		t.Fatalf("expected 404 robots.txt to permit all: %v", err)
	}
}

// ---------------------------------------------------------------------------
// robots.txt 24h cache
// ---------------------------------------------------------------------------

func TestFetch_RobotsTxtCachedFor24h(t *testing.T) {
	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fetchCount++
			fmt.Fprint(w, "User-agent: *\nDisallow: /nope\n")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>ok</html>")
	}))
	defer srv.Close()

	f := web.NewFetcher()
	// Two requests to the same host should only fetch robots.txt once.
	for i := 0; i < 3; i++ {
		if _, err := f.Fetch(t.Context(), srv.URL+"/page", web.FetchOptions{}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if fetchCount != 1 {
		t.Errorf("robots.txt fetch count: got %d want 1", fetchCount)
	}
}

// ---------------------------------------------------------------------------
// Max-bytes cap
// ---------------------------------------------------------------------------

func TestFetch_ExceedsMaxBytesErrors(t *testing.T) {
	// The 5 MiB cap; serve 5 MiB + 1 byte.
	body := make([]byte, 5<<20+1)
	for i := range body {
		body[i] = 'x'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	f := web.NewFetcher()
	_, err := f.Fetch(t.Context(), srv.URL+"/big", web.FetchOptions{SkipRobots: true})
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
}

// ---------------------------------------------------------------------------
// SkipRobots (used by tests to bypass robots.txt fetching)
// ---------------------------------------------------------------------------

func TestFetch_SkipRobotsFlag(t *testing.T) {
	// Serve a robots.txt that disallows everything, but SkipRobots should
	// bypass the check entirely.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>allowed</html>")
		}
	}))
	defer srv.Close()

	f := web.NewFetcher()
	res, err := f.Fetch(t.Context(), srv.URL+"/page", web.FetchOptions{SkipRobots: true})
	if err != nil {
		t.Fatalf("SkipRobots should bypass block: %v", err)
	}
	if res.Kind != "html" {
		t.Errorf("kind: got %q want html", res.Kind)
	}
}
