package session_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// autonomyTestRecord is a minimal Record used to seed Create() in the
// store-autonomy tests. The autonomy fields are left zero so the test
// drives them via SetAutonomyProfile, mirroring how the manager will
// actually use the API.
func autonomyTestRecord(id string, now time.Time) session.Record {
	return session.Record{
		ID:           id,
		Name:         "autonomy-" + id,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		ContextKind:  session.ContextKindSystem,
	}
}

// memStore + sqlStore round-trip for all four cases the WP02 spec
// requires:
//
//   - empty layer (default — both columns NULL)
//   - level only (Level=Bold, no overrides)
//   - overrides only (Level=nil, partial overrides)
//   - level + overrides (both set)
//
// For SQL parity we also verify the round-trip through the actual
// SQLite-backed store so the Scan / Insert / Update path is exercised
// end-to-end.

func TestMemStore_AutonomyRoundTrip_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := session.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("default GetAutonomyProfile = %+v, want IsZero", got)
	}
}

func TestMemStore_AutonomyRoundTrip_LevelOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := session.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bold := autonomy.TierBold
	want := autonomy.Layer{Level: &bold}
	if err := s.SetAutonomyProfile(ctx, "s1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err := s.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Level == nil || *got.Level != autonomy.TierBold {
		t.Errorf("got Level = %v, want bold", got.Level)
	}
	if len(got.Overrides) != 0 {
		t.Errorf("got Overrides = %v, want empty", got.Overrides)
	}
}

func TestMemStore_AutonomyRoundTrip_OverridesOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := session.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := autonomy.Layer{
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations:       int(75),
			autonomy.KnobTokenCeilingPerTurn: int(262144),
		},
	}
	if err := s.SetAutonomyProfile(ctx, "s1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err := s.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Level != nil {
		t.Errorf("got Level = %v, want nil", got.Level)
	}
	if got.Overrides[autonomy.KnobMaxIterations] != 75 {
		t.Errorf("MaxIterations = %v, want 75", got.Overrides[autonomy.KnobMaxIterations])
	}
	if got.Overrides[autonomy.KnobTokenCeilingPerTurn] != 262144 {
		t.Errorf("TokenCeilingPerTurn = %v, want 262144", got.Overrides[autonomy.KnobTokenCeilingPerTurn])
	}
}

func TestMemStore_AutonomyRoundTrip_LevelAndOverrides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := session.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	strict := autonomy.TierStrict
	families := autonomy.NewFamilySet(autonomy.FamilyRead, autonomy.FamilyWrite)
	want := autonomy.Layer{
		Level: &strict,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations:       int(10),
			autonomy.KnobAskOnAmbiguity:      autonomy.AskHard,
			autonomy.KnobAutoApproveFamilies: families,
			autonomy.KnobRecapStyle:          autonomy.RecapBrief,
			autonomy.KnobContinueOnError:     autonomy.ErrorRetryOnce,
			autonomy.KnobDestructiveActionPosture: autonomy.DestructiveConfirm,
		},
	}
	if err := s.SetAutonomyProfile(ctx, "s1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err := s.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Level == nil || *got.Level != autonomy.TierStrict {
		t.Errorf("Level = %v, want strict", got.Level)
	}
	if got.Overrides[autonomy.KnobMaxIterations] != 10 {
		t.Errorf("MaxIterations = %v, want 10", got.Overrides[autonomy.KnobMaxIterations])
	}
	if got.Overrides[autonomy.KnobAskOnAmbiguity] != autonomy.AskHard {
		t.Errorf("AskOnAmbiguity = %v, want %v", got.Overrides[autonomy.KnobAskOnAmbiguity], autonomy.AskHard)
	}
	gotFamilies, ok := got.Overrides[autonomy.KnobAutoApproveFamilies].(autonomy.FamilySet)
	if !ok {
		t.Fatalf("AutoApproveFamilies = %T, want FamilySet", got.Overrides[autonomy.KnobAutoApproveFamilies])
	}
	if !gotFamilies.Equal(families) {
		t.Errorf("AutoApproveFamilies = %v, want %v", gotFamilies.Sorted(), families.Sorted())
	}
}

// TestMemStore_AutonomyMutationIsolation pins that a caller's mutation
// of the Overrides map after Set doesn't bleed into the stored copy
// (and vice versa). The store is supposed to clone on the write/read
// boundary so callers can hold the layer they passed in safely.
func TestMemStore_AutonomyMutationIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := session.NewMemoryStore()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	overrides := map[autonomy.Knob]any{autonomy.KnobMaxIterations: int(50)}
	if err := s.SetAutonomyProfile(ctx, "s1", autonomy.Layer{Overrides: overrides}); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	overrides[autonomy.KnobMaxIterations] = int(999) // mutate caller's map
	got, err := s.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Overrides[autonomy.KnobMaxIterations] != 50 {
		t.Errorf("post-mutation MaxIterations = %v, want 50", got.Overrides[autonomy.KnobMaxIterations])
	}
}

func TestMemStore_AutonomyMissingSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := session.NewMemoryStore()
	if _, err := s.GetAutonomyProfile(ctx, "ghost"); err == nil {
		t.Errorf("GetAutonomyProfile ghost = nil err, want ErrSessionNotFound")
	}
	if err := s.SetAutonomyProfile(ctx, "ghost", autonomy.DefaultLayer()); err == nil {
		t.Errorf("SetAutonomyProfile ghost = nil err, want ErrSessionNotFound")
	}
}

// ── SQL parity ─────────────────────────────────────────────────────────

