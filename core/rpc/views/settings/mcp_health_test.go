package settings_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
)

// TestMCPAutoRestart_DefaultOn verifies that a freshly-constructed API
// returns true (auto-restart enabled) before any explicit Save.
func TestMCPAutoRestart_DefaultOn(t *testing.T) {
	api := settings.NewAPI(nil) // nil → memoryStore
	got, err := api.GetMCPAutoRestart(context.Background())
	if err != nil {
		t.Fatalf("GetMCPAutoRestart: %v", err)
	}
	if !got {
		t.Error("expected default MCPAutoRestart=true, got false")
	}
}

// TestMCPAutoRestart_RoundTrip verifies that Set persists and Get reads back.
func TestMCPAutoRestart_RoundTrip(t *testing.T) {
	api := settings.NewAPI(nil)

	// Disable.
	if err := api.SetMCPAutoRestart(context.Background(), false); err != nil {
		t.Fatalf("SetMCPAutoRestart(false): %v", err)
	}
	got, err := api.GetMCPAutoRestart(context.Background())
	if err != nil {
		t.Fatalf("GetMCPAutoRestart after disable: %v", err)
	}
	if got {
		t.Error("expected MCPAutoRestart=false after SetMCPAutoRestart(false)")
	}

	// Re-enable.
	if err := api.SetMCPAutoRestart(context.Background(), true); err != nil {
		t.Fatalf("SetMCPAutoRestart(true): %v", err)
	}
	got, err = api.GetMCPAutoRestart(context.Background())
	if err != nil {
		t.Fatalf("GetMCPAutoRestart after re-enable: %v", err)
	}
	if !got {
		t.Error("expected MCPAutoRestart=true after SetMCPAutoRestart(true)")
	}
}

// TestMCPAutoRestart_SettingsAccessor verifies the MCPAutoRestart accessor
// on the Settings struct correctly inverts the Disabled field.
func TestMCPAutoRestart_SettingsAccessor(t *testing.T) {
	s := settings.Settings{}
	if !s.MCPAutoRestart() {
		t.Error("zero-value Settings should have MCPAutoRestart()=true")
	}

	s.MCPAutoRestartDisabled = true
	if s.MCPAutoRestart() {
		t.Error("MCPAutoRestartDisabled=true should give MCPAutoRestart()=false")
	}
}
