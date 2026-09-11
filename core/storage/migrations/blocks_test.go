package migrations

import "testing"

func TestVersionBlock_Contains(t *testing.T) {
	t.Parallel()
	b := VersionBlock{Min: 100, Max: 199}
	cases := []struct {
		v    int
		want bool
	}{
		{99, false},
		{100, true},
		{150, true},
		{199, true},
		{200, false},
	}
	for _, c := range cases {
		if got := b.Contains(c.v); got != c.want {
			t.Errorf("Contains(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestCanonicalBlocks_StorageRange(t *testing.T) {
	t.Parallel()
	b, ok := LookupBlock("storage")
	if !ok {
		t.Fatal("storage block missing")
	}
	if b.Min != 1 || b.Max != 99 {
		t.Errorf("storage block = %+v, want {1,99}", b)
	}
}

func TestCanonicalBlocks_Coverage(t *testing.T) {
	t.Parallel()
	missions := []string{
		"storage", "event-log", "secrets-keychain", "sessions", "scheduler",
		"mcp", "a2a", "signed-cards-trust", "bundle",
		"shared-context-distribution", "memory-rag", "app-layer",
		"user-slash-commands", "units", "tasks", "cedar-policy",
	}
	for _, m := range missions {
		if _, ok := LookupBlock(m); !ok {
			t.Errorf("expected block for mission %q", m)
		}
	}
}

// TestCanonicalBlocks_NoOverlap is table-driven over the declared blocks
// themselves (not a fixed mission list) so that adding a new overlapping
// block to CanonicalBlocks fails this test in CI, rather than lying
// dormant until two missions actually collide on a Version number and
// the app refuses to boot (storagesqlite.Open -> Registry.Register ->
// ErrVersionCollision — the v0.63.0 P0 shape).
//
// Every mission previously had a distinct block (2026-08-20 finding:
// "a2a"/"signed-cards-trust" both {600,699} and "bundle"/
// "shared-context-distribution" both {700,799} — see docs/unwired-ledger.md
// and the comments in blocks.go). This test codifies that invariant going
// forward: NO two owning-mission names may share any part of a range,
// full stop. There is no "intentional sharing" exemption — Registry.Register
// keys on Version globally across the whole registry, so two names sharing
// a block is a live boot-failure hazard regardless of intent.
func TestCanonicalBlocks_NoOverlap(t *testing.T) {
	t.Parallel()
	type entry struct {
		mission string
		block   VersionBlock
	}
	entries := make([]entry, 0, len(CanonicalBlocks))
	for m, b := range CanonicalBlocks {
		entries = append(entries, entry{mission: m, block: b})
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			a, b := entries[i], entries[j]
			if a.block.Min <= b.block.Max && b.block.Min <= a.block.Max {
				t.Errorf("blocks overlap: %q %+v and %q %+v", a.mission, a.block, b.mission, b.block)
			}
		}
	}
}
