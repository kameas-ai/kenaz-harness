package contextsync

import (
	"context"
	"errors"
	"fmt"

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
		defaultOpts := ProjectSyncOpts{Notes: true, Binaries: false, Memory: true}
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

// Handoff_Share re-encrypts a session and routes it to the recipient's inbox.
// Plain events are loaded by the backend; no content crosses the RPC boundary.
func (im *Impl) Handoff_Share(ctx context.Context, sessionID, recipientUserID string) error {
	if im.Handoff == nil {
		return ErrContextSyncUnavailable
	}
	// Pass an empty slice — the backend loads events from the encrypted
	// fleet stream via Resume in a future enhancement. For v0.21.0 the
	// caller is responsible for providing events through the chassis path.
	// The RPC binding intentionally accepts only opaque IDs.
	return im.Handoff.ShareSession(ctx, sessionID, recipientUserID, nil)
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
		})
	}
	return out, nil
}

// Handoff_Accept decrypts the inbox item. Returns a view with the event count;
// no content crosses the RPC boundary.
func (im *Impl) Handoff_Accept(ctx context.Context, inboxItemID string) (AcceptedSessionView, error) {
	if im.Handoff == nil {
		return AcceptedSessionView{}, ErrContextSyncUnavailable
	}
	records, err := im.Handoff.AcceptShare(ctx, inboxItemID)
	if err != nil {
		return AcceptedSessionView{}, fmt.Errorf("contextsync: accept share: %w", err)
	}
	// LocalSessionID is empty in v0.21.0 — a future WP wires session
	// persistence so the caller gets a real ID. For now the event count
	// tells the UI the operation succeeded.
	return AcceptedSessionView{
		LocalSessionID: "",
		EventCount:     len(records),
	}, nil
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
	return ArtifactClassOptionsView{Notes: o.Notes, Binaries: o.Binaries, Memory: o.Memory}
}

func viewToOpts(v ArtifactClassOptionsView) ProjectSyncOpts {
	return ProjectSyncOpts{Notes: v.Notes, Binaries: v.Binaries, Memory: v.Memory}
}
