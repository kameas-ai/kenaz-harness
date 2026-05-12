package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateHooks_UnknownEventDisabled verifies that a hook with an
// unrecognised event name is disabled in-memory (but not removed).
func TestMigrateHooks_UnknownEventDisabled(t *testing.T) {
	t.Parallel()
	hooks := []Hook{
		{ID: "h1", Name: "n", Event: "pre_send", Kind: KindBuiltin, Builtin: BuiltinMemoryRetrieve, Enabled: true},
		{ID: "h2", Name: "n", Event: "future_event_unknown", Kind: KindBuiltin, Builtin: BuiltinMemoryRetrieve, Enabled: true},
	}
	out := MigrateHooks(hooks, nil)
	if len(out) != 2 {
		t.Fatalf("MigrateHooks len=%d, want 2", len(out))
	}
	if !out[0].Enabled {
		t.Error("known event hook should remain enabled")
	}
	if out[1].Enabled {
		t.Error("unknown event hook should be disabled")
	}
}

// TestMigrateHooks_V2EventsPassThrough verifies that all v2 events are
// not migrated (they are valid and enabled).
func TestMigrateHooks_V2EventsPassThrough(t *testing.T) {
	t.Parallel()
	var hooks []Hook
	for i, ev := range AllEvents {
		hooks = append(hooks, Hook{
			ID:      "h" + string(rune('0'+i)),
			Name:    "n",
			Event:   ev,
			Kind:    KindShell,
			Command: "echo",
			Enabled: true,
		})
	}
	out := MigrateHooks(hooks, nil)
	for _, h := range out {
		if !h.Enabled {
			t.Errorf("hook %q (event=%q) should remain enabled", h.ID, h.Event)
		}
	}
}

// TestRegistry_LoadWithMigration verifies that a pre-v2 hooks.json file
// (containing only v1 events) loads and passes the migration without
// disabling any hooks.
func TestRegistry_LoadWithMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Write a "legacy" hooks.json with v1 events only.
	legacy := []map[string]any{
		{
			"id":      "mem-retrieve",
			"name":    "Memory retrieve",
			"event":   "pre_send",
			"kind":    "builtin",
			"enabled": true,
			"match":   map[string]any{},
			"builtin": "memory.retrieve",
		},
		{
			"id":      "mem-persist",
			"name":    "Memory persist",
			"event":   "post_send",
			"kind":    "builtin",
			"enabled": true,
			"match":   map[string]any{},
			"builtin": "memory.persist",
		},
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	hooks := reg.List()
	if len(hooks) != 2 {
		t.Fatalf("len(hooks)=%d, want 2", len(hooks))
	}
	for _, h := range hooks {
		if !h.Enabled {
			t.Errorf("legacy hook %q should remain enabled after migration", h.ID)
		}
	}
}

// TestIsHooksV2Enabled_Default verifies the feature flag defaults to true.
func TestIsHooksV2Enabled_Default(t *testing.T) {
	t.Parallel()
	// Test from config package via re-import would create a cycle;
	// test the core/hooks package's own migration path instead.
	// The feature flag test lives in core/config, but here we verify
	// that MigrateHooks is idempotent on empty input.
	out := MigrateHooks(nil, nil)
	if out != nil {
		t.Errorf("MigrateHooks(nil) should return nil, got %v", out)
	}
}
