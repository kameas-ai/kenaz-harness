// Package log — retention sweep.
//
// RetentionSweep implements three strategies:
//   - keep_forever: no-op (NFR — never lose data on first run).
//   - delete_after_window: delete rows older than window.
//   - archive_after_window: write old rows to JSONL archive, then delete.
//
// The sweep also re-verifies the chain after deletion to detect if a
// purge left an inconsistent tail.
package log

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RetentionStrategy controls how the sweep handles aged-out rows.
type RetentionStrategy string

const (
	// RetentionKeepForever never deletes rows. Safe default on first install.
	RetentionKeepForever RetentionStrategy = "keep_forever"

	// RetentionDeleteAfterWindow deletes rows older than the window.
	RetentionDeleteAfterWindow RetentionStrategy = "delete_after_window"

	// RetentionArchiveAfterWindow writes aged-out rows to a JSONL archive
	// in <DataDir>/audit-archive/ then deletes them.
	RetentionArchiveAfterWindow RetentionStrategy = "archive_after_window"
)

// RetentionResult summarises the sweep outcome.
type RetentionResult struct {
	// Purged is the number of rows deleted from the live store.
	Purged int `json:"purged"`
	// Archived is the number of rows written to the archive file.
	Archived int `json:"archived"`
	// ArchivePath is the path of the JSONL file written (empty when
	// strategy is not archive_after_window or nothing was archived).
	ArchivePath string `json:"archive_path,omitempty"`
	// ChainVerified is true when the post-sweep chain verification passed.
	ChainVerified bool `json:"chain_verified"`
}

// SweepableBackend extends Backend with the additional operations needed
// by the retention sweep.
type SweepableBackend interface {
	Backend
	// DeleteRows removes rows by event_id. Callers MUST archive
	// before calling DeleteRows to preserve the archive-before-delete
	// invariant (C-002 extension).
	DeleteRows(ctx context.Context, eventIDs []string) error
}

// RetentionSweep applies the retention strategy to rows older than
// cutoff (= now - window). The backend must implement SweepableBackend.
//
// The default strategy keep_forever is a no-op and always safe.
//
// archive_after_window flushes the archive file to disk before deleting;
// if the flush fails the delete is skipped (archive-before-delete invariant).
func RetentionSweep(ctx context.Context, backend SweepableBackend, dataDir string,
	strategy RetentionStrategy, window time.Duration) (RetentionResult, error) {

	if strategy == RetentionKeepForever {
		return RetentionResult{ChainVerified: true}, nil
	}

	cutoff := time.Now().Add(-window)

	// Fetch all rows older than cutoff (open upper bound = cutoff, full history).
	old, err := backend.SelectByTimeRange(ctx, time.Time{}, cutoff, "", 0, false)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("retention sweep: fetch old rows: %w", err)
	}
	if len(old) == 0 {
		// Nothing to do; verify chain and return.
		res, err := VerifyChain(ctx, backend, "", "")
		if err != nil {
			return RetentionResult{ChainVerified: false}, nil
		}
		return RetentionResult{ChainVerified: res.Verified}, nil
	}

	result := RetentionResult{}

	if strategy == RetentionArchiveAfterWindow {
		// Write archive JSONL before deleting, then delete — via the
		// same ArchiveAndDelete helper the fleet-sweeper adapter
		// (core/rpc/views/settings) uses, so the archive-before-delete
		// invariant holds regardless of which sweeper selected the
		// rows (E-007: "exactly one function ever issues DeleteRows
		// against events").
		archived, archivePath, err := ArchiveAndDelete(ctx, backend, dataDir, old)
		if err != nil {
			return result, fmt.Errorf("retention sweep: %w", err)
		}
		result.Archived = archived
		result.ArchivePath = archivePath
		result.Purged = len(old)
	} else {
		// delete_after_window: no archive, by definition of the strategy.
		ids := make([]string, len(old))
		for i, r := range old {
			ids[i] = r.EventID
		}
		if err := backend.DeleteRows(ctx, ids); err != nil {
			return result, fmt.Errorf("retention sweep: delete: %w", err)
		}
		result.Purged = len(ids)
	}

	// Post-sweep chain verify.
	verifyRes, verifyErr := VerifyChain(ctx, backend, "", "")
	if verifyErr == nil {
		result.ChainVerified = verifyRes.Verified
	}

	return result, nil
}

// ArchiveAndDelete writes rows to the JSONL archive (same file/rotation
// RetentionSweep's archive_after_window path uses) and, ONLY if that
// succeeds, deletes them via backend.DeleteRows — preserving the
// archive-before-delete invariant no matter which caller selected the
// rows.
//
// This is the SOLE path in production that issues DeleteRows against
// events for a row that must be archived first (E-007, spec §5.6 item
// 2 / §1.7c: "the local sweeper and the fleet sweeper both delete from
// one table under an archive-before-delete invariant... pick one
// owner"). The local strategy sweeper (RetentionSweep, above) calls it
// directly for archive_after_window. The fleet ACK sweeper
// (core/fleet.AuditRetentionSweeper) does not call it directly — it
// cannot import this package's types without a circular/boundary
// violation (check-no-fleet-imports.sh) — instead
// core/rpc/views/settings's fleetAuditRetentionBackendAdapter routes
// the fleet sweeper's DeleteRows calls through this function, so an
// ACK'd-and-aged row fleet selects is archived exactly the same way a
// locally-aged row is, and there is exactly one function body in the
// tree that ever calls SweepableBackend.DeleteRows without archiving
// first: the delete_after_window branch above, which is a deliberate
// no-archive strategy by definition, not a second deleter.
func ArchiveAndDelete(ctx context.Context, backend SweepableBackend, dataDir string, rows []Row) (archived int, archivePath string, err error) {
	if len(rows) == 0 {
		return 0, "", nil
	}
	archivePath, archived, archErr := archiveRows(ctx, dataDir, rows)
	if archErr != nil {
		return 0, "", fmt.Errorf("archive-and-delete: archive: %w", archErr)
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.EventID
	}
	if err := backend.DeleteRows(ctx, ids); err != nil {
		return archived, archivePath, fmt.Errorf("archive-and-delete: delete: %w", err)
	}
	return archived, archivePath, nil
}

// archiveRows writes rows to a dated JSONL file in <dataDir>/audit-archive/.
// Returns the file path and number of rows written.
func archiveRows(_ context.Context, dataDir string, rows []Row) (string, int, error) {
	dir := filepath.Join(dataDir, "audit-archive")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("archive: mkdir %q: %w", dir, err)
	}
	ts := time.Now().UTC().Format("2006-01-02")
	outPath := filepath.Join(dir, ts+".jsonl")

	// Append to existing daily file if present.
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("archive: open %q: %w", outPath, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	written := 0
	for _, r := range rows {
		rec := exportRow{
			EventID:   r.EventID,
			SessionID: r.SessionID,
			EmitterID: r.EmitterID,
			Kind:      r.Kind,
			EmittedAt: r.EmittedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
			Payload:   string(redactSecrets(r.Payload)),
		}
		if err := enc.Encode(rec); err != nil {
			return outPath, written, fmt.Errorf("archive: encode: %w", err)
		}
		written++
	}
	return outPath, written, nil
}
