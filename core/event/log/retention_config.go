package log

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

func nowUnixMilli() int64 { return time.Now().UnixMilli() }

// PersistedPolicy is the JSON shape stored in retention_config.policy
// (migration event-log/0103-retention-config's table). Kind is one of
// the three RetentionStrategy values; WindowDays is meaningful only for
// delete_after_window / archive_after_window.
type PersistedPolicy struct {
	Kind       RetentionStrategy `json:"kind"`
	WindowDays int               `json:"window_days,omitempty"`
}

// DefaultRetentionWindowDays is used when a delete_after_window /
// archive_after_window policy is persisted with no explicit window
// (should not happen via the settings write path below, which always
// sets one, but a defensive fallback matters more than a strict
// rejection here — see the E-003 comment on ReadRetentionPolicy).
const DefaultRetentionWindowDays = 90

// retentionConfigVersion is the sole row this mission ever reads or
// writes in retention_config — the table is versioned (PRIMARY KEY on
// "version") for a future migration-of-policy path, not multi-row
// config today. Migration 0103 seeds version 1.
const retentionConfigVersion = 1

// ReadRetentionPolicy reads the current policy from retention_config.
// Falls back to RetentionKeepForever — D-4's safe default, "a mission
// that starts persisting audit rows for the first time must not also
// start deleting them by default" — for every failure mode: no row,
// unparseable JSON, or a Kind that is not one of the three
// RetentionStrategy values (the exact shape of the bug migration 106
// corrects: 0103's shipped seed was '{"kind":"keep_all"}', which is not
// a valid value; a NEW way for an unrecognised value to reach this
// table — e.g. a downgrade, or manual DB surgery — must fail exactly
// as safely). Never silently start deleting on an error path (spec
// E-003).
func ReadRetentionPolicy(ctx context.Context, db storage.DB) (PersistedPolicy, error) {
	var raw string
	err := db.Reader().QueryRow(ctx,
		"SELECT policy FROM retention_config WHERE version = ?", retentionConfigVersion).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return PersistedPolicy{Kind: RetentionKeepForever}, nil
		}
		return PersistedPolicy{Kind: RetentionKeepForever}, fmt.Errorf("log: ReadRetentionPolicy: %w", err)
	}
	var p PersistedPolicy
	if jsonErr := json.Unmarshal([]byte(raw), &p); jsonErr != nil {
		return PersistedPolicy{Kind: RetentionKeepForever}, nil //nolint:nilerr // malformed persisted JSON degrades to the safe default, not an error the caller must handle specially
	}
	switch p.Kind {
	case RetentionKeepForever, RetentionDeleteAfterWindow, RetentionArchiveAfterWindow:
		return p, nil
	default:
		// Unrecognised Kind (including the pre-106 "keep_all" seed on
		// any install that somehow still carries it, and any future
		// downgrade/corruption case). Degrade to the safe default
		// rather than erroring the sweep loop or guessing.
		return PersistedPolicy{Kind: RetentionKeepForever}, nil
	}
}

// WriteRetentionPolicy persists p to retention_config, upserting the
// single versioned row.
func WriteRetentionPolicy(ctx context.Context, db storage.DB, p PersistedPolicy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("log: WriteRetentionPolicy: marshal: %w", err)
	}
	return db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO retention_config (version, policy, effective_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(version) DO UPDATE SET policy = excluded.policy, effective_at = excluded.effective_at`,
			retentionConfigVersion, string(raw), nowUnixMilli())
		if err != nil {
			return fmt.Errorf("log: WriteRetentionPolicy: %w", err)
		}
		return nil
	})
}
