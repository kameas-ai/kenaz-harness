package rpc

// api_settings_store_injection_test.go — upgrade-path-coverage-01PMUG01
// FR-4a/6.5: rpc.New gains a settings-store injection option
// (WithSettingsStore) so a test can hand it a hermetic store instead of
// letting it resolve settings.NewFileStoreFromEnv() -> os.UserConfigDir()
// -- the developer's real config directory. This is NOT gated on the
// core.Core parameter: even New(nil) built a live file store before this
// change (core/rpc/api.go, the settingsStore construction block just
// above narrative.SetSettingsGate).
//
// Two things need pinning:
//   - the option, when supplied, wins outright — the store New wires is
//     the exact value passed in, not a wrapper or a copy;
//   - the zero-option-set default is UNCHANGED — it still resolves
//     through NewFileStoreFromEnv. This test proves that without
//     actually touching a real developer machine by sandboxing
//     HOME/XDG_CONFIG_HOME/AppData first (the same sandboxUserDir /
//     sandboxUserConfigDir helpers api_cedar_gate_wiring_test.go
//     defines), so the "default" being asserted is the real
//     construction path, not a mock of it.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// TestNew_SettingsStoreDefaultsToFileStoreFromEnv asserts that when no
// WithSettingsStore option is given, New still routes through
// settings.NewFileStoreFromEnv() -- i.e. this WP's injection seam changes
// nothing about production's zero-option-set behaviour.
func TestNew_SettingsStoreDefaultsToFileStoreFromEnv(t *testing.T) {
	sandboxUserConfigDir(t)

	api := New(nil)
	assertSettingsStoreIsSandboxed(t, api)

	if api.settingsImpl == nil {
		t.Fatal("settingsImpl is nil")
	}
	store, ok := api.settingsImpl.Store().(*settings.FileStore)
	if !ok {
		t.Fatalf("default settings store is %T, want *settings.FileStore (settings.NewFileStoreFromEnv's concrete type)",
			api.settingsImpl.Store())
	}
	if store.Path() == "" {
		t.Fatal("default settings store has an empty Path()")
	}
}

// TestNew_WithSettingsStoreOverridesDefault asserts that WithSettingsStore
// wins over the NewFileStoreFromEnv default -- the exact injected value is
// what New wires, not a fresh store built around it. Deliberately does
// NOT sandbox HOME/XDG_CONFIG_HOME/AppData: if the override were ever
// silently ignored, this test would either build a live store pointed at
// this machine's real config dir (a bug this WP exists to prevent) or,
// at minimum, fail the pointer-identity assertion below -- either way it
// must not pass by accident.
func TestNew_WithSettingsStoreOverridesDefault(t *testing.T) {
	want := newTestStore(t)

	api := New(nil, WithSettingsStore(want))

	if api.settingsImpl == nil {
		t.Fatal("settingsImpl is nil")
	}
	got := api.settingsImpl.Store()
	if got != want {
		t.Fatalf("settingsImpl.Store() = %v, want the exact injected store %v -- WithSettingsStore was not honoured", got, want)
	}
}
