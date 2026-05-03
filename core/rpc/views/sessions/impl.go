package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/llm/pricing"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	autotitle "github.com/sigil-tech/kaneaz-harness/core/sessions/autotitle"
	"github.com/sigil-tech/kaneaz-harness/core/usage"
)

// ErrEmptyContentBlocks is returned by SendMessageWithBlocks when the
// caller passes a nil or empty slice. Mirrors the existing
// AppendMessage behaviour where the manager rejects empty content.
var ErrEmptyContentBlocks = errors.New("rpc/sessions: contentBlocks must be non-empty")

// ErrSessionHasArtifacts is returned by Delete when the session owns
// artifacts but the caller has explicitly opted out of the cascade
// (DeleteArtifacts=false) AND the session has no project to absorb
// the orphans (PromoteArtifactsToProject is meaningful only when a
// project exists). The session row is preserved; the caller must
// retry with one of the two cascade branches.
var ErrSessionHasArtifacts = errors.New("rpc/sessions: session has artifacts; delete or promote them before deleting the session")

// recordToView projects a session.Record into the wire-shape Session
// the frontend consumes. Lives here (not in core/session) to avoid an
// import cycle: this package imports session, so session must not
// import this package.
//
// Timestamps render as RFC3339Nano UTC for byte-stable JSON.
func recordToView(r session.Record) Session {
	kind := r.ContextKind
	if kind == "" {
		// Sessions persisted before the system_prompt feature landed
		// have an empty kind from the SQL DEFAULT; surface the canonical
		// 'system' so the frontend never sees a third value.
		kind = session.ContextKindSystem
	}
	out := Session{
		ID:           r.ID,
		Name:         r.Name,
		CreatedAt:    r.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339Nano),
		SystemPrompt: r.SystemPrompt,
		ContextKind:  kind,
		AutoTitled:   r.AutoTitled,
	}
	if r.ProjectID != nil {
		out.ProjectID = *r.ProjectID
	}
	return out
}

// managerAPI is the real SessionsAPI implementation: a thin adapter
// from the rpc-layer wire shape to the core/session.Manager. It owns
// no state of its own; the Manager is the single source of truth.
//
// Streaming subscriptions (StartStream / StopStream) are not yet
// wired; they remain best-effort no-ops returning empty subscription
// ids until the streaming mission lands. The CRUD surface — which is
// what the rail UI needs to render — is fully functional.
type managerAPI struct {
	mgr         *session.Manager
	attachments *attachments.Manager // optional; nil falls back to legacy column path
	// Artifacts cascade plumbing (artifacts-storage WP02 / FR-014).
	// When all three are non-nil, Delete extends the session-delete
	// cascade with: list artifacts → drop them via the FK CASCADE on
	// session delete → refcount-sweep any orphaned CAS files. Empty
	// triple keeps the pre-WP02 cascade (attachments + session row).
	artStore coreart.Store
	media    attachments.MediaStore
	dataDir  string
	// resumeStarter is the long-turn-resilience-01KR3PRS WP03 seam
	// that opens a continuation chat-stream against the partial
	// assistant row identified by originalMessageID. The chassis wires
	// this to chat.ChatRunner.StartStream with a synthesized
	// continuation prompt; tests can substitute a recording fake.
	// nil disables the seam: ResumeMessage returns
	// ErrResumeNotConfigured.
	resumeStarter ResumeStarter
	// titleGen is the optional auto-title generator used by SuggestTitle.
	// nil causes SuggestTitle to return ErrTitleGeneratorNotConfigured.
	titleGen TitleGenerator
	// usageMgr is the optional usage manager for GetUsage. nil returns
	// zeroed aggregate (feature disabled or not yet wired).
	usageMgr usage.Manager
}

// TitleGenerator is the surface SuggestTitle needs. Matches
// autotitle.Generator.GenerateTitle so the real generator satisfies it
// without an adapter.
type TitleGenerator interface {
	GenerateTitle(ctx context.Context, transcript autotitle.Transcript) (string, error)
}

// ErrTitleGeneratorNotConfigured is returned by SuggestTitle when no
// auto-title generator has been wired into the API.
var ErrTitleGeneratorNotConfigured = errors.New("rpc/sessions: title generator not configured")

