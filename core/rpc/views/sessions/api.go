// Package sessions defines the SessionsAPI view-scoped accessor on the
// HarnessAPI surface. Frontend mission delivers the interface shape; the
// concrete implementation is wired by the sessions feature mission.
package sessions

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// AutonomyKnobValues is the wire shape for a ResolvedKnobs payload.
// Concrete typed fields for every knob plus the per-knob source trace.
// (autonomy-dial-01KR3M2A WP03)
type AutonomyKnobValues struct {
	MaxIterations            int               `json:"maxIterations"`
	AskOnAmbiguity           string            `json:"askOnAmbiguity"`
	AutoApproveFamilies      []string          `json:"autoApproveFamilies"`
	TokenCeilingPerTurn      int               `json:"tokenCeilingPerTurn"`
	RecapStyle               string            `json:"recapStyle"`
	ContinueOnError          string            `json:"continueOnError"`
	DestructiveActionPosture string            `json:"destructiveActionPosture"`
	SourceTrace              map[string]string `json:"sourceTrace"`
	// Tier is the effective tier label resolved from the highest-priority
	// layer that contributed a Level (session > project > global > default).
	Tier string `json:"tier"`
}

// ResolvedAutonomy is the full ResolveAutonomy RPC payload: the
// resolved knobs plus the three input layers so the panels can render
// per-layer override badges without a second round-trip.
type ResolvedAutonomy struct {
	Resolved AutonomyKnobValues `json:"resolved"`
	Global   autonomy.Layer     `json:"global"`
	Project  autonomy.Layer     `json:"project"`
	Session  autonomy.Layer     `json:"session"`
}

// ContentBlock mirrors core/llm.ContentBlock on the rpc wire so the
// frontend (multimodal-io WP04) can hand assembled image / document
// blocks to SendMessageWithBlocks without spelling out the connector
// shape itself. Field names match corellm.ContentBlock exactly.
type ContentBlock = llm.ContentBlock

// Session is the lightweight session metadata the rail consumes.
type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	// SystemPrompt + ContextKind surface the per-session starting
	// context (Mission A). Empty SystemPrompt + ContextKind="system"
	// is the default for sessions created before the feature landed.
	SystemPrompt string `json:"systemPrompt"`
	ContextKind  string `json:"contextKind"`
	// ProjectID is the session's project membership; empty string for
	// loose sessions. Mirrors session.Record.ProjectID.
	ProjectID string `json:"projectId,omitempty"`
	// AutoTitled is true when the auto-titling engine has written a
	// title, or when the user has manually renamed the session (locking
	// out further auto-titling). Mirrors session.Record.AutoTitled.
	// Populated by migration 0311 (session-auto-titling-01KQ8TDS WP01).
	AutoTitled bool `json:"autoTitled"`
	// Kind tags the session for capability gating. Empty / absent
	// values are treated as "chat". Populated by migration 0318
	// (harness-self-mcp-onboarding-01KQ8TDU WP03).
	Kind string `json:"kind,omitempty"`

	// Branch linkage fields (branching-ux-polish-01KQ8TD7 WP02).
	// These are omitted for top-level sessions; non-empty only for
	// branch sessions. They are projected from the branches table via
	// a join — the sessions table itself has no parent column.

	// ParentSessionId is the id of the parent session this session
	// branched from. Empty for root sessions.
	ParentSessionId string `json:"parentSessionId,omitempty"`
	// ParentMessageId is the message anchor used by the breadcrumb.
	// Empty for branches created before migration 0322.
	ParentMessageId string `json:"parentMessageId,omitempty"`
	// BranchTitle is the display-name override for the branch. Empty
	// when no explicit title was set.
	BranchTitle string `json:"branchTitle,omitempty"`
	// BranchDepth is 0 for top-level sessions; pre-computed by iterative
	// ancestor walks capped at 32.
	BranchDepth int `json:"branchDepth,omitempty"`
}

// ToolCall mirrors the frontend ToolCall shape for tool-use rendering.
type ToolCall struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ArgsSummary string `json:"argsSummary"`
	Latency     string `json:"latency,omitempty"`
	// UsedSecrets is true when the dispatch resolved at least one
	// @secret: reference. Never carries plaintext — provenance only
	// (model-secret-references-01KW7M5A WP14).
	UsedSecrets bool `json:"usedSecrets,omitempty"`
}

