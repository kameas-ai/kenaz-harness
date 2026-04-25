package storage

import (
	"errors"
	"testing"
)

func TestAllEventKindsHaveSchemas(t *testing.T) {
	t.Parallel()
	for _, kind := range AllEventKinds {
		if _, ok := eventPayloadSchemas[kind]; !ok {
			t.Errorf("event kind %q missing payload schema", kind)
		}
	}
	if len(AllEventKinds) != len(eventPayloadSchemas) {
		t.Errorf("AllEventKinds (%d) and eventPayloadSchemas (%d) sizes differ",
			len(AllEventKinds), len(eventPayloadSchemas))
	}
}

func TestValidateEventPayload_Happy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    string
		payload map[string]any
	}{
		{
			EventKindDBOpened,
			map[string]any{
				"install_id":        "01ABC",
				"libsql_version":    "0.7.0",
				"sqlite_version":    "3.51.0",
				"encryption_status": "enabled",
				"wal_mode":          true,
				"foreign_keys_on":   true,
			},
		},
		{
			EventKindMigrationApplied,
			map[string]any{
				"version":        1,
				"id":             "storage/0001-init",
				"owning_mission": "storage",
				"content_hash":   "sha256:abcd",
				"duration_ms":    int64(5),
			},
		},
		{
			EventKindEventsDropped,
			map[string]any{"count": int64(3)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if err := ValidateEventPayload(tc.kind, tc.payload); err != nil {
				t.Errorf("validate(%s): %v", tc.kind, err)
			}
		})
	}
}

func TestValidateEventPayload_MissingKey(t *testing.T) {
	t.Parallel()
	err := ValidateEventPayload(EventKindMigrationApplied, map[string]any{
		"version": 1,
		"id":      "x",
		// missing owning_mission, content_hash, duration_ms
	})
	if err == nil {
		t.Fatal("expected missing-key error")
	}
}

func TestValidateEventPayload_UnknownKind(t *testing.T) {
	t.Parallel()
	err := ValidateEventPayload("nope", map[string]any{})
	if !errors.Is(err, ErrUnknownEventKind) {
		t.Fatalf("got %v, want ErrUnknownEventKind", err)
	}
}

func TestEventPayloadKeys(t *testing.T) {
	t.Parallel()
	keys, ok := EventPayloadKeys(EventKindBackupTaken)
	if !ok {
		t.Fatal("expected backup_taken to have schema")
	}
	if len(keys) == 0 {
		t.Fatal("expected non-empty key list")
	}
	// Mutating returned slice must not affect the schema.
	keys[0] = "tampered"
	freshKeys, _ := EventPayloadKeys(EventKindBackupTaken)
	if freshKeys[0] == "tampered" {
		t.Fatal("EventPayloadKeys must return a copy")
	}
}