// ResumeStarter is the streaming surface ResumeMessage delegates to so
// the sessions package never imports the chat package directly. The
// closure produces a continuation prompt from the partial assistant
// row, opens a fresh ChatRunner subscription, and returns its id.
//
// Production wiring (core/rpc/api.go) supplies an adapter that:
//
//	1. Builds the continuation system-injection prompt from the partial
//	   row's content (last 200 chars).
//	2. Calls chat.ChatRunner.StartStream with the synthesized prompt as
//	   the userMessage so the existing AskBus/HistoryReadNode plumbing
//	   delivers it to the model on the first kernel fire.
//	3. Threads the original message id through to the continuation row
//	   (via the AppendContinuation persistence path inside the kernel
//	   run's terminal SessionWriteNode replacement — see WP03 task
//	   block in plan §Layer 3).
//
// The signature returns the chat-stream subscription id so the resume
// RPC can hand it to the frontend, which drains the same llm:stream-*
// topics it already subscribes to for fresh turns.
//
// long-turn-resilience-01KR3PRS WP03.
type ResumeStarter interface {
	StartResume(ctx context.Context, sessionID, originalMessageID, profileID, modelOverride string) (subscriptionID string, err error)
}

// ResumeStarterFunc adapts a function value to the ResumeStarter
// interface so chassis wiring can pass a closure inline.
type ResumeStarterFunc func(ctx context.Context, sessionID, originalMessageID, profileID, modelOverride string) (string, error)

// StartResume satisfies ResumeStarter.
func (f ResumeStarterFunc) StartResume(ctx context.Context, sessionID, originalMessageID, profileID, modelOverride string) (string, error) {
	return f(ctx, sessionID, originalMessageID, profileID, modelOverride)
}

// WithResumeStarter wires a ResumeStarter into the SessionsAPI so the
// resume RPC can open continuation streams.
func WithResumeStarter(api SessionsAPI, starter ResumeStarter) SessionsAPI {
	if m, ok := api.(*managerAPI); ok {
		m.resumeStarter = starter
	}
	return api
}

// ErrResumeNotConfigured is returned by ResumeMessage when the chassis
// has not wired a ResumeStarter (test paths, boot failures).
var ErrResumeNotConfigured = errors.New("rpc/sessions: resume starter not configured")

// ErrResumeNotRecoverable is returned by ResumeMessage when the partial
// row exists but its streaming_recoverable flag is false (tool_use
// already executed before the drop). The user must re-issue the
// request rather than continue.
var ErrResumeNotRecoverable = errors.New("rpc/sessions: partial message is not recoverable; re-issue the request")

// ErrResumeNotPartial is returned by ResumeMessage when the supplied
// messageID exists but does not carry a streaming_failed_at timestamp
// (the row landed cleanly; there is nothing to resume).
var ErrResumeNotPartial = errors.New("rpc/sessions: message did not stream-fail; nothing to resume")

// ErrResumeAlreadyContinued is returned by ResumeMessage when an
// earlier resume already wrote a continuation row pointing at the
// supplied partial id. Callers should refresh the transcript rather
// than open a duplicate stream.
var ErrResumeAlreadyContinued = errors.New("rpc/sessions: partial message already has a continuation; refresh transcript")

// NewManagerAPI returns a SessionsAPI backed by the supplied Manager.
// Manager must be non-nil; callers (typically core/rpc.New) must
// construct one before invoking this.
func NewManagerAPI(mgr *session.Manager) SessionsAPI {
	if mgr == nil {
		panic("rpc/sessions: NewManagerAPI: nil manager")
	}
	return &managerAPI{mgr: mgr}
}

// NewManagerAPIWithAttachments returns a SessionsAPI that drives the
// attachments table for SetSystemPrompt while keeping the legacy
// session.system_prompt column populated for one-release compat.
//
// TODO: remove the legacy column write next mission post-WP04 once the
// frontend reads attachments directly.
func NewManagerAPIWithAttachments(mgr *session.Manager, att *attachments.Manager) SessionsAPI {
	if mgr == nil {
		panic("rpc/sessions: NewManagerAPI: nil manager")
	}
	return &managerAPI{mgr: mgr, attachments: att}
}

