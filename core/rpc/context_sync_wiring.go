// context_sync_wiring.go — adapters that bridge core/fleet concrete types to
// the core/rpc/views/contextsync narrow interfaces.
//
// This file lives in package rpc so it can legally import core/fleet (the
// no-fleet-imports guard allows core/rpc to import core/fleet). The view
// package (core/rpc/views/contextsync) itself remains fleet-free.
package rpc

import (
	"context"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	contextsyncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
)

// ── sessionSyncBackendAdapter ─────────────────────────────────────────────────

// sessionSyncBackendAdapter wraps *fleet.SessionSyncer and implements
// contextsyncview.SessionSyncBackend.
type sessionSyncBackendAdapter struct {
	ss *corefleet.SessionSyncer
}

func (a *sessionSyncBackendAdapter) EnableSync(ctx context.Context, sessionID string, events []contextsyncview.SessionEventRecord) error {
	fleet := make([]corefleet.SessionEventRecord, 0, len(events))
	for _, r := range events {
		fleet = append(fleet, corefleet.SessionEventRecord{Seq: r.Seq, Bytes: r.Bytes})
	}
	return a.ss.EnableSync(ctx, sessionID, fleet)
}

func (a *sessionSyncBackendAdapter) DisableSync(ctx context.Context, sessionID string) error {
	return a.ss.DisableSync(ctx, sessionID)
}

func (a *sessionSyncBackendAdapter) Resume(ctx context.Context, sessionID string, sinceSeq uint64, apply func(contextsyncview.SessionEventRecord) error) error {
	return a.ss.Resume(ctx, sessionID, sinceSeq, func(r corefleet.SessionEventRecord) error {
		return apply(contextsyncview.SessionEventRecord{Seq: r.Seq, Bytes: r.Bytes})
	})
}

func (a *sessionSyncBackendAdapter) DeleteRemote(ctx context.Context, sessionID string) error {
	return a.ss.DeleteRemote(ctx, sessionID)
}

func (a *sessionSyncBackendAdapter) IsSyncEnabled(sessionID string) bool {
	return a.ss.IsSyncEnabled(sessionID)
}

// ── projectSyncBackendAdapter ─────────────────────────────────────────────────

// projectSyncBackendAdapter wraps *fleet.ProjectSyncer and implements
// contextsyncview.ProjectSyncBackend.
type projectSyncBackendAdapter struct {
	ps *corefleet.ProjectSyncer
}

func (a *projectSyncBackendAdapter) EnableSync(ctx context.Context, projectID string, events []contextsyncview.ProjectEventRecord, opts contextsyncview.ProjectSyncOpts) error {
	fleet := make([]corefleet.ProjectEventRecord, 0, len(events))
	for _, r := range events {
		fleet = append(fleet, corefleet.ProjectEventRecord{
			Seq:           r.Seq,
			Bytes:         r.Bytes,
			ArtifactClass: corefleet.ArtifactClass(r.ArtifactClass),
		})
	}
	return a.ps.EnableSync(ctx, projectID, fleet, toFleetOpts(opts))
}

func (a *projectSyncBackendAdapter) DisableSync(ctx context.Context, projectID string) error {
	return a.ps.DisableSync(ctx, projectID)
}

func (a *projectSyncBackendAdapter) DeleteRemote(ctx context.Context, projectID string) error {
	return a.ps.DeleteRemote(ctx, projectID)
}

func (a *projectSyncBackendAdapter) SetArtifactClassOptions(projectID string, opts contextsyncview.ProjectSyncOpts) error {
	return a.ps.SetArtifactClassOptions(projectID, toFleetOpts(opts))
}

func (a *projectSyncBackendAdapter) GetArtifactClassOptions(projectID string) contextsyncview.ProjectSyncOpts {
	return fromFleetOpts(a.ps.GetArtifactClassOptions(projectID))
}

func (a *projectSyncBackendAdapter) IsSyncEnabled(projectID string) bool {
	return a.ps.IsSyncEnabled(projectID)
}

func toFleetOpts(o contextsyncview.ProjectSyncOpts) corefleet.ArtifactClassOptions {
	return corefleet.ArtifactClassOptions{Notes: o.Notes, Binaries: o.Binaries, Memory: o.Memory}
}

func fromFleetOpts(o corefleet.ArtifactClassOptions) contextsyncview.ProjectSyncOpts {
	return contextsyncview.ProjectSyncOpts{Notes: o.Notes, Binaries: o.Binaries, Memory: o.Memory}
}

// ── handoffBackendAdapter ─────────────────────────────────────────────────────

// handoffBackendAdapter wraps *fleet.HandoffHandler and implements
// contextsyncview.HandoffBackend.
type handoffBackendAdapter struct {
	hh *corefleet.HandoffHandler
}

func (a *handoffBackendAdapter) ListTeam(ctx context.Context) ([]contextsyncview.TeamMemberRecord, error) {
	members, err := a.hh.ListTeam(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contextsyncview.TeamMemberRecord, 0, len(members))
	for _, m := range members {
		out = append(out, contextsyncview.TeamMemberRecord{
			UserID:      m.UserID,
			DisplayName: m.DisplayName,
			Email:       m.Email,
		})
	}
	return out, nil
}

func (a *handoffBackendAdapter) ShareSession(ctx context.Context, sessionID, recipientUserID string, plainEvents []contextsyncview.SessionEventRecord) error {
	fleet := make([]corefleet.SessionEventRecord, 0, len(plainEvents))
	for _, r := range plainEvents {
		fleet = append(fleet, corefleet.SessionEventRecord{Seq: r.Seq, Bytes: r.Bytes})
	}
	return a.hh.ShareSession(ctx, sessionID, recipientUserID, fleet)
}

func (a *handoffBackendAdapter) Inbox(ctx context.Context) ([]contextsyncview.InboxItemRecord, error) {
	items, err := a.hh.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contextsyncview.InboxItemRecord, 0, len(items))
	for _, it := range items {
		var receivedAt string
		if !it.ReceivedAt.IsZero() {
			receivedAt = it.ReceivedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		out = append(out, contextsyncview.InboxItemRecord{
			InboxItemID:  it.InboxItemID,
			SessionID:    it.SessionID,
			SenderUserID: it.SenderUserID,
			SenderEmail:  it.SenderEmail,
			ReceivedAt:   receivedAt,
		})
	}
	return out, nil
}

func (a *handoffBackendAdapter) AcceptShare(ctx context.Context, inboxItemID string) ([]contextsyncview.SessionEventRecord, error) {
	records, err := a.hh.AcceptShare(ctx, inboxItemID)
	if err != nil {
		return nil, err
	}
	out := make([]contextsyncview.SessionEventRecord, 0, len(records))
	for _, r := range records {
		out = append(out, contextsyncview.SessionEventRecord{Seq: r.Seq, Bytes: r.Bytes})
	}
	return out, nil
}

// ── recoveryBackendAdapter ────────────────────────────────────────────────────

// recoveryBackendAdapter implements contextsyncview.RecoveryBackend by
// delegating to the context_crypto.go package-level functions.
type recoveryBackendAdapter struct{}

func (r *recoveryBackendAdapter) GenerateRecoveryCode() (string, error) {
	seed, err := corefleet.SeedKey()
	if err != nil {
		return "", err
	}
	return corefleet.MintRecoveryCode(seed)
}

func (r *recoveryBackendAdapter) ApplyRecoveryCode(code string) error {
	seed, err := corefleet.UseRecoveryCode(code)
	if err != nil {
		return err
	}
	return corefleet.StoreContextSeed(seed)
}
