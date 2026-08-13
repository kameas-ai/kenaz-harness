package event

import (
	"context"
	"encoding/json"
	"time"
)

// ULID is a 128-bit, lexicographically sortable, monotonic identifier
// rendered as the canonical 26-character Crockford-base32 string. Its
// zero value is the all-zero ULID ("00000000000000000000000000").
type ULID string

// IsZero reports whether u is the zero ULID.
func (u ULID) IsZero() bool { return u == "" || u == "00000000000000000000000000" }

// String returns u as a canonical Crockford-base32 string.
func (u ULID) String() string { return string(u) }

// EmitterID is a namespaced source identifier such as "llm/anthropic",
// "mcp/client", or "session/". It is validated against the allowlist
// documented in plan §5.6 (FR-017).
type EmitterID string

// String returns the raw identifier.
func (e EmitterID) String() string { return string(e) }

// Kind is a stable, namespaced event-kind id (e.g.
// "llm.request.started", "mcp.tool.invoked"). Forward-compat: unknown
// kinds are preserved verbatim by older readers (NFR-006).
type Kind string

// String returns the raw kind.
func (k Kind) String() string { return string(k) }

// RedactionSummary records what the redaction pipeline did to a payload
// before persistence. Even an empty pipeline run records a summary
// (NoOp = true) so audit can distinguish "no patterns matched" from
// "redaction skipped" (the latter is impossible by construction; C-004).
type RedactionSummary struct {
	// MatchersFired lists the matcher ids that fired during this Apply.
	// Order matches the order they fired.
	MatchersFired []string `json:"matchers_fired,omitempty"`
	// FieldPathsRedacted lists JSON paths whose values were replaced.
	FieldPathsRedacted []string `json:"field_paths_redacted,omitempty"`
	// NoOp is true if the pipeline executed but no matcher fired and no
	// field path was redacted. Distinct from a skipped pipeline.
	NoOp bool `json:"no_op,omitempty"`
	// SaltFingerprint identifies the HMAC salt under which deterministic
	// placeholders in this entry were generated. Used for forensic
	// correlation across same-salt windows (R6).
	SaltFingerprint string `json:"salt_fingerprint,omitempty"`
}

// Event is one immutable record in the log. Fields mirror spec
// "Key Entities" and the events table in plan §5.1.
//
// Events are produced only by Emitter.Append. The fields are read-only
// to consumers; the package exposes no method that mutates an Event.
type Event struct {
	EventID          ULID             `json:"event_id"`
	SessionID        *ULID            `json:"session_id,omitempty"`
	EmitterID        EmitterID        `json:"emitter_id"`
	Kind             Kind             `json:"kind"`
	EmittedAt        time.Time        `json:"emitted_at"`
	Payload          json.RawMessage  `json:"payload"`
	PayloadHash      [32]byte         `json:"payload_hash"`
	PrevHash         [32]byte         `json:"prev_hash"`
	RedactionSummary RedactionSummary `json:"redaction_summary"`
}

// AppendInput is the caller-supplied side of Emitter.Append. The
// payload is plaintext-as-built; redaction runs inside Append.
type AppendInput struct {
	SessionID *ULID
	EmitterID EmitterID
	Kind      Kind
	// Payload is anything json.Marshal can encode. The pipeline
	// canonicalizes and redacts before persistence; callers MUST NOT
	// pre-redact.
	Payload any
	// EmittedAt overrides the emitter's monotonic clock floor.
	// Zero means "use the emitter's clock". Provided for tests and
	// for replay tooling; production callers should leave it zero.
	EmittedAt time.Time
}

// Emitter is the single write surface for the event log. It exposes
// Append and nothing else. There is no Update, Delete, or Patch (FR-003,
// C-002). The pipeline is non-bypassable inside Append (C-004).
type Emitter interface {
	// Append validates the kind and emitter, runs the redaction
	// pipeline, computes payload_hash, links prev_hash, and persists
	// under a single transaction. Returns the assigned EventID and
	// the materialized Event on success.
	//
	// Errors are typed; see errors.go.
	Append(ctx context.Context, in AppendInput) (Event, error)
}

// ReadOpts is shared by all Reader query methods.
type ReadOpts struct {
	// Limit caps the number of events returned. Zero = default cap.
	Limit int
	// After is an exclusive lower bound on event_id. Zero = no bound.
	After ULID
	// Reverse iterates from newest to oldest.
	Reverse bool
}

// Cursor is a forward-only stream of events. It is single-use; once
// closed it cannot be re-opened. A cursor opened at event id E always
// replays from E even if later events arrive (FR-009 byte-stability
// foundation).
type Cursor interface {
	// Next returns the next Event, or io.EOF when the cursor is
	// exhausted. Honors context cancellation.
	Next(ctx context.Context) (Event, error)
	// Close releases cursor resources.
	Close() error
}

// ContentQuery is the input to Reader.Search. The grammar is the FTS5
// MATCH syntax; surfaces FTS5 errors as ErrInvalidQuery.
type ContentQuery struct {
	Query string
	// SessionID restricts the search to a single session if set.
	SessionID *ULID
	// Kinds restricts to one or more kinds.
	Kinds []Kind
}