// NewManagerAPIWithAttachmentsAndArtifacts returns a SessionsAPI
// wired for the artifacts-storage cascade extension (FR-014).
// artStore + media + dataDir are optional — passing nil for any of
// them collapses to the attachments-only behaviour.
func NewManagerAPIWithAttachmentsAndArtifacts(
	mgr *session.Manager,
	att *attachments.Manager,
	artStore coreart.Store,
	media attachments.MediaStore,
	dataDir string,
) SessionsAPI {
	if mgr == nil {
		panic("rpc/sessions: NewManagerAPI: nil manager")
	}
	return &managerAPI{
		mgr:         mgr,
		attachments: att,
		artStore:    artStore,
		media:       media,
		dataDir:     dataDir,
	}
}

// WithTitleGenerator returns a copy of the managerAPI wired with the
// supplied TitleGenerator. Called by api.go during boot to attach the
// auto-title generator to the sessions view without altering the
// existing constructor signatures.
func WithTitleGeneratorOpt(api SessionsAPI, gen TitleGenerator) SessionsAPI {
	if m, ok := api.(*managerAPI); ok {
		m.titleGen = gen
	}
	return api
}

// List implements SessionsAPI.
func (a *managerAPI) List(ctx context.Context) ([]Session, error) {
	records, err := a.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(records))
	for _, r := range records {
		out = append(out, recordToView(r))
	}
	return out, nil
}

// Get implements SessionsAPI.
func (a *managerAPI) Get(ctx context.Context, id string) (Session, error) {
	r, err := a.mgr.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}
	return recordToView(r), nil
}

// Create implements SessionsAPI.
func (a *managerAPI) Create(ctx context.Context, name string) (Session, error) {
	r, err := a.mgr.Create(ctx, name)
	if err != nil {
		return Session{}, err
	}
	return recordToView(r), nil
}

// Rename implements SessionsAPI.
func (a *managerAPI) Rename(ctx context.Context, id, name string) error {
	return a.mgr.Rename(ctx, id, name)
}

// Delete implements SessionsAPI with the default cascade (delete
// artifacts alongside the session). Equivalent to
// DeleteWithOptions(ctx, id, DeleteOptions{}).
func (a *managerAPI) Delete(ctx context.Context, id string) error {
	return a.DeleteWithOptions(ctx, id, DeleteOptions{})
}

