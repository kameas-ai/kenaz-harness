package settings

import (
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
)

// TestFileStore_AutonomyDefaultEmpty pins that a fresh-install
// settings.json (or a missing file) returns the empty Layer — the
// resolver then folds this layer through to the tier-default.
func TestFileStore_AutonomyDefaultEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadAutonomyProfile()
	if err != nil {
		t.Fatalf("LoadAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("default LoadAutonomyProfile = %+v, want IsZero", got)
	}
}

// TestFileStore_AutonomyRoundTrip exercises SaveAutonomyProfile +
// LoadAutonomyProfile through the on-disk JSON file: a non-empty Layer
// must round-trip with stable Level + Overrides values.
func TestFileStore_AutonomyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bold := autonomy.TierBold
	families := autonomy.NewFamilySet(autonomy.FamilyRead, autonomy.FamilyWrite)
	want := autonomy.Layer{
		Level: &bold,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations:       int(75),
			autonomy.KnobTokenCeilingPerTurn: int(262144),
			autonomy.KnobAutoApproveFamilies: families,
		},
	}
	if err := store.SaveAutonomyProfile(want); err != nil {
		t.Fatalf("SaveAutonomyProfile: %v", err)
	}
	got, err := store.LoadAutonomyProfile()
	if err != nil {
		t.Fatalf("LoadAutonomyProfile: %v", err)
	}
	if got.Level == nil || *got.Level != autonomy.TierBold {
		t.Errorf("Level = %v, want bold", got.Level)
	}
	if got.Overrides[autonomy.KnobMaxIterations] != 75 {
		t.Errorf("MaxIterations = %v, want 75", got.Overrides[autonomy.KnobMaxIterations])
	}
	if got.Overrides[autonomy.KnobTokenCeilingPerTurn] != 262144 {
		t.Errorf("TokenCeilingPerTurn = %v, want 262144", got.Overrides[autonomy.KnobTokenCeilingPerTurn])
	}
	gotFamilies, ok := got.Overrides[autonomy.KnobAutoApproveFamilies].(autonomy.FamilySet)
	if !ok || !gotFamilies.Equal(families) {
		t.Errorf("AutoApproveFamilies = %v, want %v",
			got.Overrides[autonomy.KnobAutoApproveFamilies], families.Sorted())
	}
}

// TestFileStore_AutonomyClearViaEmptyLayer pins that overwriting a
// previously-stored Layer with the empty Layer clears the field on
// disk so the next load returns the zero Layer (resolver fallback).
func TestFileStore_AutonomyClearViaEmptyLayer(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bold := autonomy.TierBold
	if err := store.SaveAutonomyProfile(autonomy.Layer{Level: &bold}); err != nil {
		t.Fatalf("SaveAutonomyProfile bold: %v", err)
	}
	if err := store.SaveAutonomyProfile(autonomy.Layer{}); err != nil {
		t.Fatalf("SaveAutonomyProfile clear: %v", err)
	}
	got, err := store.LoadAutonomyProfile()
	if err != nil {
		t.Fatalf("LoadAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("after clear LoadAutonomyProfile = %+v, want IsZero", got)
	}
}

// TestMemoryStore_AutonomyRoundTrip exercises the same contract on the
// in-memory SettingsStore used by tests + the API fallback.
func TestMemoryStore_AutonomyRoundTrip(t *testing.T) {
	store := newMemoryStore()
	got, err := store.LoadAutonomyProfile()
	if err != nil {
		t.Fatalf("LoadAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("default = %+v, want IsZero", got)
	}
	strict := autonomy.TierStrict
	want := autonomy.Layer{
		Level: &strict,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations: int(5),
		},
	}
	if err := store.SaveAutonomyProfile(want); err != nil {
		t.Fatalf("SaveAutonomyProfile: %v", err)
	}
	got, err = store.LoadAutonomyProfile()
	if err != nil {
		t.Fatalf("LoadAutonomyProfile: %v", err)
	}
	if got.Level == nil || *got.Level != autonomy.TierStrict {
		t.Errorf("Level = %v, want strict", got.Level)
	}
	if got.Overrides[autonomy.KnobMaxIterations] != 5 {
		t.Errorf("MaxIterations = %v, want 5", got.Overrides[autonomy.KnobMaxIterations])
	}
}
