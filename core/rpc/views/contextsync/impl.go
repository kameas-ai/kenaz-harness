package contextsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// ErrContextSyncUnavailable is returned when a context-sync method is called
// but the backend is not wired (fleet disabled / OSS build).
var ErrContextSyncUnavailable = errors.New("contextsync: not wired — fleet disabled or OSS build")

// Impl implements ContextSyncAPI backed by injected backends.
// All fields may be nil; every method nil-guards and returns
// ErrContextSyncUnavailable so the surface degrades gracefully.
type Impl struct {
	Session  SessionSyncBackend
	Project  ProjectSyncBackend
	Handoff  HandoffBackend
	Recovery RecoveryBackend

	// SessionEvents loads the local session Handoff_Share sends
	// (device-keys-handoff-01DEVKH01 WP04 — closes unwired-ledger
	// 2026-10-06 item 1, which hardcoded nil events). nil → Handoff_Share
	// refuses rather than posting an empty handoff.
	SessionEvents SessionEventLoader

	// Accepted persists accepted handoffs as local sessions (WP05). nil →
	// Handoff_Accept refuses rather than decrypting into nowhere.
	Accepted AcceptedSessionStore

	// Gate is the Cedar policy gate consulted before the two DESTRUCTIVE
	// ContextSync operations — SessionSync_DeleteRemote and
	// ProjectSync_DeleteRemote (fleet-enforcement-truth-01PMZ505 WP13,
	// owner ruling G-7). May be nil (pre-boot / test posture); the
	// gate-hook helpers in core/policy/cedar degrade to default-allow in
	// that case, matching every other Cedar-gated RPC view in this repo.
	// Toggle (SessionSync_Toggle / ProjectSync_Toggle) and resume
	// (SessionSync_ResumeFrom) are deliberately NOT gated by this field —
	// ruling G-7 leaves them ungated for now.
	Gate cedar.Gate
}

var _ ContextSyncAPI = (*Impl)(nil)

// ── SessionSync ───────────────────────────────────────────────────────────────

// SessionSync_Toggle enables or disables fleet sync for sessionID.
// When enabling, the implementation uses the zero-length empty slice as
// existingEvents (callers that want backfill must call EnableSync directly
// before wiring). The RPC layer does not expose plaintext events.
func (im *Impl) SessionSync_Toggle(ctx context.Context, sessionID string, enable bool) (SessionSyncStatus, error) {
	if im.Session == nil {
		return SessionSyncStatus{}, ErrContextSyncUnavailable
	}
	if enable {
		// Backfill with empty — callers that need real backfill call the
		// backend directly from the chassis (not via RPC) to avoid passing
		// plaintext across the binding boundary.
		if err := im.Session.EnableSync(ctx, sessionID, nil); err != nil {
			return SessionSyncStatus{}, fmt.Errorf("contextsync: session enable: %w", err)
		}
		return SessionSyncStatus{Enabled: true}, nil
	}
	if err := im.Session.DisableSync(ctx, sessionID); err != nil {
		return SessionSyncStatus{}, fmt.Errorf("contextsync: session disable: %w", err)
	}
	return SessionSyncStatus{Enabled: false}, nil
}

// SessionSync_DeleteRemote purges the remote stream for sessionID.
//
// Gated by ActionContextSyncSessionPurge (ruling G-7): the Cedar check
// runs BEFORE the backend call, and on Deny (which includes NotApplicable
// — see CheckContextSyncSessionPurge's doc) nothing is purged.
func (im *Impl) SessionSync_DeleteRemote(ctx context.Context, sessionID string) error {
	if im.Session == nil {
		return ErrContextSyncUnavailable
	}
	if err := cedar.CheckContextSyncSessionPurge(ctx, im.Gate, sessionID); err != nil {
		return fmt.Errorf("contextsync: session delete remote denied by policy: %w", err)
	}
	return im.Session.DeleteRemote(ctx, sessionID)
}

