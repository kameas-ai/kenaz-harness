package fleet

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// ── KindRegistry ──────────────────────────────────────────────────────────

func validKind(id string) SyncKind {
	return SyncKind{
		ID:             id,
		Scopes:         []Scope{ScopeUser},
		Transport:      TransportLWWCategory,
		Collect:        func(context.Context) ([]byte, error) { return []byte(`{}`), nil },
		Apply:          func(context.Context, Scope, []byte) error { return nil },
		SecretPolicy:   SecretPolicyMustNotContainSecrets,
		ConflictPolicy: ConflictPolicyLWW,
	}
}

func TestKindRegistry_RegisterAndLookup(t *testing.T) {
	r := NewKindRegistry()
	if err := r.Register(validKind("provider_profiles")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Kind("provider_profiles")
	if !ok {
		t.Fatal("expected kind to be found")
	}
	if got.ID != "provider_profiles" {
		t.Errorf("ID = %q, want provider_profiles", got.ID)
	}
	if _, ok := r.Kind("nope"); ok {
		t.Error("expected unregistered kind to be absent")
	}
}

func TestKindRegistry_DuplicateRejected(t *testing.T) {
	r := NewKindRegistry()
	if err := r.Register(validKind("dup")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(validKind("dup")); err == nil {
		t.Error("expected duplicate registration to fail")
	}
}

func TestKindRegistry_ValidationErrors(t *testing.T) {
	base := validKind("x")

	cases := []struct {
		name   string
		mutate func(k SyncKind) SyncKind
	}{
		{"empty ID", func(k SyncKind) SyncKind { k.ID = ""; return k }},
		{"no scopes", func(k SyncKind) SyncKind { k.Scopes = nil; return k }},
		{"no transport", func(k SyncKind) SyncKind { k.Transport = ""; return k }},
		{"no secret policy", func(k SyncKind) SyncKind { k.SecretPolicy = ""; return k }},
		{"no conflict policy", func(k SyncKind) SyncKind { k.ConflictPolicy = ""; return k }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewKindRegistry()
			if err := r.Register(tc.mutate(base)); err == nil {
				t.Errorf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestKindRegistry_KindsSortedByID(t *testing.T) {
	r := NewKindRegistry()
	for _, id := range []string{"zeta", "alpha", "mu"} {
		if err := r.Register(validKind(id)); err != nil {
			t.Fatalf("Register(%s): %v", id, err)
		}
	}
	kinds := r.Kinds()
	if len(kinds) != 3 {
		t.Fatalf("len(Kinds()) = %d, want 3", len(kinds))
	}
	want := []string{"alpha", "mu", "zeta"}
	for i, k := range kinds {
		if k.ID != want[i] {
			t.Errorf("Kinds()[%d].ID = %q, want %q", i, k.ID, want[i])
		}
	}
	ids := r.IDs()
	for i, id := range ids {
		if id != want[i] {
			t.Errorf("IDs()[%d] = %q, want %q", i, id, want[i])
		}
	}
}

func TestSyncKind_HasScope(t *testing.T) {
	k := SyncKind{Scopes: []Scope{ScopeUser, ScopeOrg}}
	if !k.HasScope(ScopeUser) {
		t.Error("expected HasScope(ScopeUser) = true")
	}
	if !k.HasScope(ScopeOrg) {
		t.Error("expected HasScope(ScopeOrg) = true")
	}
	if k.HasScope(ScopeTeam) {
		t.Error("expected HasScope(ScopeTeam) = false")
	}
}

// ── SyncKind.CategoryConfig adapter ──────────────────────────────────────

func TestSyncKind_CategoryConfig_CollectAdapter(t *testing.T) {
	k := SyncKind{
		ID: "widget",
		Collect: func(context.Context) ([]byte, error) {
			return []byte(`{"n":1}`), nil
		},
		SecretPolicy:   SecretPolicyMustNotContainSecrets,
		ConflictPolicy: ConflictPolicyLWW,
	}
	cfg := k.CategoryConfig()
	if cfg.Collector == nil {
		t.Fatal("expected Collector to be wired")
	}
	raw, err := cfg.Collector(context.Background())
	if err != nil {
		t.Fatalf("Collector: %v", err)
	}
	if string(raw) != `{"n":1}` {
		t.Errorf("Collector output = %s, want {\"n\":1}", raw)
	}
}

func TestSyncKind_CategoryConfig_ApplyAdapter_UsesScopeUser(t *testing.T) {
	var gotScope Scope
	var gotPayload []byte
	k := SyncKind{
		ID: "widget",
		Apply: func(_ context.Context, scope Scope, payload []byte) error {
			gotScope = scope
			gotPayload = payload
			return nil
		},
		SecretPolicy:   SecretPolicyMustNotContainSecrets,
		ConflictPolicy: ConflictPolicyLWW,
	}
	cfg := k.CategoryConfig()
	if cfg.Applier == nil {
		t.Fatal("expected Applier to be wired")
	}
	if err := cfg.Applier(context.Background(), json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatalf("Applier: %v", err)
	}
	if gotScope != ScopeUser {
		t.Errorf("Apply scope = %q, want %q (LWW transport is per-user)", gotScope, ScopeUser)
	}
	if string(gotPayload) != `{"n":2}` {
		t.Errorf("Apply payload = %s, want {\"n\":2}", gotPayload)
	}
}

func TestSyncKind_CategoryConfig_NilCollectApply(t *testing.T) {
	k := SyncKind{ID: "collect-only", Collect: func(context.Context) ([]byte, error) { return nil, nil }}
	cfg := k.CategoryConfig()
	if cfg.Collector == nil {
		t.Error("expected Collector to be wired")
	}
	if cfg.Applier != nil {
		t.Error("expected Applier to be nil when SyncKind.Apply is nil")
	}
}

// ── Syncer genericity: a kind registered outside AllSyncCategories() works ──

// TestSyncer_RegisterCategory_UnknownCategoryIsGeneric proves the sync.go
// fix backing WP05: a SyncCategory that is NOT one of the five built-ins
// (AllSyncCategories()) still works end-to-end — SetEnabled/Push/Pull/Status
// — once registered, because RegisterCategory now lazily creates its
// CategoryState instead of relying on NewSyncer's fixed pre-population.
func TestSyncer_RegisterCategory_UnknownCategoryIsGeneric(t *testing.T) {
	fake := newFakeSyncServerSimple()
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-generic",
		RefreshToken: "rt-generic",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	syncer := NewSyncer(c)
	t.Cleanup(syncer.Stop)

	const customCat SyncCategory = "slash_commands"

	// Not one of the five built-ins.
	isBuiltin := false
	for _, cat := range AllSyncCategories() {
		if cat == customCat {
			isBuiltin = true
		}
	}
	if isBuiltin {
		t.Fatalf("%q unexpectedly already a built-in category", customCat)
	}

	payload := json.RawMessage(`{"commands":[]}`)
	applied := make(chan json.RawMessage, 1)
	syncer.RegisterCategory(customCat, CategoryConfig{
		Collector: func(context.Context) (json.RawMessage, error) { return payload, nil },
		Applier: func(_ context.Context, raw json.RawMessage) error {
			applied <- raw
			return nil
		},
	})

	// SetEnabled triggers an immediate push — must not error with "unknown
	// category" the way it would have before the RegisterCategory fix.
	if err := syncer.SetEnabled(context.Background(), customCat, true); err != nil {
		t.Fatalf("SetEnabled(%q): %v", customCat, err)
	}
	stored, ok := fake.get(customCat)
	if !ok {
		t.Fatal("expected server to have received the push for the custom category")
	}
	if string(stored.Payload) != string(payload) {
		t.Errorf("pushed payload = %s, want %s", stored.Payload, payload)
	}

	// Status must include the custom category too.
	found := false
	for _, st := range syncer.Status() {
		if st.Category == customCat {
			found = true
			if !st.Enabled {
				t.Error("expected custom category to report Enabled=true in Status()")
			}
		}
	}
	if !found {
		t.Error("expected Status() to include the custom category")
	}
}
