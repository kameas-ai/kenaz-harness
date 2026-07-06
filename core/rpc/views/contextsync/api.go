// Package contextsync exposes the RPC surface for private E2E-encrypted
// session/agent-memory continuity across devices (fleet-context-sync-01NDFSEX15).
//
// All methods are received by the frontend via Wails bindings in the form
// SessionSync_Op, ProjectSync_Op, Handoff_Op, and ContextSync_Op. The
// implementations are injected via Impl so that core/rpc/views/contextsync
// never imports core/fleet directly (the no-fleet-imports guard allows only
// core/rpc, core/rpc/views/settings, core/rpc/views/fleet,
// core/rpc/middleware, and core/mcp/builtin/sites).
//
// Privacy invariant: no plaintext session or project content may appear in
// any argument, return value, log line, or audit payload. Only opaque IDs
// and status flags cross the RPC boundary.
package contextsync

import "context"

// SessionSyncStatus is the RPC-facing view of per-session sync state.
type SessionSyncStatus struct {
	Enabled bool `json:"enabled"`
}

// ProjectSyncStatus is the RPC-facing view of per-project sync state.
type ProjectSyncStatus struct {
	Enabled bool `json:"enabled"`
	// ArtifactClassOptions describes which artifact classes are included.
	ArtifactClassOptions ArtifactClassOptionsView `json:"artifactClassOptions"`
}

// ArtifactClassOptionsView is the RPC-facing projection of ArtifactClassOptions.
type ArtifactClassOptionsView struct {
	Notes    bool `json:"notes"`
	Binaries bool `json:"binaries"`
	Memory   bool `json:"memory"`
}

