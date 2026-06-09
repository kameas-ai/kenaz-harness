package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── fake sync server ──────────────────────────────────────────────────────────

// fakeSyncServerSimple is a simple in-memory per-category state store that mirrors
// the fleet /api/v1/sync/{category} shape.
type fakeSyncServerSimple struct {
	mu    sync.Mutex
	state map[SyncCategory]SyncPayload
}

func newFakeSyncServerSimple() *fakeSyncServerSimple {
	return &fakeSyncServerSimple{state: make(map[SyncCategory]SyncPayload)}
}

func (f *fakeSyncServerSimple) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/sync/"
	if len(r.URL.Path) <= len(prefix) {
		http.NotFound(w, r)
		return
	}
	cat := SyncCategory(r.URL.Path[len(prefix):])

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var p SyncPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.state[cat] = p
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case http.MethodGet:
		f.mu.Lock()
		stored, ok := f.state[cat]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stored)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (f *fakeSyncServerSimple) get(cat SyncCategory) (SyncPayload, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.state[cat]
	return p, ok
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestSync_PushAndPull(t *testing.T) {
	fake := newFakeSyncServerSimple()
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-sync",
		RefreshToken: "rt-sync",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	syncer := NewSyncer(c)

	// Register a simple theme category collector.
	themePayload := json.RawMessage(`{"theme":"dark"}`)
	syncer.RegisterCategory(SyncCategoryUITheme, CategoryConfig{
		Collector: func(_ context.Context) (json.RawMessage, error) {
			return themePayload, nil
		},
		Applier: func(_ context.Context, raw json.RawMessage) error {
			return nil
		},
	})

	// Enable triggers immediate push.
	if err := syncer.SetEnabled(context.Background(), SyncCategoryUITheme, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	// Verify the server received the push.
	stored, ok := fake.get(SyncCategoryUITheme)
	if !ok {
		t.Fatal("expected server to have received push")
	}
	if string(stored.Payload) != `{"theme":"dark"}` {
		t.Errorf("server payload = %s, want dark theme", stored.Payload)
	}

	// Now pull on a new syncer (simulating a second device).
	var pulled json.RawMessage
	var pullMu sync.Mutex
	syncer2 := NewSyncer(c)
	syncer2.RegisterCategory(SyncCategoryUITheme, CategoryConfig{
		Collector: func(_ context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"theme":"light"}`), nil
		},
		Applier: func(_ context.Context, raw json.RawMessage) error {
			pullMu.Lock()
			defer pullMu.Unlock()
			pulled = raw
			return nil
		},
	})
	syncer2.categories[SyncCategoryUITheme].enabled = true // bypass SetEnabled to skip push

	if err := syncer2.Pull(context.Background(), SyncCategoryUITheme); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	pullMu.Lock()
	defer pullMu.Unlock()
	if pulled == nil {
		t.Error("expected applier to have been called")
	}
}

func TestSync_LWW_RemoteOlderSkipped(t *testing.T) {
	fake := newFakeSyncServerSimple()
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-sync",
		RefreshToken: "rt-sync",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	syncer := NewSyncer(c)

	applyCalled := 0
	syncer.RegisterCategory(SyncCategoryModelPrefs, CategoryConfig{
		Collector: func(_ context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"model":"opus"}`), nil
		},
		Applier: func(_ context.Context, raw json.RawMessage) error {
			applyCalled++
			return nil
		},
	})
	syncer.categories[SyncCategoryModelPrefs].enabled = true

	// Push to server sets lwwTS.
	if err := syncer.Push(context.Background(), SyncCategoryModelPrefs); err != nil {
		t.Fatalf("Push: %v", err)
	}
	// Manually set the local LWW to a future time so the remote appears older.
	syncer.categories[SyncCategoryModelPrefs].mu.Lock()
	syncer.categories[SyncCategoryModelPrefs].lwwTS = time.Now().Add(time.Hour).UnixNano()
	syncer.categories[SyncCategoryModelPrefs].mu.Unlock()

	// Pull should skip (remote older than local).
	if err := syncer.Pull(context.Background(), SyncCategoryModelPrefs); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if applyCalled > 0 {
		t.Error("applier should not be called when remote is older")
	}
}