func openAutonomySQLStore(t *testing.T) (session.Store, storage.DB) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return session.NewSQLStore(session.NewStorageDB(db)), db
}

func TestSQLStore_AutonomyRoundTrip_Empty(t *testing.T) {
	t.Parallel()
	store, _ := openAutonomySQLStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("default GetAutonomyProfile = %+v, want IsZero", got)
	}
}

func TestSQLStore_AutonomyRoundTrip_FullLayer(t *testing.T) {
	t.Parallel()
	store, _ := openAutonomySQLStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bold := autonomy.TierBold
	families := autonomy.NewFamilySet(autonomy.FamilyRead, autonomy.FamilyWrite)
	want := autonomy.Layer{
		Level: &bold,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations:       int(75),
			autonomy.KnobAskOnAmbiguity:      autonomy.AskMajor,
			autonomy.KnobTokenCeilingPerTurn: int(262144),
			autonomy.KnobAutoApproveFamilies: families,
			autonomy.KnobRecapStyle:          autonomy.RecapFull,
			autonomy.KnobContinueOnError:     autonomy.ErrorAdapt,
			autonomy.KnobDestructiveActionPosture: autonomy.DestructiveCedarOnly,
		},
	}
	if err := store.SetAutonomyProfile(ctx, "s1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err := store.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Level == nil || *got.Level != autonomy.TierBold {
		t.Errorf("Level = %v, want bold", got.Level)
	}
	for _, knob := range []autonomy.Knob{
		autonomy.KnobMaxIterations,
		autonomy.KnobAskOnAmbiguity,
		autonomy.KnobTokenCeilingPerTurn,
		autonomy.KnobRecapStyle,
		autonomy.KnobContinueOnError,
		autonomy.KnobDestructiveActionPosture,
	} {
		if !reflect.DeepEqual(got.Overrides[knob], want.Overrides[knob]) {
			t.Errorf("knob %s: got %v (%T), want %v (%T)",
				knob, got.Overrides[knob], got.Overrides[knob],
				want.Overrides[knob], want.Overrides[knob])
		}
	}
	gotFamilies, ok := got.Overrides[autonomy.KnobAutoApproveFamilies].(autonomy.FamilySet)
	if !ok || !gotFamilies.Equal(families) {
		t.Errorf("AutoApproveFamilies round-trip = %v, want %v", got.Overrides[autonomy.KnobAutoApproveFamilies], families.Sorted())
	}
}

// TestSQLStore_AutonomyRoundTrip_PartialOverrides exercises the case
// where Level is nil but a sparse subset of knobs are pinned. After
// reload Level must remain nil and only the pinned knobs must be
// present in Overrides — this is the canonical "global=Default,
// project=null, session=Custom" shape from the plan §Resolution
// semantics example.
func TestSQLStore_AutonomyRoundTrip_PartialOverrides(t *testing.T) {
	t.Parallel()
	store, _ := openAutonomySQLStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := autonomy.Layer{
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations: int(75),
		},
	}
	if err := store.SetAutonomyProfile(ctx, "s1", want); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	got, err := store.GetAutonomyProfile(ctx, "s1")
	if err != nil {
		t.Fatalf("GetAutonomyProfile: %v", err)
	}
	if got.Level != nil {
		t.Errorf("Level = %v, want nil", got.Level)
	}
	if len(got.Overrides) != 1 || got.Overrides[autonomy.KnobMaxIterations] != 75 {
		t.Errorf("Overrides = %v, want only MaxIterations=75", got.Overrides)
	}
}

// TestSQLStore_AutonomyClearViaEmptyLayer pins that writing the empty
// Layer back to a session that already had a non-empty profile clears
// both columns to NULL — this is how the "Reset session to project
// default" UI affordance maps onto storage.
func TestSQLStore_AutonomyClearViaEmptyLayer(t *testing.T) {
	t.Parallel()
	store, db := openAutonomySQLStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, autonomyTestRecord("s1", now)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	bold := autonomy.TierBold
	if err := store.SetAutonomyProfile(ctx, "s1", autonomy.Layer{Level: &bold}); err != nil {
		t.Fatalf("SetAutonomyProfile: %v", err)
	}
	// Clear by writing the empty Layer.
	if err := store.SetAutonomyProfile(ctx, "s1", autonomy.Layer{}); err != nil {
		t.Fatalf("SetAutonomyProfile clear: %v", err)
	}
	row := db.Reader().QueryRow(ctx,
		"SELECT autonomy_level, autonomy_overrides FROM sessions WHERE id = ?", "s1")
	var (
		lvl *int64
		ovr *string
	)
	if err := row.Scan(&lvl, &ovr); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lvl != nil {
		t.Errorf("autonomy_level = %d, want NULL after clear", *lvl)
	}
	if ovr != nil {
		t.Errorf("autonomy_overrides = %q, want NULL after clear", *ovr)
	}
}

// TestSQLStore_AutonomyMissingSession pins that GetAutonomyProfile and
// SetAutonomyProfile both surface ErrSessionNotFound when the session
// id has no row — important for the manager layer's error handling.
func TestSQLStore_AutonomyMissingSession(t *testing.T) {
	t.Parallel()
	store, _ := openAutonomySQLStore(t)
	ctx := context.Background()
	if _, err := store.GetAutonomyProfile(ctx, "ghost"); err == nil {
		t.Errorf("GetAutonomyProfile ghost = nil err, want ErrSessionNotFound")
	}
	if err := store.SetAutonomyProfile(ctx, "ghost", autonomy.DefaultLayer()); err == nil {
		t.Errorf("SetAutonomyProfile ghost = nil err, want ErrSessionNotFound")
	}
}