// DeleteWithOptions implements SessionsAPI's FR-014 cascade extension.
//
// Order of operations:
//  1. Resolve the session's artifact roster + content hashes so we
//     can refcount-sweep after the cascade.
//  2. If preserveArtifacts && session has no project → return
//     ErrSessionHasArtifacts. The caller must promote or accept the
//     delete-artifacts default.
//  3. If preserveArtifacts && promote → walk each artifact, route
//     through Store.UpdateScope("project", projectID).
//  4. RemoveScope on session-scope attachments (existing A8 cascade).
//  5. If deleteArtifacts (default), explicitly Delete each artifact
//     row first — post-migration 0304 the FK is ON DELETE SET NULL,
//     so artifact rows would otherwise survive their session as
//     orphans. Promoted artifacts (step 3) are skipped.
//  6. session.Manager.Delete — at this point any remaining artifact
//     rows have either been deleted (step 5) or promoted (step 3);
//     the SET NULL FK simply unhooks them from the deleted session.
//  7. Refcount-sweep every captured hash; remove the on-disk file
//     when the composite refcount drops to zero. Catches BOTH the
//     attachments-only orphans (existing behaviour) and the
//     artifacts orphans we just deleted in step 5.
func (a *managerAPI) DeleteWithOptions(ctx context.Context, id string, opts DeleteOptions) error {
	// 1. List artifacts BEFORE the cascade so we can refcount-sweep
	// the freed hashes after the session row is gone. Empty list
	// (or nil store) collapses to the pre-WP02 cascade.
	var artifactRows []coreart.Artifact
	if a.artStore != nil {
		rows, err := a.artStore.List(ctx, coreart.ArtifactFilter{SessionID: id})
		if err != nil {
			return fmt.Errorf("rpc/sessions: list artifacts: %w", err)
		}
		artifactRows = rows
	}

	// 2/3. Honor the preserve-then-promote contract.
	promoted := false
	if len(artifactRows) > 0 && !opts.DeleteArtifactsCascade() {
		// Resolve session's project membership.
		rec, err := a.mgr.Get(ctx, id)
		if err != nil {
			return err
		}
		hasProject := rec.ProjectID != nil && *rec.ProjectID != ""
		if !hasProject {
			return fmt.Errorf("%w: %d artifact(s)", ErrSessionHasArtifacts, len(artifactRows))
		}
		if !opts.PromoteArtifactsToProject {
			return fmt.Errorf("%w: %d artifact(s); set PromoteArtifactsToProject to keep them",
				ErrSessionHasArtifacts, len(artifactRows))
		}
		// Promote every artifact to project scope before the session
		// row is dropped. Post-migration 0304 the SET NULL FK leaves
		// the artifact alive with NULL session_id and the freshly
		// updated project_id, exactly the FR-014 promote semantics.
		for _, art := range artifactRows {
			if _, err := a.artStore.UpdateScope(ctx, art.ID, coreart.ScopeKindProject, *rec.ProjectID); err != nil {
				return fmt.Errorf("rpc/sessions: promote artifact %s: %w", art.ID, err)
			}
		}
		// Promoted artifacts survive — clear them from the sweep set
		// so the on-disk CAS file isn't reclaimed.
		artifactRows = nil
		promoted = true
	}

	// 4. Existing A8 cascade — drop session-scope attachments so the
	// attachments refcount source sees them missing during the sweep
	// in step 7.
	if a.attachments != nil {
		if _, err := a.attachments.RemoveScope(ctx, attachments.ScopeKindSession, id); err != nil {
			return err
		}
	}

	// 5. Default cascade: explicitly Delete each (non-promoted)
	// artifact row. Post-0304 the artifacts FK is SET NULL so the
	// session.Delete below would orphan rather than reap them.
	if !promoted && a.artStore != nil {
		for _, art := range artifactRows {
			if _, err := a.artStore.Delete(ctx, art.ID); err != nil {
				return fmt.Errorf("rpc/sessions: delete artifact %s: %w", art.ID, err)
			}
		}
	}

	// 6. Delete the session row. With every cascading artifact
	// already gone (step 5) or promoted (step 3), the FK SET NULL
	// only fires against any rows we missed — defensive harmless.
	if err := a.mgr.Delete(ctx, id); err != nil {
		return err
	}

	// 6. Refcount-sweep every captured artifact hash. Skip when the
	// MediaStore is unwired (test paths that don't share state).
	//
	// The non-self refcount check mirrors the artifacts view's Delete
	// (see rpc/views/artifacts/impl.go::nonSelfRefcountIsZero): the
	// composite RefcountFor includes media_artifacts rows themselves,
	// so we need to subtract the per-hash media-row count to learn
	// whether anything OTHER than the metadata still references the
	// file. When the answer is "nothing else", we drop the metadata
	// rows AND the on-disk file.
	if a.media != nil && a.dataDir != "" {
		seen := make(map[string]struct{}, len(artifactRows))
		for _, art := range artifactRows {
			if art.ContentHash == "" {
				continue
			}
			if _, ok := seen[art.ContentHash]; ok {
				continue
			}
			seen[art.ContentHash] = struct{}{}
			mediaRows, lerr := a.media.List(ctx, attachments.MediaFilter{ContentHash: art.ContentHash})
			if lerr != nil {
				return fmt.Errorf("rpc/sessions: list media rows: %w", lerr)
			}
			total, refErr := a.media.RefcountFor(ctx, art.ContentHash)
			if refErr != nil {
				return fmt.Errorf("rpc/sessions: refcount %s: %w", art.ContentHash, refErr)
			}
			if total > len(mediaRows) {
				// Something else still references the hash (another
				// attachment, or a sister artifact in a different
				// session). Leave the file in place.
				continue
			}
			for _, m := range mediaRows {
				if derr := a.media.Delete(ctx, m.ID); derr != nil {
					return fmt.Errorf("rpc/sessions: delete media row: %w", derr)
				}
			}
			path := filepath.Join(a.dataDir, "media", art.ContentHash)
			if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
				return fmt.Errorf("rpc/sessions: remove cas file: %w", rerr)
			}
		}
	}
	return nil
}

// Reorder implements SessionsAPI.
func (a *managerAPI) Reorder(ctx context.Context, ids []string) error {
	return a.mgr.Reorder(ctx, ids)
}

// StartStream implements SessionsAPI. Streaming is not yet wired by
// the harness; returns an empty subscription id so the frontend's
// subscribe/unsubscribe protocol does not error. The streaming
// mission replaces this implementation.
func (a *managerAPI) StartStream(_ context.Context, _ string) (string, error) {
	return "", nil
}

// StopStream implements SessionsAPI. Counterpart to StartStream;
// always succeeds.
func (a *managerAPI) StopStream(_ context.Context, _ string) error {
	return nil
}

