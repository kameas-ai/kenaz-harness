package sqlite_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// TestOpen_EveryMigrationVersionFallsInsideItsOwnBlock drives the REAL
// production migration wiring (storagesqlite.Open -> every mission's
// RegisterMigrations, exactly as core/storage/sqlite/sqlite.go:118-167
// assembles it at boot) and checks two things for every migration that
// actually registers today:
//
//  1. Version falls inside CanonicalBlocks[OwningMission]. Registry.Register
//     already enforces this at registration time (ErrVersionOutOfBlock), so
//     this half is a redundant witness — but a cheap one, and it means a
//     future refactor of Register's validation still gets caught here.
//  2. The migration's ID prefix (the "<mission>/" before the first "-")
//     matches OwningMission. This is the check Register() CANNOT make: two
//     mission names sharing one block (the exact 2026-08-20 finding fixed
//     in blocks.go) would let a migration authored under one name's ID
//     convention register with the OTHER name's OwningMission and still
//     pass block validation, because both names resolve to the same range.
//     The convention across every migration in the repo (verified by
//     inspection: "bundle/700-trust-anchors-init", "sessions/0300-init",
//     "units/1100-init", "tasks/1200-tasks-init",
//     "user-slash-commands/1000-slash-commands-user",
//     "event-log/0100-events", "memory-rag/0821-narrative-jobs-pending")
//     is ID = "<OwningMission>/<Version>-<slug>". This test makes that
//     convention load-bearing instead of just a convention.
func TestOpen_EveryMigrationVersionFallsInsideItsOwnBlock(t *testing.T) {
	t.Parallel()
	db := mustOpen(t, t.TempDir())

	all := db.Migrations().All()
	if len(all) == 0 {
		t.Fatal("no migrations registered — wiring must have changed; this test needs updating, not skipping")
	}

	for _, m := range all {
		block, ok := migrations.LookupBlock(m.OwningMission)
		if !ok {
			t.Errorf("migration %s: OwningMission %q has no reserved block", m.ID, m.OwningMission)
			continue
		}
		if !block.Contains(m.Version) {
			t.Errorf("migration %s: Version %d outside %s's block [%d,%d]",
				m.ID, m.Version, m.OwningMission, block.Min, block.Max)
		}

		prefix, rest, found := strings.Cut(m.ID, "/")
		if !found {
			t.Errorf("migration %s: ID does not follow the <mission>/<version>-<slug> convention", m.ID)
			continue
		}
		if prefix != m.OwningMission {
			t.Errorf("migration %s: ID prefix %q does not match OwningMission %q — "+
				"this is exactly the drift that lets two missions sharing a block "+
				"silently swap identities", m.ID, prefix, m.OwningMission)
		}

		// Cross-check the version literal embedded in the ID (the digits
		// before the first '-') against m.Version itself, catching a
		// copy-pasted ID whose embedded number was never updated.
		versionDigits, _, _ := strings.Cut(rest, "-")
		if v, err := strconv.Atoi(versionDigits); err == nil {
			if v != m.Version {
				t.Errorf("migration %s: ID embeds version %d but Version field is %d", m.ID, v, m.Version)
			}
		}
	}
}
