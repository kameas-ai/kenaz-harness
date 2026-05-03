package projects_test

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/projects"
)

// autonomyTestProject seeds a minimal Project so the tests can exercise
// autonomy Set/Get without colliding with name validation.
func autonomyTestProject(id string, now time.Time) projects.Project {
	return projects.Project{
		ID:        id,
		Name:      "p-" + id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestMemStore_AutonomyDefaultEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := projects.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestProject("p1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.GetAutonomyProfile(ctx, "p1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("default autonomy = %+v, want IsZero", got)
	}
}

func TestMemStore_AutonomyRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := projects.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestProject("p1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bold := autonomy.TierBold
	families := autonomy.NewFamilySet(autonomy.FamilyRead, autonomy.FamilyWrite)
	want := autonomy.Layer{
		Level: &bold,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations:       int(75),
			autonomy.KnobAutoApproveFamilies: families,
		},
	}
	if err := s.SetAutonomyProfile(ctx, "p1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err := s.GetAutonomyProfile(ctx, "p1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Level == nil || *got.Level != autonomy.TierBold {
		t.Errorf("Level = %v, want bold", got.Level)
	}
	if got.Overrides[autonomy.KnobMaxIterations] != 75 {
		t.Errorf("MaxIterations = %v, want 75", got.Overrides[autonomy.KnobMaxIterations])
	}
	gotFamilies, ok := got.Overrides[autonomy.KnobAutoApproveFamilies].(autonomy.FamilySet)
	if !ok || !gotFamilies.Equal(families) {
		t.Errorf("AutoApproveFamilies = %v, want %v",
			got.Overrides[autonomy.KnobAutoApproveFamilies], families.Sorted())
	}
}

func TestMemStore_AutonomyMissingProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := projects.NewMemoryStore()
	if _, err := s.GetAutonomyProfile(ctx, "ghost"); err == nil {
		t.Errorf("GetAutonomyProfile ghost = nil err, want ErrNotFound")
	}
	if err := s.SetAutonomyProfile(ctx, "ghost", autonomy.DefaultLayer()); err == nil {
		t.Errorf("SetAutonomyProfile ghost = nil err, want ErrNotFound")
	}
}

// TestSQLStore_AutonomyRoundTrip exercises the full persistence path
// through the SQLite-backed store: Create + Set + Get + reopen.
func TestSQLStore_AutonomyRoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, autonomyTestProject("p1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetAutonomyProfile(ctx, "p1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("default GetAutonomyProfile = %+v, want IsZero", got)
	}
	bold := autonomy.TierBold
	want := autonomy.Layer{
		Level: &bold,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations:       int(75),
			autonomy.KnobTokenCeilingPerTurn: int(262144),
		},
	}
	if err := store.SetAutonomyProfile(ctx, "p1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err = store.GetAutonomyProfile(ctx, "p1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
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
}

// TestSQLStore_AutonomyClearViaEmptyLayer pins that writing the empty
// Layer back to a project that previously had a profile clears both
// columns to NULL — the project-panel's "Reset to global" button maps
// onto this contract.
func TestSQLStore_AutonomyClearViaEmptyLayer(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, autonomyTestProject("p1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bold := autonomy.TierBold
	if err := store.SetAutonomyProfile(ctx, "p1", autonomy.Layer{Level: &bold}); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	if err := store.SetAutonomyProfile(ctx, "p1", autonomy.Layer{}); err != nil {
		t.Fatalf("SetAutonomyProfile clear: %v", err)
	}
	got, err := store.GetAutonomyProfile(ctx, "p1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("autonomy after clear = %+v, want IsZero", got)
	}
}

func TestSQLStore_AutonomyMissingProject(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	if _, err := store.GetAutonomyProfile(ctx, "ghost"); err == nil {
		t.Errorf("GetAutonomyProfile ghost = nil err, want ErrNotFound")
	}
	if err := store.SetAutonomyProfile(ctx, "ghost", autonomy.DefaultLayer()); err == nil {
		t.Errorf("SetAutonomyProfile ghost = nil err, want ErrNotFound")
	}
}