// messageToView projects the durable session.Message into the
// rpc-layer wire shape. Tool-call args are coerced to a one-line
// summary; the full structured payload stays in the durable record.
//
// Compaction bookkeeping (WP07): CompactedIntoID / CompactedAt /
// ArchivedAt are surfaced on the wire as RFC3339Nano UTC strings (or
// the empty string for nil pointers) so the frontend can render the
// summary indicator + archived chip without a second round-trip.
func messageToView(m session.Message) Message {
	out := Message{
		ID:        m.ID,
		SessionID: m.SessionID,
		Role:      string(m.Role),
		Content:   m.Content,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:          tc.ID,
				Name:        tc.Name,
				ArgsSummary: "",
			})
		}
	}
	if m.CompactedIntoID != nil {
		out.CompactedIntoID = *m.CompactedIntoID
	}
	if m.CompactedAt != nil {
		out.CompactedAt = m.CompactedAt.UTC().Format(time.RFC3339Nano)
	}
	if m.ArchivedAt != nil {
		out.ArchivedAt = m.ArchivedAt.UTC().Format(time.RFC3339Nano)
	}
	// long-turn-resilience-01KR3PRS WP03: surface the partial-row
	// metadata so the frontend can render the Resume affordance + the
	// "(continued below)" caption.
	if m.StreamingFailedAt != nil {
		out.StreamingFailedAt = m.StreamingFailedAt.UTC().Format(time.RFC3339Nano)
	}
	out.StreamingFailureKind = m.StreamingFailureKind
	out.StreamingRecoverable = m.StreamingRecoverable
	out.ContinuationOf = m.ContinuationOf
	return out
}

// ListMessages implements SessionsAPI.
func (a *managerAPI) ListMessages(ctx context.Context, id string) ([]Message, error) {
	msgs, err := a.mgr.ListMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToView(m))
	}
	return out, nil
}

// ListMessagesActive implements SessionsAPI. Filters to rows where
// archived_at IS NULL via the manager's ListMessagesActive helper.
//
// SweptCount is currently always 0: by design the soft-archive sweep
// hard-deletes rows without leaving a tombstone, so a precise count
// requires either a per-session counter (deferred to WP09) or a
// recoverable "earliest sequence" marker we don't yet keep. The wire
// field ships as a stub so frontend code paths are exercised end-to-end
// and the placeholder UI becomes a one-line flip once tracking lands.
//
// TODO (WP09): track swept count via a session-level counter
// incremented inside compaction.RunSweep so the placeholder renders an
// accurate "N earlier turns no longer available" copy.
func (a *managerAPI) ListMessagesActive(ctx context.Context, id string) (ListMessagesResult, error) {
	msgs, err := a.mgr.ListMessagesActive(ctx, id)
	if err != nil {
		return ListMessagesResult{}, err
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToView(m))
	}
	return ListMessagesResult{Messages: out, SweptCount: 0}, nil
}

// ListMessagesAll implements SessionsAPI. Returns every message in the
// session, including soft-archived rows, ordered by sequence ASC. The
// frontend "Show full history" toggle hits this path. SweptCount is a
// placeholder zero today — see ListMessagesActive for the rationale.
func (a *managerAPI) ListMessagesAll(ctx context.Context, id string) (ListMessagesResult, error) {
	msgs, err := a.mgr.ListMessages(ctx, id)
	if err != nil {
		return ListMessagesResult{}, err
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToView(m))
	}
	return ListMessagesResult{Messages: out, SweptCount: 0}, nil
}

// AppendMessage implements SessionsAPI. Persists a single chat turn
// and returns the stored record (with an assigned id + sequence).
func (a *managerAPI) AppendMessage(ctx context.Context, id, role, content string) (Message, error) {
	stored, err := a.mgr.AppendMessage(ctx, id, session.Message{
		Role:    session.Role(role),
		Content: content,
	})
	if err != nil {
		return Message{}, err
	}
	return messageToView(stored), nil
}