// SessionSync_ResumeFrom replays fleet events from sinceSeq and returns the
// count of applied events. Events are decrypted in the fleet layer; the
// view receives only the count (no content crosses the RPC boundary).
func (im *Impl) SessionSync_ResumeFrom(ctx context.Context, sessionID string, sinceSeq uint64) (int, error) {
	if im.Session == nil {
		return 0, ErrContextSyncUnavailable
	}
	var count int
	err := im.Session.Resume(ctx, sessionID, sinceSeq, func(_ SessionEventRecord) error {
		count++
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("contextsync: session resume: %w", err)
	}
	return count, nil
}

// ── ProjectSync ───────────────────────────────────────────────────────────────

// ProjectSync_Toggle enables or disables fleet sync for projectID.
func (im *Impl) ProjectSync_Toggle(ctx context.Context, projectID string, enable bool) (ProjectSyncStatus, error) {
	if im.Project == nil {
		return ProjectSyncStatus{}, ErrContextSyncUnavailable
	}
	if enable {
		// Use default artifact class options; callers may update via
		// ProjectSync_SetArtifactClass after enabling.
		defaultOpts := ProjectSyncOpts{Notes: true, Binaries: false}
		if err := im.Project.EnableSync(ctx, projectID, nil, defaultOpts); err != nil {
			return ProjectSyncStatus{}, fmt.Errorf("contextsync: project enable: %w", err)
		}
		opts := im.Project.GetArtifactClassOptions(projectID)
		return ProjectSyncStatus{
			Enabled:              true,
			ArtifactClassOptions: optsToView(opts),
		}, nil
	}
	if err := im.Project.DisableSync(ctx, projectID); err != nil {
		return ProjectSyncStatus{}, fmt.Errorf("contextsync: project disable: %w", err)
	}
	return ProjectSyncStatus{Enabled: false}, nil
}

// ProjectSync_DeleteRemote purges the remote stream for projectID.
//
// Gated by ActionContextSyncProjectPurge (ruling G-7): the Cedar check
// runs BEFORE the backend call, and on Deny (which includes NotApplicable
// — see CheckContextSyncProjectPurge's doc) nothing is purged.
func (im *Impl) ProjectSync_DeleteRemote(ctx context.Context, projectID string) error {
	if im.Project == nil {
		return ErrContextSyncUnavailable
	}
	if err := cedar.CheckContextSyncProjectPurge(ctx, im.Gate, projectID); err != nil {
		return fmt.Errorf("contextsync: project delete remote denied by policy: %w", err)
	}
	return im.Project.DeleteRemote(ctx, projectID)
}

// ProjectSync_SetArtifactClass updates the per-artifact-class opt-in for
// projectID and returns the current status.
func (im *Impl) ProjectSync_SetArtifactClass(ctx context.Context, projectID string, opts ArtifactClassOptionsView) (ProjectSyncStatus, error) {
	if im.Project == nil {
		return ProjectSyncStatus{}, ErrContextSyncUnavailable
	}
	backendOpts := viewToOpts(opts)
	if err := im.Project.SetArtifactClassOptions(projectID, backendOpts); err != nil {
		return ProjectSyncStatus{}, fmt.Errorf("contextsync: set artifact class: %w", err)
	}
	return ProjectSyncStatus{
		Enabled:              im.Project.IsSyncEnabled(projectID),
		ArtifactClassOptions: opts,
	}, nil
}

// ── Handoff ───────────────────────────────────────────────────────────────────

// Handoff_ListTeam returns team members available as handoff recipients.
func (im *Impl) Handoff_ListTeam(ctx context.Context) ([]TeamMemberView, error) {
	if im.Handoff == nil {
		return nil, ErrContextSyncUnavailable
	}
	members, err := im.Handoff.ListTeam(ctx)
	if err != nil {
		return nil, fmt.Errorf("contextsync: list team: %w", err)
	}
	out := make([]TeamMemberView, 0, len(members))
	for _, m := range members {
		out = append(out, TeamMemberView{
			UserID:      m.UserID,
			DisplayName: m.DisplayName,
			Email:       m.Email,
			CanReceive:  m.CanReceive,
		})
	}
	return out, nil
}

// ErrHandoffNothingToShare is returned by Handoff_Share for a session with
// no messages — before any network call (fleet would 422 handoff_empty).
var ErrHandoffNothingToShare = errors.New("This session has no messages to share yet.")

// ErrHandoffLoaderUnavailable means the local session loader is not wired.
var ErrHandoffLoaderUnavailable = errors.New("contextsync: session loader not wired — cannot share")

// Handoff_Share loads the session's full transcript from the local store
// as self-contained events, and hands them to the backend, which encrypts
// them and routes them to the recipient's inbox. Only opaque ids cross the
// RPC boundary. Errors from the backend are returned UNWRAPPED: their text
// is the human copy the share dialog shows.
func (im *Impl) Handoff_Share(ctx context.Context, sessionID, recipientUserID string) error {
	if im.Handoff == nil {
		return ErrContextSyncUnavailable
	}
	if im.SessionEvents == nil {
		return ErrHandoffLoaderUnavailable
	}
	events, err := im.SessionEvents.LoadSessionEvents(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("contextsync: load session for sharing: %w", err)
	}
	if len(events) == 0 {
		return ErrHandoffNothingToShare
	}
	return im.Handoff.ShareSession(ctx, sessionID, recipientUserID, events)
}

// Handoff_RecipientDevices lists a teammate's receiving devices.
func (im *Impl) Handoff_RecipientDevices(ctx context.Context, recipientUserID string) ([]RecipientDeviceView, error) {
	if im.Handoff == nil {
		return nil, ErrContextSyncUnavailable
	}
	devs, err := im.Handoff.RecipientDevices(ctx, recipientUserID)
	if err != nil {
		return nil, err
	}
	if devs == nil {
		devs = []RecipientDeviceView{}
	}
	return devs, nil
}

// Handoff_Inbox returns the current fleet handoff inbox.
func (im *Impl) Handoff_Inbox(ctx context.Context) ([]InboxItemView, error) {
	if im.Handoff == nil {
		return nil, ErrContextSyncUnavailable
	}
	items, err := im.Handoff.Inbox(ctx)
	if err != nil {
		return nil, fmt.Errorf("contextsync: inbox: %w", err)
	}
	out := make([]InboxItemView, 0, len(items))
	for _, it := range items {
		out = append(out, InboxItemView{
			InboxItemID:  it.InboxItemID,
			SessionID:    it.SessionID,
			SenderUserID: it.SenderUserID,
			SenderEmail:  it.SenderEmail,
			ReceivedAt:   it.ReceivedAt,

			Undecryptable: it.Undecryptable,
		})
	}
	return out, nil
}

// ErrHandoffAcceptUnavailable means the local accepted-session store is
// not wired, so an accept would have nowhere to persist.
var ErrHandoffAcceptUnavailable = errors.New("contextsync: accepted-session store not wired — cannot accept")

// Handoff_Accept decrypts the inbox item with this device's key, persists
// it as a NEW local session with provenance (sender, inbox item id), audits
// the inbound share, then deletes the fleet copy (OQ-1, owner-ruled: one
// accept per user — the item leaves the user's other devices too). A
// second accept of the same item returns the existing local session with
// no fetch; if the earlier fleet DELETE had failed, it is retried then.
// No content crosses the RPC boundary. Backend errors are returned
// unwrapped: their text is the human copy the inbox shows.
func (im *Impl) Handoff_Accept(ctx context.Context, inboxItemID string) (AcceptedSessionView, error) {
	if im.Handoff == nil {
		return AcceptedSessionView{}, ErrContextSyncUnavailable
	}
	if im.Accepted == nil {
		return AcceptedSessionView{}, ErrHandoffAcceptUnavailable
	}
	if prev, ok := im.Accepted.Lookup(ctx, inboxItemID); ok {
		im.deleteFleetCopy(ctx, inboxItemID)
		prev.AlreadyAccepted = true
		return prev, nil
	}
	rec, err := im.Handoff.AcceptShare(ctx, inboxItemID)
	if err != nil {
		return AcceptedSessionView{}, err
	}
	view, err := im.Accepted.Persist(ctx, rec)
	if err != nil {
		return AcceptedSessionView{}, fmt.Errorf("contextsync: save shared session: %w", err)
	}
	if !view.AlreadyAccepted {
		im.Handoff.RecordAccepted(ctx, rec, view.LocalSessionID)
	}
	im.deleteFleetCopy(ctx, inboxItemID)
	return view, nil
}

// deleteFleetCopy removes the fleet copy of an accepted (persisted) item
// unless that already succeeded. Best-effort: the local copy is
// authoritative, the item expires in 7 days regardless, and the next
// re-open retries a failed delete.
func (im *Impl) deleteFleetCopy(ctx context.Context, inboxItemID string) {
	if im.Accepted.FleetCopyDeleted(ctx, inboxItemID) {
		return
	}
	if err := im.Handoff.DeleteShare(ctx, inboxItemID); err != nil {
		logging.L().Warn("contextsync.handoff.delete_after_accept_failed", "err", err.Error())
		return
	}
	if err := im.Accepted.MarkFleetCopyDeleted(ctx, inboxItemID); err != nil {
		logging.L().Warn("contextsync.handoff.mark_deleted_failed", "err", err.Error())
	}
}

// Handoff_Delete dismisses an inbox item without accepting it.
func (im *Impl) Handoff_Delete(ctx context.Context, inboxItemID string) error {
	if im.Handoff == nil {
		return ErrContextSyncUnavailable
	}
	return im.Handoff.DeleteShare(ctx, inboxItemID)
}

// ── Recovery ──────────────────────────────────────────────────────────────────

// ContextSync_GenerateRecoveryCode mints a one-time recovery code for the
// device context seed.
func (im *Impl) ContextSync_GenerateRecoveryCode(_ context.Context) (string, error) {
	if im.Recovery == nil {
		return "", ErrContextSyncUnavailable
	}
	code, err := im.Recovery.GenerateRecoveryCode()
	if err != nil {
		return "", fmt.Errorf("contextsync: generate recovery code: %w", err)
	}
	return code, nil
}

// ContextSync_ApplyRecoveryCode imports a recovery code, overwriting the
// local context seed.
func (im *Impl) ContextSync_ApplyRecoveryCode(_ context.Context, code string) error {
	if im.Recovery == nil {
		return ErrContextSyncUnavailable
	}
	if err := im.Recovery.ApplyRecoveryCode(code); err != nil {
		return fmt.Errorf("contextsync: apply recovery code: %w", err)
	}
	return nil
}

// ── conversion helpers ────────────────────────────────────────────────────────

func optsToView(o ProjectSyncOpts) ArtifactClassOptionsView {
	return ArtifactClassOptionsView{Notes: o.Notes, Binaries: o.Binaries}
}

func viewToOpts(v ArtifactClassOptionsView) ProjectSyncOpts {
	return ProjectSyncOpts{Notes: v.Notes, Binaries: v.Binaries}
}