// Message is a single chat-message entry. NEVER carries credential fields.
type Message struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionId"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	CreatedAt string     `json:"createdAt"`
	Streaming bool       `json:"streaming,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// CompactedIntoID is the id of the synthetic summary row that
	// folded this message in. Empty (omitted) on rows the compaction
	// engine never touched. Frontend uses this to render an "archived →
	// summary" jump chip on archived rows (compaction-strategy-ui WP07).
	CompactedIntoID string `json:"compactedIntoId,omitempty"`
	// CompactedAt is the RFC3339Nano UTC moment the compaction engine
	// wrote the summary row that replaces this message. Empty on rows
	// the engine never touched. On the synthetic summary row itself,
	// CompactedAt is non-empty and CompactedIntoID is empty.
	CompactedAt string `json:"compactedAt,omitempty"`
	// ArchivedAt is the RFC3339Nano UTC moment this row was flagged as
	// archived (folded into a summary). Empty on live rows; non-empty
	// rows are excluded from ListMessagesActive.
	ArchivedAt string `json:"archivedAt,omitempty"`

	// StreamingFailedAt is the RFC3339Nano UTC moment the chat runner
	// persisted this row as a partial-output drop
	// (long-turn-resilience-01KR3PRS WP03). Empty on every healthy row;
	// non-empty rows are partial assistant turns the user can resume
	// when StreamingRecoverable is true.
	StreamingFailedAt string `json:"streamingFailedAt,omitempty"`
	// StreamingFailureKind is the wire-shape mirror of
	// session.Message.StreamingFailureKind. One of "transient" | "auth"
	// | "unknown"; empty on healthy rows.
	StreamingFailureKind string `json:"streamingFailureKind,omitempty"`
	// StreamingRecoverable indicates whether the partial row is safe to
	// resume via Sessions_ResumeMessage. False when tool_use already
	// ran before the drop — the user must re-issue the request instead.
	StreamingRecoverable bool `json:"streamingRecoverable,omitempty"`
	// ContinuationOf is the id of the partial assistant row this row
	// continues. Empty on every original assistant row; non-empty only
	// on the continuation row written by Sessions_ResumeMessage.
	ContinuationOf string `json:"continuationOf,omitempty"`

	// Per-message token usage (per-message-token-meter-01KR3PQR).
	// Present only on assistant rows for which the token-cost-telemetry
	// pipeline captured usage (session_messages columns: prompt_tokens,
	// completion_tokens, cost_usd, cost_source). Absent on user / system
	// / tool rows and on rows that pre-date the migration.
	PromptTokens     *int     `json:"promptTokens,omitempty"`
	CompletionTokens *int     `json:"completionTokens,omitempty"`
	CostUSD          *float64 `json:"costUsd,omitempty"`
	// MessageCostSource mirrors the token-cost-telemetry taxonomy:
	// "provider" | "derived" | "mixed" | "unknown". Empty on rows
	// with no usage data.
	MessageCostSource string `json:"messageCostSource,omitempty"`
}

// ResumeMessageResult is the wire shape returned by Sessions_ResumeMessage.
// SubscriptionID matches the existing LLM stream-subscription contract so
// the frontend can wire the resume stream into the same chunk + closed
// topics it already drains for fresh turns.
//
// long-turn-resilience-01KR3PRS WP03.
type ResumeMessageResult struct {
	// SubscriptionID is the chat-stream subscription id the resume
	// invocation opened. Empty when the resume API short-circuited
	// before opening a stream (the message was non-recoverable; the
	// caller receives ErrResumeNotRecoverable instead).
	SubscriptionID string `json:"subscriptionId"`
	// OriginalMessageID is the id of the partial assistant row the
	// resume continued. Round-tripped so the frontend can correlate
	// the subscription id with the bubble that triggered it.
	OriginalMessageID string `json:"originalMessageId"`
}

// ListMessagesResult is the wire-shape envelope for the WP07
// ListMessagesActive / ListMessagesAll surface. Carries the message
// list plus a SweptCount field describing rows that were once archived
// but have since been hard-deleted by the soft-archive sweep.
//
// SweptCount note: the sweep deletes rows outright (no tombstone), so
// computing the gap precisely requires either a per-session counter
// (deferred to WP09) or a recoverable "earliest sequence" marker. For
// WP07 the field ships as zero — the frontend guards the placeholder
// behind SweptCount > 0, so the UI path is wired but only renders once
// the WP09 tracking lands. See plan §2.8.
type ListMessagesResult struct {
	Messages   []Message `json:"messages"`
	SweptCount int       `json:"sweptCount"`
}

// DeleteOptions configures the artifacts-storage cascade extension
// (FR-014). The zero value is the default behaviour: artifacts get
// deleted alongside the session, no promotion to project scope.
type DeleteOptions struct {
	// DeleteArtifacts controls whether the session's artifacts are
	// removed during the cascade. Default true (zero-value Preserve
	// inverts to Delete=true — see DeleteArtifactsCascade). Setting
	// PreserveArtifacts=true forces the caller to either promote the
	// artifacts to the session's project (PromoteArtifactsToProject)
	// or accept the ErrSessionHasArtifacts error.
	PreserveArtifacts bool `json:"preserveArtifacts,omitempty"`
	// PromoteArtifactsToProject is meaningful only when
	// PreserveArtifacts is true AND the session has a project. When
	// set, every artifact is moved to project scope before the
	// session row is deleted.
	PromoteArtifactsToProject bool `json:"promoteArtifactsToProject,omitempty"`
}

// DeleteArtifactsCascade returns the user-facing flag that drives
// session-delete cascade. Inverts the persisted PreserveArtifacts so
// the zero value means "delete artifacts alongside the session" (the
// FR-014 default).
func (o DeleteOptions) DeleteArtifactsCascade() bool { return !o.PreserveArtifacts }

// SessionUsage is the per-session cumulative token + cost aggregate
// returned by GetUsage (token-cost-telemetry-01KQ8TD7 WP03).
type SessionUsage struct {
	// PromptTokens is the sum of all input tokens for the session.
	PromptTokens int `json:"promptTokens"`
	// CompletionTokens is the sum of all output tokens.
	CompletionTokens int `json:"completionTokens"`
	// TotalTokens is PromptTokens + CompletionTokens.
	TotalTokens int `json:"totalTokens"`
	// CostUSD is the summed cost in USD. 0 when unknown.
	CostUSD float64 `json:"costUsd"`
	// CostSource is one of "provider", "derived", "mixed", "unknown".
	CostSource string `json:"costSource"`
	// MessageCount is the number of assistant turns with usage data.
	MessageCount int `json:"messageCount"`
	// PricingDataDate is the last_updated date of the pricing table
	// ("YYYY-MM-DD") so the UI tooltip can surface data age.
	PricingDataDate string `json:"pricingDataDate"`
}

// SessionsAPI is the view-scoped accessor for session CRUD + streams.
// Implementations MUST be safe for concurrent use.
type SessionsAPI interface {
	List(ctx context.Context) ([]Session, error)
	Get(ctx context.Context, id string) (Session, error)
	Create(ctx context.Context, name string) (Session, error)
	Rename(ctx context.Context, id, name string) error
	Delete(ctx context.Context, id string) error
	// DeleteWithOptions is the FR-014 variant: opts drive the
	// artifacts cascade (delete vs preserve, promote-to-project on
	// preserve). The bare Delete keeps the pre-WP02 contract for
	// callers that don't yet thread options.
	DeleteWithOptions(ctx context.Context, id string, opts DeleteOptions) error
	Reorder(ctx context.Context, ids []string) error
	StartStream(ctx context.Context, id string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error

	// Chat-message surface (frontend-foundations chat-ui mission).
	ListMessages(ctx context.Context, id string) ([]Message, error)
	// ListMessagesActive returns messages where archived_at IS NULL,
	// ordered by sequence ASC. The default scrollback fetch for
	// sessions whose history has been compacted (compaction-strategy-ui
	// WP07). The SweptCount field is a placeholder for the count of
	// archived-then-hard-deleted rows; until WP09 lands a per-session
	// counter, it is always 0.
	ListMessagesActive(ctx context.Context, id string) (ListMessagesResult, error)
	// ListMessagesAll returns every message in the session including
	// archived rows, ordered by sequence ASC. Used when the user
	// toggles "Show full history" in the chat view.
	ListMessagesAll(ctx context.Context, id string) (ListMessagesResult, error)
	AppendMessage(ctx context.Context, id, role, content string) (Message, error)
	// SendMessageWithBlocks persists a user turn carrying polymorphic
	// content blocks (text + image + document). The legacy text-only
	// AppendMessage path is left untouched for callers that don't need
	// multimodal input. contentBlocks must be non-empty (multimodal-io
	// WP03 / FR-013).
	SendMessageWithBlocks(ctx context.Context, id string, contentBlocks []ContentBlock) (Message, error)
	SaveDraft(ctx context.Context, id, draft string) error
	LoadDraft(ctx context.Context, id string) (string, error)

	// SetSystemPrompt persists per-session starting context. kind is
	// 'system' (invisible, prepended on every send) or 'user_seed'
	// (visible — the caller is responsible for also appending the
	// content as a user message via AppendMessage).
	SetSystemPrompt(ctx context.Context, id, content, kind string) error

	// MoveToProject sets the session's project membership. An empty
	// projectID detaches the session and makes it loose.
	MoveToProject(ctx context.Context, id, projectID string) error

	// SuggestTitle forces a new auto-title generation and writes the
	// result regardless of the current auto_titled state (the "Suggest
	// new title" manual path — session-auto-titling WP04). Returns the
	// generated title string on success.
	SuggestTitle(ctx context.Context, id string) (string, error)

	// ClearTitle resets the session's name to "" and auto_titled=0,
	// re-enabling future auto-title attempts. Mirrors the session
	// manager's ClearTitle method (session-auto-titling WP04).
	ClearTitle(ctx context.Context, id string) error

	// GetUsage returns the cumulative token + cost aggregate for the
	// session (token-cost-telemetry-01KQ8TD7 WP03). Returns a zeroed
	// Aggregate with CostSource="unknown" for sessions with no usage
	// data yet.
	GetUsage(ctx context.Context, id string) (SessionUsage, error)

	// ResumeMessage opens a continuation stream against the partial
	// assistant row identified by messageID. The returned subscription
	// id is the same shape the LLM view's StartStream returns — the
	// frontend drains llm:stream-chunk + llm:stream-closed against it
	// to render the continuation bubble.
	//
	// Idempotency: when the partial row already has a continuation
	// row attached (i.e. an earlier Resume already succeeded), the API
	// returns ErrResumeAlreadyContinued without opening a new stream.
	// Callers should treat this as "the continuation is already on
	// disk, refresh the transcript" rather than as a hard error.
	//
	// long-turn-resilience-01KR3PRS WP03.
	ResumeMessage(ctx context.Context, sessionID, messageID string) (ResumeMessageResult, error)

	// LoadAutonomyProfile returns the persisted per-session autonomy
	// override Layer. Empty Layer means "inherit from project / global."
	// (autonomy-dial-01KR3M2A WP03)
	LoadAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error)
	// SaveAutonomyProfile persists the per-session autonomy.Layer.
	// Pass an empty Layer to clear the override.
	SaveAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error
	// ResolveAutonomy resolves the effective knobs for a session by
	// folding global → project → session layers. Returns the resolved
	// knobs plus the three input layers so panels can render badges.
	ResolveAutonomy(ctx context.Context, id string) (ResolvedAutonomy, error)

	// Export serialises a session transcript to the local filesystem.
	// format is "markdown" or "json". The file-picker dialog is opened
	// via the FilePicker port; the default filename is derived from the
	// session title and today's date. The caller receives the path chosen
	// by the user and the number of bytes written.
	//
	// Cedar gate: Action::"session.export" is checked before any work is
	// done; Cedar Deny returns an error and NO file is written and NO
	// audit event is emitted.
	//
	// Audit: on success emits KindSessionExport with the output basename
	// (NOT the full path — privacy invariant).
	//
	// session-export-01NDFSEX05 WP02.
	Export(ctx context.Context, sessionID, format string) (ExportResult, error)
}

// ExportResult is the wire shape returned by Sessions_Export.
type ExportResult struct {
	// Path is the absolute path chosen by the user via the file-picker.
	// Empty when the user cancelled.
	Path string `json:"path"`
	// ByteCount is the number of bytes written to the main export file.
	ByteCount int64 `json:"byteCount"`
}