// SendMessageWithBlocks implements SessionsAPI. The persisted row uses
// the polymorphic []ContentBlock shape (multimodal-io WP02 added the
// content_json column). Legacy callers stick with AppendMessage; new
// frontend code (WP04) hits this path whenever the user attaches an
// image or document.
//
// The LLM stream itself is NOT triggered here — the chat surface calls
// LLM_StartStream after the user turn lands, mirroring the pre-WP03
// AppendMessage flow.
func (a *managerAPI) SendMessageWithBlocks(ctx context.Context, id string, contentBlocks []ContentBlock) (Message, error) {
	if len(contentBlocks) == 0 {
		return Message{}, ErrEmptyContentBlocks
	}
	stored, err := a.mgr.AppendMessage(ctx, id, session.Message{
		Role:          session.RoleUser,
		ContentBlocks: contentBlocks,
	})
	if err != nil {
		return Message{}, err
	}
	return messageToView(stored), nil
}

// SaveDraft implements SessionsAPI.
func (a *managerAPI) SaveDraft(ctx context.Context, id, draft string) error {
	return a.mgr.SaveDraft(ctx, id, draft)
}

// LoadDraft implements SessionsAPI. The Manager surfaces the draft as
// part of the Record; we round-trip through Get to keep the wire
// shape single-purpose.
func (a *managerAPI) LoadDraft(ctx context.Context, id string) (string, error) {
	r, err := a.mgr.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return r.Draft, nil
}

// SetSystemPrompt implements SessionsAPI.
//
// Post-WP03 this is a thin wrapper over the attachments table — the
// canonical home for session starting context. The legacy
// session.system_prompt column write stays for the one-release compat
// buffer so Mission A's existing code paths (and any old DB rows that
// migration 0301 already seeded) keep working without surprise.
//
// Behaviour:
//   - content == "" with an existing position-0 inline session-scope
//     attachment removes the attachment row.
//   - content != "" upserts a position-0 inline attachment whose
//     content_source is "inline:<sha256(content)>". Any prior
//     position-0 inline attachment for the session is removed first so
//     a re-set always replaces.
//
// TODO: remove the legacy a.mgr.SetSystemPrompt call next mission
// post-WP04 once the frontend reads attachments directly.
func (a *managerAPI) SetSystemPrompt(ctx context.Context, id, content, kind string) error {
	if a.attachments != nil {
		if err := a.upsertSessionAttachment(ctx, id, content, kind); err != nil {
			return err
		}
	}
	return a.mgr.SetSystemPrompt(ctx, id, content, kind)
}

// upsertSessionAttachment removes any existing position-0 inline
// session-scope attachment for sessionID and inserts a new one when
// content is non-empty. Library-source attachments at position 0 are
// preserved — only inline snapshots are owned by the SetSystemPrompt
// shim.
func (a *managerAPI) upsertSessionAttachment(ctx context.Context, sessionID, content, kind string) error {
	existing, err := a.attachments.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession,
		ScopeID:   sessionID,
	})
	if err != nil {
		return err
	}
	for _, att := range existing {
		if att.Position != 0 {
			continue
		}
		if !startsWithInline(att.ContentSource) {
			continue
		}
		if err := a.attachments.Remove(ctx, att.ID); err != nil {
			return err
		}
	}
	if content == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(content))
	source := "inline:" + hex.EncodeToString(hash[:])
	attKind := attachments.KindSystem
	if kind == session.ContextKindUserSeed {
		attKind = attachments.KindUser
	}
	_, err = a.attachments.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       sessionID,
		ContentSource: source,
		Content:       content,
		Kind:          attKind,
		Position:      0,
	})
	return err
}

func startsWithInline(src string) bool {
	const prefix = "inline:"
	return len(src) >= len(prefix) && src[:len(prefix)] == prefix
}

// MoveToProject implements SessionsAPI. An empty projectID detaches
// the session (loose).
func (a *managerAPI) MoveToProject(ctx context.Context, id, projectID string) error {
	var p *string
	if projectID != "" {
		v := projectID
		p = &v
	}
	return a.mgr.MoveToProject(ctx, id, p)
}

