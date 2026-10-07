// context_sync_wiring.go — adapters that bridge core/fleet concrete types to
// the core/rpc/views/contextsync narrow interfaces.
//
// This file lives in package rpc so it can legally import core/fleet (the
// no-fleet-imports guard allows core/rpc to import core/fleet). The view
// package (core/rpc/views/contextsync) itself remains fleet-free.
package rpc

import (
	"context"
	"errors"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	contextsyncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
)

// ── sessionSyncBackendAdapter ─────────────────────────────────────────────────

// sessionSyncBackendAdapter wraps *fleet.SessionSyncer and implements
// contextsyncview.SessionSyncBackend.
type sessionSyncBackendAdapter struct {
	ss *corefleet.SessionSyncer
	// breaker is the context-sync append breaker (fleet-session-truth-
	// 01DOGF0A); toggling sync for a session clears its breaker state —
	// re-enabling is the user's explicit retry, disabling means the session
	// is no longer "not syncing", it is simply not synced. May be nil.
	breaker *corefleet.AppendBreaker
}

func (a *sessionSyncBackendAdapter) EnableSync(ctx context.Context, sessionID string, events []contextsyncview.SessionEventRecord) error {
	fleet := make([]corefleet.SessionEventRecord, 0, len(events))
	for _, r := range events {
		fleet = append(fleet, corefleet.SessionEventRecord{Seq: r.Seq, Bytes: r.Bytes})
	}
	err := a.ss.EnableSync(ctx, sessionID, fleet)
	if err == nil {
		a.breaker.Reset(sessionID)
	}
	return err
}

func (a *sessionSyncBackendAdapter) DisableSync(ctx context.Context, sessionID string) error {
	err := a.ss.DisableSync(ctx, sessionID)
	a.breaker.Reset(sessionID)
	return err
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
	return corefleet.ArtifactClassOptions{Notes: o.Notes, Binaries: o.Binaries}
}

func fromFleetOpts(o corefleet.ArtifactClassOptions) contextsyncview.ProjectSyncOpts {
	return contextsyncview.ProjectSyncOpts{Notes: o.Notes, Binaries: o.Binaries}
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
			CanReceive:  m.CanReceive,
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
//
// Importing a recovery code replaces the context seed, which changes this
// device's derived handoff key (seed + node id). The adapter therefore
// re-registers the device keys via PUT /api/v1/me/nodes/{node_id}/keys —
// the exact use case fleet's contract names for that route
// (device-keys-handoff-01DEVKH01 WP02, spec §4 consequence (a)). A 403
// node_removed there takes the same terminal sign-out as enroll.
type recoveryBackendAdapter struct {
	// client / dataDir are nil/"" when fleet is not wired; the import then
	// stays local and the next enroll registers the new key.
	client  *corefleet.Client
	dataDir string
	// onNodeRemoved applies the node_removed sign-out (settings API).
	onNodeRemoved func()
	// register is the PUT seam (nil → client.RegisterDeviceKeys); tests
	// substitute it.
	register func(ctx context.Context, nodeID string) error
}

// recoveryReRegisterTimeout bounds the post-import PUT keys.
const recoveryReRegisterTimeout = 15 * time.Second

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
	if err := corefleet.StoreContextSeed(seed); err != nil {
		return err
	}
	r.reRegisterKeys()
	return nil
}

// reRegisterKeys pushes the post-import handoff key to fleet. Best-effort:
// the import itself already succeeded locally, and the next enroll sends
// the same derived key; failures are logged and recorded in the client's
// KeyRegistration (surfaced on the session snapshot).
func (r *recoveryBackendAdapter) reRegisterKeys() {
	if r == nil || r.client == nil || r.client.IsNop() {
		return
	}
	nodeID := corefleet.ReadNodeID(r.dataDir)
	if nodeID == "" {
		return // never enrolled on this install: enroll registers keys
	}
	if ok, _ := r.client.SignedIn(context.Background()); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), recoveryReRegisterTimeout)
	defer cancel()
	register := r.register
	if register == nil {
		register = r.client.RegisterDeviceKeys
	}
	err := register(ctx, nodeID)
	switch {
	case err == nil:
		logging.L().Info("rpc.context_sync.recovery_keys_reregistered")
	case errors.Is(err, corefleet.ErrNodeRemoved):
		logging.L().Warn("rpc.context_sync.recovery_keys_node_removed")
		if r.onNodeRemoved != nil {
			r.onNodeRemoved()
		}
	default:
		logging.L().Warn("rpc.context_sync.recovery_keys_reregister_failed", "err", err.Error())
	}
}
