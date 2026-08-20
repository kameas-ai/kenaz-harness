package settings

// This file is the ONLY place audit-that-tells-the-truth-01PMZA10
// UNIT-7 converts between core/event/log's storage-layer retention
// types and core/fleet's transport-layer ones — deliberately, not in
// core/event/log itself. core/event/log must not import core/fleet
// (check-no-fleet-imports.sh permits only core/rpc/views/settings/,
// core/rpc, core/rpc/views/fleet, core/rpc/middleware and
// core/mcp/builtin/sites to do that), and core/event/log/sqlbackend.go
// says so explicitly at its SelectBefore doc comment: "the boundary
// package converts."

import (
	"context"
	"errors"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// sqlEventLogBackend is the exact surface *eventlog.SQLBackend provides
// (SweepableBackend's Backend + DeleteRows, plus the SelectBefore shim
// UNIT-3 built specifically for this adapter — spec §5.3, R-6:
// "SelectBefore is not on log.Backend"). A narrow local interface
// rather than a concrete *eventlog.SQLBackend parameter so tests can
// substitute a fake without touching real sqlite.
type sqlEventLogBackend interface {
	eventlog.SweepableBackend
	SelectBefore(ctx context.Context, cutoff time.Time, limit int) ([]eventlog.RetentionRow, error)
}

// fleetAuditRetentionBackend adapts a sqlEventLogBackend to
// fleet.AuditRetentionBackend, feeding
// core/fleet.AuditRetentionSweeper a non-nil Backend for the first
// time (AC-015) — core/rpc/api.go's nil-backend no-op path
// ("Backend is nil here ... SweepOnce returns 0 rows when backend is
// nil (safe no-op)") becomes unreachable in production wiring.
//
// DeleteRows routes through eventlog.ArchiveAndDelete rather than a
// bare backend.DeleteRows call — E-007 (spec §5.6 item 2): the local
// strategy sweeper and the fleet ACK sweeper both delete from `events`
// under an archive-before-delete invariant, and a raw SQL DELETE
// serialised by SetMaxOpenConns(1) is safe against CORRUPTION but does
// not by itself preserve that invariant for whichever sweeper's rows
// bypass an archive step. Routing both sweepers' deletes through the
// ONE ArchiveAndDelete function makes "a row is archived before it is
// deleted" true regardless of which sweeper selected it — this is this
// mission's resolution of E-007: the local sweeper's RetentionSweep
// (core/event/log/retention.go) and the fleet sweeper's adapter here
// are the only two callers of ArchiveAndDelete / a bare DeleteRows in
// the tree, and only RetentionSweep's own delete_after_window branch
// ever calls DeleteRows without archiving first — a deliberate
// no-archive strategy by definition, not a second uncoordinated
// deleter.
type fleetAuditRetentionBackend struct {
	backend sqlEventLogBackend
	dataDir string
}

// NewFleetAuditRetentionBackend constructs the adapter. dataDir is
// where archived rows are written (<dataDir>/audit-archive/, same
// location and JSONL rotation the local sweeper's archive_after_window
// strategy uses).
func NewFleetAuditRetentionBackend(backend sqlEventLogBackend, dataDir string) fleet.AuditRetentionBackend {
	return &fleetAuditRetentionBackend{backend: backend, dataDir: dataDir}
}

func (a *fleetAuditRetentionBackend) SelectBefore(ctx context.Context, cutoff time.Time, limit int) ([]fleet.AuditRetentionRow, error) {
	rows, err := a.backend.SelectBefore(ctx, cutoff, limit)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.AuditRetentionRow, len(rows))
	for i, r := range rows {
		out[i] = fleet.AuditRetentionRow{EventID: r.EventID, EmittedAt: r.EmittedAt}
	}
	return out, nil
}

func (a *fleetAuditRetentionBackend) DeleteRows(ctx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	rows := make([]eventlog.Row, 0, len(eventIDs))
	for _, id := range eventIDs {
		row, err := a.backend.GetRow(ctx, id)
		if err != nil {
			if errors.Is(err, eventlog.ErrNotFound) {
				// Already gone — e.g. the local sweeper (or a prior
				// fleet pass) deleted it first. Not an error: nothing
				// left to archive or delete for this id.
				continue
			}
			return err
		}
		rows = append(rows, row)
	}
	_, _, err := eventlog.ArchiveAndDelete(ctx, a.backend, a.dataDir, rows)
	return err
}