// SuggestTitle implements SessionsAPI. It re-generates a title from the
// session's current active messages and writes it via RequestRetitle
// (bypasses the auto_titled guard intentionally — this is the manual
// "Suggest new title" path). Returns the generated title string.
func (a *managerAPI) SuggestTitle(ctx context.Context, id string) (string, error) {
	if a.titleGen == nil {
		return "", ErrTitleGeneratorNotConfigured
	}

	msgs, err := a.mgr.ListMessagesActive(ctx, id)
	if err != nil {
		return "", fmt.Errorf("rpc/sessions: list messages: %w", err)
	}

	// Cap to the most-recent 10 messages.
	if len(msgs) > 10 {
		msgs = msgs[len(msgs)-10:]
	}

	transcript := make(autotitle.Transcript, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == session.RoleUser || m.Role == session.RoleAssistant {
			transcript = append(transcript, autotitle.TranscriptMessage{
				Role: string(m.Role),
				Text: m.Content,
			})
		}
	}

	title, err := a.titleGen.GenerateTitle(ctx, transcript)
	if err != nil {
		return "", fmt.Errorf("rpc/sessions: generate title: %w", err)
	}

	if err := a.mgr.RequestRetitle(ctx, id, title); err != nil {
		return "", fmt.Errorf("rpc/sessions: write title: %w", err)
	}
	return title, nil
}

// ClearTitle implements SessionsAPI. Resets the session name to "" and
// auto_titled=0, re-enabling future auto-title attempts.
func (a *managerAPI) ClearTitle(ctx context.Context, id string) error {
	return a.mgr.ClearTitle(ctx, id)
}

// WithUsageManager wires a usage.Manager into the sessions view so
// GetUsage can return real aggregates (token-cost-telemetry WP03).
// Safe to call at boot before any request arrives.
func WithUsageManager(api SessionsAPI, mgr usage.Manager) SessionsAPI {
	if m, ok := api.(*managerAPI); ok {
		m.usageMgr = mgr
	}
	return api
}

// GetUsage implements SessionsAPI. Returns the per-session cumulative
// token + cost aggregate from session_messages columns added in
// migration 0314. Falls back to an zeroed Aggregate when the usage
// manager is not wired.
func (a *managerAPI) GetUsage(ctx context.Context, id string) (SessionUsage, error) {
	pricingDate := pricing.LastUpdatedString()
	if a.usageMgr == nil {
		return SessionUsage{CostSource: "unknown", PricingDataDate: pricingDate}, nil
	}
	agg, err := a.usageMgr.GetSession(ctx, id)
	if err != nil {
		return SessionUsage{}, fmt.Errorf("rpc/sessions: GetUsage: %w", err)
	}
	return SessionUsage{
		PromptTokens:     agg.PromptTokens,
		CompletionTokens: agg.CompletionTokens,
		TotalTokens:      agg.TotalTokens,
		CostUSD:          agg.CostUSD,
		CostSource:       agg.CostSource,
		MessageCount:     agg.MessageCount,
		PricingDataDate:  pricingDate,
	}, nil
}

// ResumeMessage implements SessionsAPI. Validates the partial row's
// shape, ensures idempotency by scanning for an existing continuation
// row, and delegates the actual stream open to the wired ResumeStarter.
//
// long-turn-resilience-01KR3PRS WP03.
func (a *managerAPI) ResumeMessage(ctx context.Context, sessionID, messageID string) (ResumeMessageResult, error) {
	if a.resumeStarter == nil {
		return ResumeMessageResult{}, ErrResumeNotConfigured
	}
	partial, err := a.mgr.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return ResumeMessageResult{}, fmt.Errorf("rpc/sessions: load partial: %w", err)
	}
	if partial.StreamingFailedAt == nil {
		return ResumeMessageResult{}, ErrResumeNotPartial
	}
	if !partial.StreamingRecoverable {
		return ResumeMessageResult{}, ErrResumeNotRecoverable
	}
	// Idempotency: walk the session's messages and refuse to open a
	// second stream when a continuation row already points at this id.
	// O(n) scan is fine; chat sessions cap out in the low thousands and
	// this is a click-driven path. Only walks active rows so a soft-
	// archived continuation doesn't keep the original perpetually
	// "already continued" — re-resume after compaction is intentional.
	allMsgs, lerr := a.mgr.ListMessages(ctx, sessionID)
	if lerr != nil {
		return ResumeMessageResult{}, fmt.Errorf("rpc/sessions: list messages: %w", lerr)
	}
	for _, m := range allMsgs {
		if m.ContinuationOf == messageID && m.ArchivedAt == nil {
			return ResumeMessageResult{}, ErrResumeAlreadyContinued
		}
	}

	subID, serr := a.resumeStarter.StartResume(ctx, sessionID, messageID, "", "")
	if serr != nil {
		return ResumeMessageResult{}, fmt.Errorf("rpc/sessions: start resume: %w", serr)
	}
	return ResumeMessageResult{
		SubscriptionID:    subID,
		OriginalMessageID: messageID,
	}, nil
}
