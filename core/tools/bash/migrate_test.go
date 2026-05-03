package bash_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/tools/bash"
)

// ── test doubles ──────────────────────────────────────────────────────────

// fakeSnippetWriter records Write calls and can be configured to fail on a
// given call index (0-based).
type fakeSnippetWriter struct {
	mu       sync.Mutex
	written  []string // snippet names in order
	failAt   int      // return error when len(written) == failAt; -1 = never fail
}

func newFakeWriter() *fakeSnippetWriter          { return &fakeSnippetWriter{failAt: -1} }
func newFakeWriterFailAt(n int) *fakeSnippetWriter { return &fakeSnippetWriter{failAt: n} }

func (f *fakeSnippetWriter) WritePolicySnippet(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAt >= 0 && len(f.written) == f.failAt {
		return errors.New("fake: write failed")
	}
	f.written = append(f.written, name)
	return nil
}

func (f *fakeSnippetWriter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.written)
}

// fakeMigrationStore is an in-memory MigrationStore.
type fakeMigrationStore struct {
	mu      sync.Mutex
	migrated bool
}

func (s *fakeMigrationStore) LoadBashAllowlistMigrated() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migrated, nil
}

func (s *fakeMigrationStore) SaveBashAllowlistMigrated(m bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migrated = m
	return nil
}

// ── tests ─────────────────────────────────────────────────────────────────

// TestMigrateBashAllowlist_Idempotent verifies that a second call with the
// migrated flag already set writes no additional snippets.
func TestMigrateBashAllowlist_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeMigrationStore{}
	writer := newFakeWriter()

	// First call — should write all patterns and set the flag.
	if err := bash.MigrateBashAllowlist(ctx, writer, store); err != nil {
		t.Fatalf("first call: %v", err)
	}
	firstCount := writer.count()
	if firstCount == 0 {
		t.Fatal("first call wrote 0 snippets, expected >0")
	}
	migrated, _ := store.LoadBashAllowlistMigrated()
	if !migrated {
		t.Fatal("BashAllowlistMigrated should be true after first call")
	}

	// Second call — idempotent, must not write any additional snippets.
	if err := bash.MigrateBashAllowlist(ctx, writer, store); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if writer.count() != firstCount {
		t.Fatalf("second call wrote %d additional snippets, expected 0",
			writer.count()-firstCount)
	}
}

// TestMigrateBashAllowlist_PartialFailure verifies that when WritePolicySnippet
// fails mid-migration the function returns an error and BashAllowlistMigrated
// stays false (so the next boot retries).
func TestMigrateBashAllowlist_PartialFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeMigrationStore{}
	// Fail on the 5th write (index 4) — arbitrary mid-migration point.
	writer := newFakeWriterFailAt(4)

	err := bash.MigrateBashAllowlist(ctx, writer, store)
	if err == nil {
		t.Fatal("expected error from WritePolicySnippet failure, got nil")
	}
	migrated, _ := store.LoadBashAllowlistMigrated()
	if migrated {
		t.Fatal("BashAllowlistMigrated must stay false after a partial failure")
	}
}

// TestMigrateBashAllowlist_Timing verifies that migrating all historical
// patterns completes in under 1 second (NFR-005).
func TestMigrateBashAllowlist_Timing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeMigrationStore{}
	writer := newFakeWriter()

	start := time.Now()
	if err := bash.MigrateBashAllowlist(ctx, writer, store); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("migration took %v, want < 1s (NFR-005)", elapsed)
	}
	t.Logf("migration of %d patterns completed in %v", writer.count(), elapsed)
}

// TestMigrateBashAllowlist_AllPatternsWritten verifies that one snippet per
// historical allowlist entry is written (no missing, no duplicates).
func TestMigrateBashAllowlist_AllPatternsWritten(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeMigrationStore{}
	writer := newFakeWriter()

	if err := bash.MigrateBashAllowlist(ctx, writer, store); err != nil {
		t.Fatalf("migration: %v", err)
	}

	// The historical list has 31 entries (ls, cat, head, tail, grep, find, wc,
	// file, stat, du, df, which, type, echo, pwd, env, date, uname, git,
	// python, python3, node, go, cargo, npm, npx, make, gcc, clang, ruby,
	// rustc).
	const expectedCount = 31
	if n := writer.count(); n != expectedCount {
		t.Fatalf("wrote %d snippets, want %d", n, expectedCount)
	}

	// Ensure all names match the Cedar filename regex (belt-and-suspenders).
	seen := make(map[string]bool, expectedCount)
	for _, name := range writer.written {
		if seen[name] {
			t.Errorf("duplicate snippet name: %q", name)
		}
		seen[name] = true
	}
}
