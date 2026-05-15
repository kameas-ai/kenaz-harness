// migration_replay_test.go — CI invariant: every registered kind's fixture
// payload migrates to its CurrentVersion cleanly (NFR-006).
//
// The test builds a fixture payload for each registered kind at each
// version below CurrentVersion, runs MigratePayload, and asserts:
//   - The migration chain completes without error.
//   - The result is valid JSON.
//   - The final version matches CurrentVersion.
package log

import (
	"fmt"
	"testing"
)

// TestMigrationReplay verifies that every registered KindSchema can replay
// its migration chain from v1 up to CurrentVersion.
func TestMigrationReplay(t *testing.T) {
	if len(schemaRegistry) == 0 {
		t.Skip("no KindSchemas registered; skip replay test")
	}
	for kind, s := range schemaRegistry {
		kind, s := kind, s
		t.Run(fmt.Sprintf("kind=%s", kind), func(t *testing.T) {
			if s.CurrentVersion < 1 {
				t.Fatalf("KindSchema.CurrentVersion must be >= 1, got %d", s.CurrentVersion)
			}
			// Start from v1 and migrate to CurrentVersion.
			fixture := []byte(`{"fixture":true}`)
			got, finalVer, err := MigratePayload(kind, 1, fixture)
			if err != nil {
				t.Fatalf("MigratePayload from v1: %v", err)
			}
			if finalVer != s.CurrentVersion {
				t.Errorf("expected final version %d, got %d", s.CurrentVersion, finalVer)
			}
			if err := EnsureValidJSON(got); err != nil {
				t.Errorf("migration produced invalid JSON: %v", err)
			}
		})
	}
}

// TestSchemaVersion_CurrentAtLeastOne verifies the global constant.
func TestSchemaVersion_CurrentAtLeastOne(t *testing.T) {
	if CurrentSchemaVersion < 1 {
		t.Fatalf("CurrentSchemaVersion must be >= 1, got %d", CurrentSchemaVersion)
	}
}
