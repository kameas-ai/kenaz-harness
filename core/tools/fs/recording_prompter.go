package fs

// RecordingPrompter — the durable, surfaceable, re-runnable record of a
// denied permission request (model-scheduled-jobs-01PMSJ01 WP06, FR-004,
// owner decision 2).
//
// Before this file, a denied filesystem write was silent: NoOpPrompter
// (now CedarPrompter — E-001, owner ruling B-4) returns PromptDeny and
// nothing else happens. That is correct for SAFETY (unattended runs must
// fail-safe deny) and wrong for HONESTY: the user has no way to find out
// their scheduled job was blocked, grant a durable permit, and re-run it.
//
// RecordingPrompter is a decorator, not a replacement: it wraps the real
// interactive Prompter (production: &CedarPrompter{Registry: ...}) and
// only ever OBSERVES the decision that Prompter already made. It changes
// no behaviour — the denial already happens — it only stops the denial
// from being invisible. This is what makes it safe to land ahead of any
// product ruling on whether kenaz__write_file should ALSO prompt (E-001
// covers that separately; this WP is orthogonal to it).
//
// origin is not read from the wrapped Prompter's decision — it is
// resolved from ctx via OriginLookup, keyed on the session id the tool
// dispatch already attaches (core/toolloop.WithSessionID). Every denial
// is recorded, interactive or scheduled (spec.md §5.5: "origin is a
// column, not a filter" — if only scheduled runs wrote rows, the WP07
// surfacing UI would read empty for every user who never scheduled a
// job, and a real defect there would go unnoticed).
import (
	"context"
	"log/slog"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Origin values for BlockedRequest.Origin (spec.md §5.5's table).
const (
	OriginScheduledChatRun = "scheduled_chat_run"
	OriginInteractive      = "interactive"
)

// BlockedRequest is what RecordingPrompter asks its Sink to persist.
type BlockedRequest struct {
	// Origin is OriginScheduledChatRun or OriginInteractive.
	Origin string
	// OriginID is the scheduled_chat_runs.id when Origin is
	// OriginScheduledChatRun, empty otherwise.
	OriginID string
	// SessionID is the session the denied call happened in.
	SessionID string
	// Family is always "fs" for a RecordingPrompter denial — the column
	// exists on the table (spec.md §5.5) so a future prompter family
	// (bash, cred, tool) can write into the same table without a schema
	// change.
	Family string
	// Action is cedar's "read_filesystem" or "write_filesystem".
	Action string
	// Resource is the canonical path the request targeted.
	Resource string
	// Reason is a short, human-readable explanation (never tool
	// arguments or file contents — spec.md's audit privacy invariant
	// applies here too).
	Reason string
}

// BlockedRequestSink is the persistence seam RecordingPrompter writes
// through. The production implementation (core/rpc) backs
// blocked_permission_requests (migration sessions/0338) and also emits
// an audit record — RecordingPrompter itself knows nothing about SQL or
// the audit log, keeping this package free of an rpc/storage dependency.
type BlockedRequestSink interface {
	RecordBlocked(ctx context.Context, req BlockedRequest) error
}

// OriginResolver resolves a session id to the (origin, originID) pair a
// blocked request should be attributed to. Returns
// (OriginInteractive, "") for a session RecordingPrompter does not
// recognise as a scheduled run — the safe, honest default: absence of
// evidence that a session is a scheduled run is not evidence it isn't,
// but attributing it to "interactive" rather than fabricating a
// scheduled-run id it cannot prove is the fail-safe direction (mirrors
// decodeToolAllowlist's "malformed reads as none, not as unrestricted"
// convention elsewhere in this mission).
type OriginResolver func(sessionID string) (origin, originID string)

// RecordingPrompter wraps Inner and records every PromptDeny outcome via
// Sink before returning it unchanged. It never changes what Inner
// decided — only whether that decision leaves a durable trace.
type RecordingPrompter struct {
	// Inner is the real interactive Prompter (production:
	// &CedarPrompter{Registry: ...}). Required — a nil Inner always
	// denies (matches NoOpPrompter's own contract) but records nothing,
	// since there is nothing to construct a meaningful BlockedRequest
	// from without knowing which op/path even reached this call.
	Inner Prompter
	// Sink persists the record. nil disables recording (Prompt still
	// delegates to Inner and returns its decision) — the same
	// nil-tolerant shape as PolicyDir's "recording is optional, denial is
	// not" contract on Gate itself.
	Sink BlockedRequestSink
	// Resolve attributes a denial to its origin. nil always resolves to
	// OriginInteractive.
	Resolve OriginResolver
}

// Prompt implements Prompter.
func (p *RecordingPrompter) Prompt(ctx context.Context, s PromptSurface) (PromptResponse, error) {
	if p == nil || p.Inner == nil {
		return PromptDeny, nil
	}
	resp, err := p.Inner.Prompt(ctx, s)
	if resp != PromptDeny {
		return resp, err
	}
	if p.Sink == nil {
		return resp, err
	}

	sessionID := toolloop.SessionIDFromContext(ctx)
	origin, originID := OriginInteractive, ""
	if p.Resolve != nil {
		if o, oid := p.Resolve(sessionID); o != "" {
			origin, originID = o, oid
		}
	}

	action := "read_filesystem"
	if s.Op == OpWrite {
		action = "write_filesystem"
	}
	reason := "denied by policy"
	if err != nil {
		reason = "prompt error: " + err.Error()
	}

	if rerr := p.Sink.RecordBlocked(ctx, BlockedRequest{
		Origin:    origin,
		OriginID:  originID,
		SessionID: sessionID,
		Family:    "fs",
		Action:    action,
		Resource:  s.CanonicalPath,
		Reason:    reason,
	}); rerr != nil {
		// Best-effort: a recording failure must not change the (already
		// safe) deny outcome — see this type's own doc, "it only stops
		// the denial being silent," not "it makes the denial contingent
		// on a database write succeeding."
		slog.WarnContext(ctx, "fs.recording_prompter.record_failed",
			"origin", origin, "action", action, "error", rerr.Error())
	}

	return resp, err
}

var _ Prompter = (*RecordingPrompter)(nil)
