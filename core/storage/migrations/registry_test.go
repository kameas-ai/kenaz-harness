package migrations

import (
	"context"
	"errors"
	"testing"
)

func newTestRegistry(t *testing.T) (*Registry, *fakeDB) {
	t.Helper()
	db := newFakeDB()
	reg := NewRegistry(db, func() string { return "2026-04-25T00:00:00Z" }, nil)
	return reg, db
}

func storageMig(version int, id, source string) Migration {
	return Migration{
		ID:            id,
		Version:       version,
		OwningMission: "storage",
		UpSource:      source,
		Up: func(ctx context.Context, tx WriteTx) error {
			_, err := tx.Exec(ctx, source)
			return err
		},
		Down: func(ctx context.Context, tx WriteTx) error { return nil },
	}
}

func TestRegister_HappyPath(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	m := storageMig(1, "storage/0001-init", "CREATE TABLE IF NOT EXISTS foo (id INTEGER)")
	if err := reg.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := reg.Lookup(1)
	if !ok {
		t.Fatal("Lookup failed")
	}
	if got.ContentHash == "" {
		t.Fatal("expected ContentHash auto-computed")
	}
	if got.ContentHash != HashSQL(m.UpSource) {
		t.Fatalf("ContentHash mismatch: got %s, want %s", got.ContentHash, HashSQL(m.UpSource))
	}
}

func TestRegister_VersionOutOfBlock(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	m := Migration{
		ID:            "event-log/0001-init",
		Version:       50, // event-log block is 100..199
		OwningMission: "event-log",
		UpSource:      "CREATE TABLE IF NOT EXISTS x (id INTEGER)",
		Up:            func(ctx context.Context, tx WriteTx) error { return nil },
	}
	err := reg.Register(m)
	if !errors.Is(err, ErrVersionOutOfBlock) {
		t.Fatalf("got %v, want ErrVersionOutOfBlock", err)
	}
}

func TestRegister_VersionInBlock_Succeeds(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	m := Migration{
		ID:            "event-log/0001-init",
		Version:       100,
		OwningMission: "event-log",
		UpSource:      "CREATE TABLE IF NOT EXISTS x (id INTEGER)",
		Up:            func(ctx context.Context, tx WriteTx) error { return nil },
	}
	if err := reg.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRegister_UnknownOwningMission(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	m := Migration{
		ID:            "ghost/0001-init",
		Version:       1,
		OwningMission: "ghost",
		UpSource:      "CREATE TABLE IF NOT EXISTS x (id INTEGER)",
		Up:            func(ctx context.Context, tx WriteTx) error { return nil },
	}
	err := reg.Register(m)
	if !errors.Is(err, ErrUnknownOwningMission) {
		t.Fatalf("got %v, want ErrUnknownOwningMission", err)
	}
}

func TestRegister_VersionCollision(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	if err := reg.Register(storageMig(1, "storage/0001-a", "CREATE TABLE foo (id INT)")); err != nil {
		t.Fatal(err)
	}
	err := reg.Register(storageMig(1, "storage/0001-b", "CREATE TABLE bar (id INT)"))
	if !errors.Is(err, ErrVersionCollision) {
		t.Fatalf("got %v, want ErrVersionCollision", err)
	}
}

func TestRegister_IDCollision(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	if err := reg.Register(storageMig(1, "storage/0001-init", "CREATE TABLE foo (id INT)")); err != nil {
		t.Fatal(err)
	}
	err := reg.Register(storageMig(2, "storage/0001-init", "CREATE TABLE bar (id INT)"))
	if !errors.Is(err, ErrMigrationIDCollision) {
		t.Fatalf("got %v, want ErrMigrationIDCollision", err)
	}
}

func TestRegister_InvalidMigration(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	cases := []Migration{
		{Version: 1, OwningMission: "storage", UpSource: "X", Up: func(context.Context, WriteTx) error { return nil }}, // missing ID
		{ID: "a", OwningMission: "storage", UpSource: "X", Up: func(context.Context, WriteTx) error { return nil }},    // version 0
		{ID: "a", Version: 1, UpSource: "X", Up: func(context.Context, WriteTx) error { return nil }},                  // missing owning_mission
		{ID: "a", Version: 1, OwningMission: "storage", UpSource: "X"},                                                 // missing Up
		{ID: "a", Version: 1, OwningMission: "storage", Up: func(context.Context, WriteTx) error { return nil }},       // missing UpSource and ContentHash
	}
	for i, m := range cases {
		err := reg.Register(m)
		if !errors.Is(err, ErrInvalidMigration) {
			t.Errorf("case %d: got %v, want ErrInvalidMigration", i, err)
		}
		// reset between cases (clean up any partial inserts on the
		// bookkeeping maps; the validators run before the maps mutate
		// but defensive)
		reg.Reset()
	}
}

func TestAll_SortedByVersion(t *testing.T) {
	t.Parallel()
	reg, _ := newTestRegistry(t)
	_ = reg.Register(storageMig(3, "storage/0003", "CREATE TABLE c (id INT)"))
	_ = reg.Register(storageMig(1, "storage/0001", "CREATE TABLE a (id INT)"))
	_ = reg.Register(storageMig(2, "storage/0002", "CREATE TABLE b (id INT)"))
	got := reg.All()
	for i, m := range got {
		if m.Version != i+1 {
			t.Errorf("All[%d].Version = %d, want %d", i, m.Version, i+1)
		}
	}
}
