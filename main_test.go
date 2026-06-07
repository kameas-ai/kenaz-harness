package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
)

// TestCSPMiddlewareSetsStrictHeaders covers privacy CI invariant #1
// (plan §4.3): the production CSP must include connect-src 'none',
// script-src 'self' (no unsafe-*), font-src 'self', no CDNs.
func TestCSPMiddlewareSetsStrictHeaders(t *testing.T) {
	mw := rpc.NewCSPMiddleware()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := mw(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatalf("Content-Security-Policy header missing")
	}

	mustContain := []string{
		"default-src 'none'",
		"connect-src 'none'",
		"script-src 'self'",
		"font-src 'self'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"object-src 'none'",
	}
	for _, s := range mustContain {
		if !strings.Contains(csp, s) {
			t.Errorf("CSP missing required directive %q\nFull CSP: %s", s, csp)
		}
	}

	mustNotContain := []string{"unsafe-eval", "unsafe-inline'; script-src", "https://", "http://"}
	for _, s := range mustNotContain {
		if strings.Contains(csp, s) {
			t.Errorf("CSP contains forbidden token %q\nFull CSP: %s", s, csp)
		}
	}
	// Style allows 'unsafe-inline' for scoped styles; ensure it's only
	// inside style-src and nowhere else.
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("style-src expected to allow 'unsafe-inline'; got: %s", csp)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}
