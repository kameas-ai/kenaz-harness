package sentry_test

// wp12_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-7 / WP12. AC-029: tier identified + fleet signed in -> the
// captured event carries a user tag; tier anonymous -> it does not.
// AC-030: a captured local crash appends to the cache and GetLastFive
// returns it. AC-031: a local report carries the build version, not
// "dev".

import (
	"context"
	"os"
	"strings"
	"testing"

	gosentry "github.com/getsentry/sentry-go"

	"github.com/kameas-ai/kenaz-harness/core/sentry"
)

// TestWP12_AC029_SetUser_TagsEventWhenIdentified drives the real
// gosentry Scope machinery (not a hand-rolled fake): initialise the SDK
// against a MockTransport (no network), call sentry.SetUser, then apply
// the current hub's scope to a fresh event and inspect its User field —
// exactly what CaptureException/CaptureMessage do internally via
// gosentry.WithScope + the SDK's own event pipeline.
func TestWP12_AC029_SetUser_TagsEventWhenIdentified(t *testing.T) {
	os.Unsetenv("HARNESS_SENTRY_DISABLED")
	// sentry.Init (not raw gosentry.Init) so the package-level
	// processClient singleton actually becomes a *liveClient —
	// sentry.SetUser routes through C().SetUser(), which is a no-op on
	// the default nopClient. The fake DSN + real Init pattern already
	// exists in this package (client_test.go's TestInit_KillSwitch);
	// sentry-go's default HTTPTransport queues sends on a background
	// worker rather than blocking Init/CaptureException on the network.
	if err := sentry.Init(sentry.TierIdentified, "https://fake@sentry.io/123", "v1", "sha"); err != nil {
		t.Fatalf("sentry.Init: %v", err)
	}

	// identified=true (tier == TierIdentified && fleet signed in).
	sentry.SetUser(true)
	event := applyScopeToFreshEvent(t)
	if event.User.ID == "" {
		t.Fatalf("AC-029: expected a non-empty User.ID when SetUser(true), got empty (tier=identified must carry a user tag)")
	}

	// identified=false (tier == TierAnonymous, or identified downgraded
	// for not being signed into fleet) must clear it.
	sentry.SetUser(false)
	event = applyScopeToFreshEvent(t)
	if event.User.ID != "" {
		t.Fatalf("AC-029: expected an empty User.ID when SetUser(false), got %q (tier=anonymous must NOT carry a user tag)", event.User.ID)
	}
}

func applyScopeToFreshEvent(t *testing.T) *gosentry.Event {
	t.Helper()
	hub := gosentry.CurrentHub()
	scope := hub.Scope()
	event := scope.ApplyToEvent(&gosentry.Event{}, nil, hub.Client())
	if event == nil {
		t.Fatal("ApplyToEvent returned nil")
	}
	return event
}

// TestWP12_AC030_GenerateLocalReport_AppendsToCache exercises
// coresentry.GenerateLocalReport + coresentry.AppendToCache directly —
// the two primitives WP12's fix wires together — and asserts
// GetLastFive sees the result. The WIRING proof (that
// core/rpc/views/sentry.Impl.GenerateLocalReport itself, the real crash
// path, calls AppendToCache) is
// core/rpc/views/sentry/impl_test.go's TestImpl_GenerateLocalReport_
// AppendsToCache — this package cannot import that one (import cycle:
// core/rpc/views/sentry already imports core/sentry).
func TestWP12_AC030_GenerateLocalReport_AppendsToCache(t *testing.T) {
	dir := t.TempDir()
	before := sentry.GetLastFive(dir)
	if len(before) != 0 {
		t.Fatalf("setup: expected empty cache, got %d entries", len(before))
	}

	path, _, err := sentry.GenerateLocalReport(context.Background(), dir)
	if err != nil {
		t.Fatalf("GenerateLocalReport: %v", err)
	}
	sentry.AppendToCache(dir, sentry.CacheEntry{
		ID:      "test-entry",
		Kind:    "local_report",
		Summary: "test",
	})

	after := sentry.GetLastFive(dir)
	if len(after) != 1 {
		t.Fatalf("GetLastFive after AppendToCache = %d entries, want 1 (path=%s)", len(after), path)
	}
	if after[0].ID != "test-entry" {
		t.Errorf("cached entry ID = %q, want %q", after[0].ID, "test-entry")
	}
}

// TestWP12_AC031_LocalReportCarriesRealVersion.
// Mutation: restore the default (do not call SetHarnessVersion). Must
// fail (report would embed "dev").
func TestWP12_AC031_LocalReportCarriesRealVersion(t *testing.T) {
	sentry.SetHarnessVersion("v0.65.0-test")
	dir := t.TempDir()
	path, _, err := sentry.GenerateLocalReport(context.Background(), dir)
	if err != nil {
		t.Fatalf("GenerateLocalReport: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(raw), "v0.65.0-test") {
		t.Fatalf("report at %s does not contain the injected version; contents: %s", path, raw)
	}
	if strings.Contains(string(raw), `"harness_version": "dev"`) {
		t.Fatalf("report still embeds the default \"dev\" version")
	}
}
