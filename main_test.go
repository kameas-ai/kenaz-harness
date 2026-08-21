package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
	settingsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
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

// newWindowSizeTestAPI builds a *rpc.API backed by a hermetic FileStore
// (never the developer's real settings.json) for the window-size tests
// below.
func newWindowSizeTestAPI(t *testing.T) *rpc.API {
	t.Helper()
	store, err := settingsview.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return rpc.New(nil, rpc.WithSettingsStore(store))
}

// TestResolveInitialWindowSize_FreshInstall covers AC-011's launch half: a
// fresh install (no settings.json) still opens at the historical 1280x800
// literal — resolveInitialWindowSize must fall back, not zero out the
// window.
func TestResolveInitialWindowSize_FreshInstall(t *testing.T) {
	api := newWindowSizeTestAPI(t)
	w, h := resolveInitialWindowSize(context.Background(), api)
	if w != defaultWindowWidth || h != defaultWindowHeight {
		t.Fatalf("resolveInitialWindowSize() = (%d, %d), want (%d, %d)", w, h, defaultWindowWidth, defaultWindowHeight)
	}
}

// TestWindowSizeRoundTrip is AC-011 driven against a fake runtime: a
// windowed end-to-end run through actual wails.Run is not available in
// this CI environment (no display server / OS window manager), so this
// drives OnStartup's resolveInitialWindowSize and OnShutdown's
// persistWindowSize directly, with getSize standing in for
// wailsruntime.WindowGetSize.
//
// Mutation covered: comment out the persistWindowSize call at the
// OnShutdown site (or replace this test's persistWindowSize call with a
// no-op) and this test fails, because the second resolve would still see
// the pre-resize value.
func TestWindowSizeRoundTrip(t *testing.T) {
	api := newWindowSizeTestAPI(t)
	ctx := context.Background()

	w1, h1 := resolveInitialWindowSize(ctx, api)
	if w1 != defaultWindowWidth || h1 != defaultWindowHeight {
		t.Fatalf("initial resolve = (%d, %d), want defaults", w1, h1)
	}

	// Simulate the user resizing to 900x600, then quitting.
	fakeGetSize := func(context.Context) (int, int) { return 900, 600 }
	persistWindowSize(ctx, api, fakeGetSize)

	// Simulate a relaunch: a fresh resolve must see the persisted size,
	// not the literal.
	w2, h2 := resolveInitialWindowSize(ctx, api)
	if w2 != 900 || h2 != 600 {
		t.Fatalf("resolve after persist = (%d, %d), want (900, 600)", w2, h2)
	}
}

// TestPersistWindowSize_IgnoresZeroSize guards against a headless / not-
// yet-shown window (WindowGetSize returning 0,0 before first paint)
// clobbering a previously-persisted real size.
func TestPersistWindowSize_IgnoresZeroSize(t *testing.T) {
	api := newWindowSizeTestAPI(t)
	ctx := context.Background()

	persistWindowSize(ctx, api, func(context.Context) (int, int) { return 900, 600 })
	persistWindowSize(ctx, api, func(context.Context) (int, int) { return 0, 0 })

	w, h := resolveInitialWindowSize(ctx, api)
	if w != 900 || h != 600 {
		t.Fatalf("zero-size persist clobbered the real size: got (%d, %d), want (900, 600)", w, h)
	}
}

// TestWindowSizeUpgradePath is AC-012 / AC-PI-1: a settings.json shaped
// like a v0.64.0 file (the literal 1280x800 this mission's own doc
// records as the pre-WP06 seed) loads cleanly and is OVERWRITTEN by the
// observed size on the next shutdown — not re-seeded back to the
// literal. Falsifiability: reverting the OnShutdown persistWindowSize
// wiring would leave settings.json holding 1280x800 forever, which is
// exactly the pre-WP06 defect TestWindowSizeRoundTrip above already
// pins; this test additionally proves it starting from a previously-
// shipped on-disk shape, not a freshly-constructed Settings struct.
func TestWindowSizeUpgradePath(t *testing.T) {
	// Reuse the SAME v0.64.0 fixture UNIT-1/WP03's wp03_test.go already
	// built for this mission's WP-PI (AC-PI-1, settings half), rather
	// than hand-rolling a second one — it already carries the literal
	// windowSize this WP's own doc records as the pre-WP06 seed. See
	// core/rpc/views/settings/testdata/upgrade/v0.64.0/PROVENANCE.md.
	legacy, err := os.ReadFile(filepath.Join("core", "rpc", "views", "settings", "testdata", "upgrade", "v0.64.0", "settings.json"))
	if err != nil {
		t.Fatalf("read v0.64.0 fixture: %v", err)
	}

	dir := t.TempDir()
	configDir := filepath.Join(dir, "kenaz-harness")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), legacy, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := settingsview.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	api := rpc.New(nil, rpc.WithSettingsStore(store))
	ctx := context.Background()

	// Boots from the v0.64.0-shaped file: resolves to the literal
	// because that IS what the user last had.
	w0, h0 := resolveInitialWindowSize(ctx, api)
	if w0 != 1280 || h0 != 800 {
		t.Fatalf("upgrade-path resolve = (%d, %d), want (1280, 800)", w0, h0)
	}

	// The user resizes and quits; the observed size must overwrite the
	// legacy literal, not be dropped in favour of it.
	persistWindowSize(ctx, api, func(context.Context) (int, int) { return 1440, 900 })

	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.WindowSize.Width != 1440 || got.WindowSize.Height != 900 {
		t.Fatalf("post-shutdown WindowSize = %+v, want {1440 900} (must overwrite, not re-seed to 1280x800)", got.WindowSize)
	}
}