// Reader covers FR-008 query patterns. Reader is the only read seam
// exposed outside core/event/ (DIRECTIVE_001).
type Reader interface {
	BySession(ctx context.Context, sid ULID, opts ReadOpts) (Cursor, error)
	ByKind(ctx context.Context, kind Kind, opts ReadOpts) (Cursor, error)
	ByEmitter(ctx context.Context, eid EmitterID, opts ReadOpts) (Cursor, error)
	ByTimeRange(ctx context.Context, from, to time.Time, opts ReadOpts) (Cursor, error)
	Search(ctx context.Context, q ContentQuery, opts ReadOpts) (Cursor, error)
	Get(ctx context.Context, id ULID) (Event, error)
}

// Report is the output of a chain verification.
type Report struct {
	// OK is true if every entry in the verified range passed.
	OK bool
	// VerifiedFrom and VerifiedTo bracket the contiguous ok span.
	VerifiedFrom ULID
	VerifiedTo   ULID
	// FirstTamperID identifies the first event whose recomputed
	// payload_hash or prev_hash linkage failed. Zero if none.
	FirstTamperID ULID
	// TruncationPoint identifies the cached chain head id that is
	// missing from the events table (legitimate truncation, not
	// tamper). Zero if none.
	TruncationPoint ULID
	// SessionsChecked is the number of distinct session chains walked.
	SessionsChecked int
	// EntriesChecked is the total number of events read.
	EntriesChecked int
	// Notes is a human-readable diagnostic summary.
	Notes string
}

// Verifier walks per-session hash chains and reports tamper or
// truncation points cleanly without aborting (US 6 + edge case).
type Verifier interface {
	VerifySession(ctx context.Context, sid ULID) (Report, error)
	VerifyAll(ctx context.Context) (Report, error)
}

// ReplayMode selects the visibility regime under redaction-supersession
// (spec OQ-3).
type ReplayMode int

const (
	// ReplayCurrentVisibility is the default: visibility reflects
	// any redaction-supersession events that have been appended
	// since the original write.
	ReplayCurrentVisibility ReplayMode = iota
	// ReplayRaw exposes the original recorded payload bytes verbatim.
	//
	// UNIMPLEMENTED, and the whole Replayer surface with it: nothing in
	// the tree implements Replayer, and core/event/replay is a doc.go
	// with no code. The design intent is that a Raw cursor requires
	// operator authorization in the context and that opening one itself
	// emits an event-log.replay.raw-opened self-event. Whoever builds
	// the Replayer builds BOTH of those in the same change — 01PMGX01
	// WP17 deleted the context-marker helpers that shipped ahead of it,
	// because an authorization primitive with no checker reads as a
	// guarantee and is not one (see helpers.go for the full note).
	ReplayRaw
)

// ReplayOpts is the input to Replayer.Open.
type ReplayOpts struct {
	Mode ReplayMode
	// FromEvent restricts replay to events at or after this id.
	FromEvent ULID
	// ToEvent restricts replay to events at or before this id.
	ToEvent ULID
}

// ReplayCursor is a deterministic, byte-stable iterator over a session.
type ReplayCursor interface {
	Next(ctx context.Context) (Event, error)
	Close() error
}

// Replayer reconstructs a session sequence byte-identically (FR-009,
// NFR-004).
type Replayer interface {
	Open(ctx context.Context, sid ULID, opts ReplayOpts) (ReplayCursor, error)
}

// Brancher creates a child session whose initial state matches the
// parent at the chosen event id (FR-010).
type Brancher interface {
	Branch(ctx context.Context, parent ULID, atEvent ULID) (ULID, error)
}

// PolicyKind enumerates retention policies. v1 honors only KeepAll.
type PolicyKind string

const (
	PolicyKeepAll     PolicyKind = "keep_all"
	PolicyKeepNDays   PolicyKind = "keep_n_days"
	PolicyKeepBudget  PolicyKind = "size_budget"
)

// Policy is a retention configuration.
type Policy struct {
	Kind         PolicyKind
	Days         int   // for keep_n_days
	BudgetBytes  int64 // for size_budget
	WarnAtBytes  int64 // for the unset-policy size warning (FR-013).
}

// Selector identifies events for archive / truncate operations.
type Selector struct {
	SessionID *ULID
	BeforeID  ULID
	BeforeAt  time.Time
}

// Archive is a destination for an archive operation (file path,
// bundle path, etc.). v1.x detail; defined here so the public API
// surface stays stable.
type Archive struct {
	Path string
}

// ArchiveRef references an archived dataset.
type ArchiveRef struct {
	Path     string
	Manifest string
	From     ULID
	To       ULID
}

// RetentionReport is the summary of an Apply.
type RetentionReport struct {
	Applied        Policy
	EventsArchived int
	EventsRemoved  int
	ArchiveRef     ArchiveRef
	Notes          string
}

// TruncateReport summarizes a Truncate.
type TruncateReport struct {
	EventsRemoved   int
	TruncationPoint ULID
	Notes           string
}

// Retention covers FR-013 / FR-014 / FR-015. v1 honors keep_all only;
// the surface is stable so consumers don't churn when v1.x ships.
type Retention interface {
	Apply(ctx context.Context, policy Policy) (RetentionReport, error)
	Archive(ctx context.Context, sel Selector, dest Archive) (ArchiveRef, error)
	Truncate(ctx context.Context, sel Selector) (TruncateReport, error)
}
