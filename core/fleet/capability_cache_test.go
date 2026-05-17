package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCapabilitiesPath(t *testing.T) {
	got := capabilitiesPath("/data")
	want := "/data/fleet/capabilities.json"
	if got != want {
		t.Errorf("capabilitiesPath = %q, want %q", got, want)
	}
}

func TestLoadCapabilities_MissingFile(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadCapabilities(dir)
	if err != nil {
		t.Fatalf("LoadCapabilities missing file = error %v, want nil", err)
	}
	if c.Source != "" {
		t.Errorf("Source = %q, want empty for absent file", c.Source)
	}
	if c.Tier != "" {
		t.Errorf("Tier = %q, want empty for absent file", c.Tier)
	}
}

func TestLoadCapabilities_EmptyDataDir(t *testing.T) {
	c, err := LoadCapabilities("")
	if err != nil {
		t.Fatalf("LoadCapabilities empty dir = error %v, want nil", err)
	}
	if c.Source != "" {
		t.Errorf("Source = %q, want empty", c.Source)
	}
}

func TestLoadCapabilities_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := capabilitiesPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("this is not valid json!!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCapabilities(dir)
	if err != nil {
		t.Fatalf("LoadCapabilities corrupt = error %v, want nil", err)
	}
	// Corrupt → zero-value returned (not the corrupt data).
	if c.Tier != "" || c.Enabled != nil {
		t.Errorf("corrupt: got non-zero Capabilities %+v", c)
	}
}

func TestSaveAndLoadCapabilities_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second) // JSON round-trips to second precision

	original := Capabilities{
		Tier: "enterprise",
		Enabled: map[Capability]bool{
			CapHostedInference:   true,
			CapSharedTeamGraph:   false,
			CapOPACustomRego:     true,
			CapSSOSAML:           true,
		},
		FetchedAt: now,
		Source:    "fleet",
	}

	if err := SaveCapabilities(dir, original); err != nil {
		t.Fatalf("SaveCapabilities = %v", err)
	}

	loaded, err := LoadCapabilities(dir)
	if err != nil {
		t.Fatalf("LoadCapabilities = %v", err)
	}

	// Source is overwritten to "cache" on load.
	if loaded.Source != "cache" {
		t.Errorf("Source = %q, want 'cache'", loaded.Source)
	}
	if loaded.Tier != original.Tier {
		t.Errorf("Tier = %q, want %q", loaded.Tier, original.Tier)
	}
	if !loaded.FetchedAt.Equal(original.FetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", loaded.FetchedAt, original.FetchedAt)
	}
	for k, wantV := range original.Enabled {
		if gotV := loaded.Enabled[k]; gotV != wantV {
			t.Errorf("Enabled[%q] = %v, want %v", k, gotV, wantV)
		}
	}
}

func TestSaveCapabilities_AtomicWrite(t *testing.T) {
	// Verify that a .tmp file is used: after save, no .tmp remains.
	dir := t.TempDir()
	c := Capabilities{
		Tier:      "pro",
		Enabled:   map[Capability]bool{CapHostedInference: true},
		FetchedAt: time.Now(),
	}
	if err := SaveCapabilities(dir, c); err != nil {
		t.Fatalf("SaveCapabilities = %v", err)
	}
	// The canonical file must exist.
	if _, err := os.Stat(capabilitiesPath(dir)); err != nil {
		t.Errorf("canonical file missing after save: %v", err)
	}
	// The .tmp file must NOT exist.
	tmpPath := capabilitiesPath(dir) + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf(".tmp file still present after save (err=%v)", err)
	}
}

func TestSaveCapabilities_EmptyDataDir(t *testing.T) {
	// Empty dataDir must be a no-op (no error, no panic).
	c := Capabilities{Tier: "pro"}
	if err := SaveCapabilities("", c); err != nil {
		t.Errorf("SaveCapabilities empty dir = %v, want nil", err)
	}
}

func TestLoadCapabilities_PreservesTimeZone(t *testing.T) {
	dir := t.TempDir()
	// Use a time with a non-UTC location to verify zone round-trips.
	// json.Marshal always emits RFC3339 (UTC equivalent), so we
	// normalise to UTC for comparison after the round-trip.
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("time zone data not available")
	}
	original := time.Now().In(loc).Truncate(time.Second)
	c := Capabilities{
		Tier:      "team",
		FetchedAt: original,
		Enabled:   map[Capability]bool{},
	}
	if err := SaveCapabilities(dir, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCapabilities(dir)
	if err != nil {
		t.Fatal(err)
	}
	// After JSON round-trip, both should represent the same instant in time.
	if !loaded.FetchedAt.Equal(original) {
		t.Errorf("FetchedAt: %v != %v (after round-trip)", loaded.FetchedAt, original)
	}
}

// TestSaveCapabilities_FileModePermissions verifies the file is written 0o600.
func TestSaveCapabilities_FileModePermissions(t *testing.T) {
	dir := t.TempDir()
	c := Capabilities{Tier: "pro", Enabled: map[Capability]bool{}, FetchedAt: time.Now()}
	if err := SaveCapabilities(dir, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(capabilitiesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

// TestLoadCapabilities_AllFieldsPreserved saves and loads using all
// 25 capability keys and verifies none are dropped.
func TestLoadCapabilities_AllFieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	enabled := make(map[Capability]bool)
	for i, cap := range AllCapabilities() {
		enabled[cap] = (i%2 == 0) // alternating true/false
	}
	c := Capabilities{
		Tier:      "enterprise",
		Enabled:   enabled,
		FetchedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := SaveCapabilities(dir, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCapabilities(dir)
	if err != nil {
		t.Fatal(err)
	}
	for k, wantV := range enabled {
		if gotV := loaded.Enabled[k]; gotV != wantV {
			t.Errorf("Enabled[%q] = %v, want %v", k, gotV, wantV)
		}
	}
}

// TestLoadCapabilities_BadPermissions simulates a read-permission error.
// Runs only on non-root systems.
func TestLoadCapabilities_BadPermissions(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission errors don't apply")
	}
	dir := t.TempDir()
	path := capabilitiesPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// Write a valid file, then chmod it unreadable.
	c := Capabilities{Tier: "pro", FetchedAt: time.Now(), Enabled: map[Capability]bool{}}
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(path, 0o600) }()

	loaded, err := LoadCapabilities(dir)
	if err != nil {
		t.Fatalf("LoadCapabilities permission error = %v, want nil", err)
	}
	// Should return zero-value Capabilities (treated as absent).
	if loaded.Tier != "" {
		t.Errorf("got Tier %q from unreadable file, want empty", loaded.Tier)
	}
}