// TeamMemberView is the RPC-facing projection of a fleet team member.
type TeamMemberView struct {
	UserID      string `json:"userID"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// InboxItemView is the RPC-facing projection of a shared-session inbox item.
type InboxItemView struct {
	InboxItemID  string `json:"inboxItemID"`
	SessionID    string `json:"sessionID"`
	SenderUserID string `json:"senderUserID"`
	SenderEmail  string `json:"senderEmail"`
	ReceivedAt   string `json:"receivedAt"` // RFC3339
}

// AcceptedSessionView is returned by Handoff_Accept after the shared events
// have been decrypted. Contains only the new local session ID — no content.
type AcceptedSessionView struct {
	// LocalSessionID is the locally created session holding the decrypted events.
	LocalSessionID string `json:"localSessionID"`
	// EventCount is the number of replayed events.
	EventCount int `json:"eventCount"`
}

// ContextSyncAPI is the view-scoped interface for the fleet context-sync
// surface. All methods that touch session or project events accept only
// opaque IDs — they never accept or return plaintext event content.
type ContextSyncAPI interface {
	// SessionSync_Toggle enables or disables fleet sync for a session. When
	// enabling, the implementation backfills existing events (encrypted) to
	// the fleet.
	SessionSync_Toggle(ctx context.Context, sessionID string, enable bool) (SessionSyncStatus, error)

	// SessionSync_DeleteRemote purges the remote stream for a session from
	// the fleet and clears the sync toggle. The local session is untouched.
	SessionSync_DeleteRemote(ctx context.Context, sessionID string) error

	// SessionSync_ResumeFrom replays fleet events since sinceSeq for a session.
	// Returns the number of events applied.
	SessionSync_ResumeFrom(ctx context.Context, sessionID string, sinceSeq uint64) (int, error)

	// ProjectSync_Toggle enables or disables fleet sync for a project.
	ProjectSync_Toggle(ctx context.Context, projectID string, enable bool) (ProjectSyncStatus, error)

	// ProjectSync_DeleteRemote purges the remote stream for a project.
	ProjectSync_DeleteRemote(ctx context.Context, projectID string) error

	// ProjectSync_SetArtifactClass updates which artifact classes are included
	// in fleet sync for a project.
	ProjectSync_SetArtifactClass(ctx context.Context, projectID string, opts ArtifactClassOptionsView) (ProjectSyncStatus, error)

	// Handoff_ListTeam returns the team members available as handoff recipients.
	Handoff_ListTeam(ctx context.Context) ([]TeamMemberView, error)

	// Handoff_Share re-encrypts a session for a recipient and posts to the fleet
	// handoff inbox. The implementation loads the events from the local session
	// store — no plaintext crosses the RPC boundary.
	Handoff_Share(ctx context.Context, sessionID, recipientUserID string) error

	// Handoff_Inbox returns the current contents of the fleet handoff inbox.
	Handoff_Inbox(ctx context.Context) ([]InboxItemView, error)

	// Handoff_Accept decrypts an inbox item and returns a view with the count
	// of decrypted events and an opaque local session ID for persistence.
	Handoff_Accept(ctx context.Context, inboxItemID string) (AcceptedSessionView, error)

	// ContextSync_GenerateRecoveryCode mints a recovery code for the device
	// context seed. The code is displayed once and must be acknowledged.
	ContextSync_GenerateRecoveryCode(ctx context.Context) (string, error)

	// ContextSync_ApplyRecoveryCode imports a recovery code, overwriting the
	// local context seed with the decoded seed.
	ContextSync_ApplyRecoveryCode(ctx context.Context, code string) error
}

// ── Injected interfaces ───────────────────────────────────────────────────────
// These narrow interfaces decouple the view from core/fleet concrete types.
// core/rpc wires the *fleet.SessionSyncer / *fleet.ProjectSyncer /
// *fleet.HandoffHandler into these interfaces in api.go.

// SessionSyncBackend is the narrow contract that Impl needs from the
// fleet session-sync layer.
type SessionSyncBackend interface {
	EnableSync(ctx context.Context, sessionID string, existingEvents []SessionEventRecord) error
	DisableSync(ctx context.Context, sessionID string) error
	Resume(ctx context.Context, sessionID string, sinceSeq uint64, apply func(SessionEventRecord) error) error
	DeleteRemote(ctx context.Context, sessionID string) error
	IsSyncEnabled(sessionID string) bool
}

// ProjectSyncBackend is the narrow contract that Impl needs from the
// fleet project-sync layer.
type ProjectSyncBackend interface {
	EnableSync(ctx context.Context, projectID string, existingEvents []ProjectEventRecord, opts ProjectSyncOpts) error
	DisableSync(ctx context.Context, projectID string) error
	DeleteRemote(ctx context.Context, projectID string) error
	SetArtifactClassOptions(projectID string, opts ProjectSyncOpts) error
	GetArtifactClassOptions(projectID string) ProjectSyncOpts
	IsSyncEnabled(projectID string) bool
}

// HandoffBackend is the narrow contract that Impl needs from the fleet
// team-handoff layer.
type HandoffBackend interface {
	ListTeam(ctx context.Context) ([]TeamMemberRecord, error)
	// ShareSession accepts the opaque session ID + recipient + already-loaded plain
	// events (loaded by the Impl from the local store before calling the backend).
	ShareSession(ctx context.Context, sessionID, recipientUserID string, plainEvents []SessionEventRecord) error
	Inbox(ctx context.Context) ([]InboxItemRecord, error)
	AcceptShare(ctx context.Context, inboxItemID string) ([]SessionEventRecord, error)
}

// RecoveryBackend handles context-seed recovery code mint + apply.
type RecoveryBackend interface {
	GenerateRecoveryCode() (string, error)
	ApplyRecoveryCode(code string) error
}

// ── Bridge types ──────────────────────────────────────────────────────────────
// Plain Go structs exchanged between the view and backends; no core/fleet
// dependency. core/rpc's wiring layer maps fleet.* → these types.

// SessionEventRecord is the payload exchanged with backends (opaque bytes).
// Privacy invariant: Bytes is never logged.
type SessionEventRecord struct {
	Seq   uint64
	Bytes []byte
}

// ProjectEventRecord is the payload exchanged with the project sync backend.
// Privacy invariant: Bytes is never logged.
type ProjectEventRecord struct {
	Seq           uint64
	Bytes         []byte
	ArtifactClass string // "notes", "binaries", "memory", or ""
}

// ProjectSyncOpts maps 1:1 to fleet.ArtifactClassOptions.
type ProjectSyncOpts struct {
	Notes    bool
	Binaries bool
	Memory   bool
}

// TeamMemberRecord is the raw fleet team member, mapped from fleet.TeamMember.
type TeamMemberRecord struct {
	UserID      string
	DisplayName string
	Email       string
}

// InboxItemRecord is the raw fleet inbox item, mapped from fleet.InboxItem.
type InboxItemRecord struct {
	InboxItemID  string
	SessionID    string
	SenderUserID string
	SenderEmail  string
	ReceivedAt   string // RFC3339
}
