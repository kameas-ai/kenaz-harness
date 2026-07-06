// Package compliance — impl.go
//
// API is the concrete ComplianceAPI implementation wired at boot time.
// It delegates to the AuditArchiver (for status, archive-now, cursor)
// and the AuditRetentionSweeper (for retention knob).
//
// When the archiver or sweeper is nil (CapAuditLogImmuDB not enabled or
// fleet disabled), status returns Enabled=false and mutating methods
// return ErrComplianceNotEnabled.
//
// (fleet-audit-archival-01NDFSEX13 WP05)
package compliance

import (
	"context"
	"errors"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// ErrComplianceNotEnabled is returned by ArchiveNow / SetRetention when
// the CapAuditLogImmuDB capability is not active.
var ErrComplianceNotEnabled = errors.New("compliance: audit_log_immudb capability not enabled; upgrade to Team tier")

// API is the concrete ComplianceAPI implementation.
//
// Thread-safe: all state mutation is delegated to the archiver and
// sweeper which are internally synchronized.
type API struct {
	archiver *fleet.AuditArchiver
	sweeper  *fleet.AuditRetentionSweeper
	capCheck func() bool
}

// NewAPI constructs the compliance view. Both archiver and sweeper may
// be nil (capability not enabled); in that case the view returns
// Enabled=false and errors on mutating methods.
func NewAPI(archiver *fleet.AuditArchiver, sweeper *fleet.AuditRetentionSweeper, capCheck func() bool) *API {
	return &API{
		archiver: archiver,
		sweeper:  sweeper,
		capCheck: capCheck,
	}
}

// Status implements ComplianceAPI.
func (a *API) Status(ctx context.Context) (ComplianceStatus, error) {
	enabled := a.capCheck != nil && a.capCheck()
	status := ComplianceStatus{
		Enabled:       enabled,
		RetentionDays: fleet.DefaultAuditRetentionDays,
	}
	if a.archiver != nil {
		lat := a.archiver.LastArchivedAt()
		if !lat.IsZero() {
			status.LastArchivedAt = lat.UTC().Format(time.RFC3339)
		}
		status.PendingCount = a.archiver.PendingCount()
		status.ChainBreak = a.archiver.ChainBreakDetected()
		status.ArchiverRunning = a.archiver.IsRunning()
	}
	if a.sweeper != nil {
		status.RetentionDays = a.sweeper.RetentionDays()
	}
	return status, nil
}

// ArchiveNow implements ComplianceAPI.
func (a *API) ArchiveNow(ctx context.Context) error {
	if !a.enabled() {
		return ErrComplianceNotEnabled
	}
	if a.archiver == nil {
		return ErrComplianceNotEnabled
	}
	return a.archiver.ArchiveNow(ctx)
}

// SetRetention implements ComplianceAPI.
func (a *API) SetRetention(_ context.Context, days int) error {
	if !a.enabled() {
		return ErrComplianceNotEnabled
	}
	if a.sweeper == nil {
		return ErrComplianceNotEnabled
	}
	a.sweeper.SetRetentionDays(days)
	return nil
}

func (a *API) enabled() bool {
	if a.capCheck == nil {
		return true // unconfigured = unrestricted (tests/dev)
	}
	return a.capCheck()
}