func TestSync_DisabledCategorySkipped(t *testing.T) {
	fake := newFakeSyncServerSimple()
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-sync",
		RefreshToken: "rt-sync",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	syncer := NewSyncer(c)

	collected := 0
	syncer.RegisterCategory(SyncCategoryProviderProfiles, CategoryConfig{
		Collector: func(_ context.Context) (json.RawMessage, error) {
			collected++
			return json.RawMessage(`[]`), nil
		},
	})
	// Category is disabled by default; explicit push should be a no-op.
	if err := syncer.Push(context.Background(), SyncCategoryProviderProfiles); err != nil {
		t.Fatalf("unexpected Push error: %v", err)
	}
	if collected > 0 {
		t.Error("collector should not be called on disabled category")
	}
}

func TestSync_SecretExclusion(t *testing.T) {
	// Verify MCPSyncCategory.Collect strips secret keys from EnvOverrides.
	secretKeys := map[string]bool{"API_KEY": true, "SECRET_TOKEN": true}
	pending := &SecretPromptQueue{}
	mcp := NewMCPSyncCategory(nil, nil, secretKeys, pending)

	// Feed via a mock reader.
	items := []InstalledMCP{
		{
			ID:           "mcp-1",
			RecipeID:     "github",
			EnabledState: true,
			EnvOverrides: map[string]string{
				"API_KEY":       "super-secret-value", // must be stripped
				"CUSTOM_URL":    "https://api.example.com", // must survive
				"SECRET_TOKEN":  "another-secret", // must be stripped
			},
			RequiresSecretKeys: []string{"API_KEY", "SECRET_TOKEN"},
		},
	}

	// Inject a reader.
	mcp.Reader = &mockMCPReader{items: items}

	raw, err := mcp.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var p InstalledMCPPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(p.Items))
	}
	got := p.Items[0]
	if _, secretPresent := got.EnvOverrides["API_KEY"]; secretPresent {
		t.Error("API_KEY must be stripped from synced payload")
	}
	if _, secretPresent := got.EnvOverrides["SECRET_TOKEN"]; secretPresent {
		t.Error("SECRET_TOKEN must be stripped from synced payload")
	}
	if got.EnvOverrides["CUSTOM_URL"] != "https://api.example.com" {
		t.Errorf("CUSTOM_URL should be preserved, got %q", got.EnvOverrides["CUSTOM_URL"])
	}

	// Ensure no credential bytes appear in the raw JSON output.
	rawStr := string(raw)
	for _, secret := range []string{"super-secret-value", "another-secret"} {
		if strings.Contains(rawStr, secret) {
			t.Errorf("secret %q must not appear in synced payload", secret)
		}
	}
}

func TestSync_PendingSecretQueue(t *testing.T) {
	// Verify that Apply enqueues MCPs needing secrets.
	pending := &SecretPromptQueue{}
	mcp := NewMCPSyncCategory(nil, nil, nil, pending)

	incoming := InstalledMCPPayload{
		Items: []InstalledMCP{
			{ID: "mcp-a", RecipeID: "slack", RequiresSecretKeys: []string{"SLACK_TOKEN"}},
			{ID: "mcp-b", RecipeID: "brave", RequiresSecretKeys: nil}, // no secrets
		},
	}
	raw, _ := json.Marshal(incoming)
	if err := mcp.Apply(context.Background(), raw); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	snap := pending.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(snap))
	}
	if snap[0].ID != "mcp-a" {
		t.Errorf("pending MCP = %q, want mcp-a", snap[0].ID)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type mockMCPReader struct {
	items []InstalledMCP
}

func (m *mockMCPReader) ListInstalled() ([]InstalledMCP, error) {
	return m.items, nil
}
