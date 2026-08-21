package settings

// wp21_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-16 / WP21. FR-032 (SD-14): MaxGeneratedImageBytes and
// LocalRuntimeRAMOverrideGB both claimed "rejected at Save" in their
// struct comments while SaveAll ran no validator touching either
// field. Fixed by adding both checks to validateCompactionFields,
// which BOTH FileStore.SaveAll and memoryStore.SaveAll call — so this
// file drives both stores explicitly (spec AC-PI-2 / blind spot #2:
// state which SaveAll a settings test touches).

import (
	"errors"
	"testing"
)

// TestSaveAll_RejectsNegativeMaxGeneratedImageBytes_FileStore is
// AC(-like), FR-032, driven against FileStore.SaveAll.
//
// Mutation: remove the `in.MaxGeneratedImageBytes < 0` check from
// validateCompactionFields. Must fail.
func TestSaveAll_RejectsNegativeMaxGeneratedImageBytes_FileStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	err = store.SaveAll(Settings{MaxGeneratedImageBytes: -1})
	if !errors.Is(err, ErrInvalidMaxGeneratedImageBytes) {
		t.Errorf("SaveAll(negative) err = %v, want ErrInvalidMaxGeneratedImageBytes", err)
	}
	// Zero and positive values must still round-trip.
	if err := store.SaveAll(Settings{MaxGeneratedImageBytes: 5 * 1024 * 1024}); err != nil {
		t.Errorf("SaveAll(positive) err = %v, want nil", err)
	}
}

// TestSaveAll_RejectsNegativeMaxGeneratedImageBytes_MemoryStore drives
// the SAME check through memoryStore.SaveAll — the store token-cost /
// context tests exercise, and the one blind spot #2 warns runs only
// two of FileStore's four validators. Proves this specific fix reaches
// both, not just the one a careless test would drive.
func TestSaveAll_RejectsNegativeMaxGeneratedImageBytes_MemoryStore(t *testing.T) {
	store := &memoryStore{}
	err := store.SaveAll(Settings{MaxGeneratedImageBytes: -1})
	if !errors.Is(err, ErrInvalidMaxGeneratedImageBytes) {
		t.Errorf("memoryStore.SaveAll(negative) err = %v, want ErrInvalidMaxGeneratedImageBytes", err)
	}
}

// TestSaveAll_RejectsNegativeLocalRuntimeRAMOverrideGB_FileStore is
// FR-032's second field, driven against FileStore.SaveAll.
func TestSaveAll_RejectsNegativeLocalRuntimeRAMOverrideGB_FileStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	err = store.SaveAll(Settings{LocalRuntimeRAMOverrideGB: -0.5})
	if !errors.Is(err, ErrInvalidLocalRuntimeRAMOverrideGB) {
		t.Errorf("SaveAll(negative) err = %v, want ErrInvalidLocalRuntimeRAMOverrideGB", err)
	}
	if err := store.SaveAll(Settings{LocalRuntimeRAMOverrideGB: 12.5}); err != nil {
		t.Errorf("SaveAll(positive) err = %v, want nil", err)
	}
}

// TestSaveAll_RejectsNegativeLocalRuntimeRAMOverrideGB_MemoryStore is
// the memoryStore twin of the test above.
func TestSaveAll_RejectsNegativeLocalRuntimeRAMOverrideGB_MemoryStore(t *testing.T) {
	store := &memoryStore{}
	err := store.SaveAll(Settings{LocalRuntimeRAMOverrideGB: -0.5})
	if !errors.Is(err, ErrInvalidLocalRuntimeRAMOverrideGB) {
		t.Errorf("memoryStore.SaveAll(negative) err = %v, want ErrInvalidLocalRuntimeRAMOverrideGB", err)
	}
}
