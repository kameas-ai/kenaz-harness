package compaction

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
)

// engine.go houses the threshold-mode summarize-then-replace compaction
// engine (plan §2.4). The engine owns the algorithm — span selection,
// tool-pair / recent-window boundary snapping, prompt building, the
// pre-flight cap check, the LLM call, the transactional persistence,
// and the audit emission. It owns NONE of the concrete dependencies:
// every collaborator is a small interface defined below so the engine
// stays unit-testable with in-memory fakes.
//
// WP08 wires the concrete implementations (session store, llm registry,
// capability catalog, audit emitter, settings reader). This file should
// have ZERO concrete imports of those subsystems.

// Engine performs threshold-mode session compaction.
//
// Concurrency: callers MUST ensure a single Engine call per session at
// a time (the chat runner serializes this naturally). The engine
// does NOT take any cross-session lock — the per-session serialization
// guarantee is the chat runner's, and the engine relies on it to keep
// "list active messages → snap boundary → write summary" race-free.
type Engine interface {
	// Compact summarizes the oldest oldestPct of tokens in sessionID's
	// history into a single synthetic system message and soft-archives
	// the originals. Returns the new summary message ID on success.
	//
	// A no-op return (summary not needed because the recent-window
	// clamp pulled the boundary to zero) returns "" with err==nil; the
	// chat runner translates that into ErrSessionFull when appropriate.
	//
	// Errors:
	//   *ErrCompactionModelTooSmall - span exceeds compaction model cap.
	//   ErrCompactionDuringToolPair  - defensive (should never fire).
	//   provider/capability errors    - bubbled untyped from the caller's
	//                                   provider call, classified by adapter.
	Compact(ctx context.Context, sessionID string, model ProviderProfileRef,
		oldestPct float64) (summaryMessageID string, err error)

	// RollingSummarize collapses every message past the most recent
	// recentWindow user-assistant pairs into a single rolling summary
	// that itself replaces any previous rolling summary. Invoked every
	// turn by the chat runner when CompactionAggressiveness == "maximal"
	// (plan §2.5).
	//
	// The "rolling pile" = previous rolling summary (if any) + every
	// turn added since the last roll. Output text is the new rolling
	// summary; the old rolling summary + new rolled-in turns are
	// archived in one transaction.
	//
	// Returns ("", nil) when the head already covers the recent window
	// and there's nothing to roll (no-op tick).
	//
	// Errors mirror Compact:
	//   *ErrCompactionModelTooSmall - rolling pile + previous summary
	//                                  exceeds compaction model cap. The
	//                                  chat runner catches this and silently
	//                                  treats this turn as the "aggressive"
	//                                  tier for graceful degradation.
	//   provider/capability errors    - bubbled untyped from the LLM call,
	//                                   classified by classifyLLMError into
	//                                   the audit error_kind.
	RollingSummarize(ctx context.Context, sessionID string,
		model ProviderProfileRef, recentWindow int) (rollingSummaryMessageID string, err error)
}

// ProviderProfileRef is the minimal shape the engine needs to identify
// the compaction model. The wider llm package today does not export a
// ProviderProfileRef type — when that type lands (provider-implementation-
// uniformity-01KQ8V4F or a follow-up), this struct can be aliased to it
// or replaced. Keeping the local declaration means the engine doesn't
// pin a circular dependency on a downstream package.
type ProviderProfileRef struct {
	// ProviderID is the provider key (e.g. "anthropic", "openai", "bedrock").
	ProviderID string
	// ModelID is the model identifier (e.g. "claude-sonnet-4-5").
	ModelID string
}

// String renders the ref for log lines and audit payloads.
func (r ProviderProfileRef) String() string {
	if r.ProviderID == "" && r.ModelID == "" {
		return "<unset>"
	}
	return r.ProviderID + ":" + r.ModelID
}

// EngineConfig is the dependency-injection envelope. See the field
// comments and the dependency-interface declarations below for what
// each plug-in is responsible for.
//
// All fields are required EXCEPT Now (defaults to time.Now) and
// RecentWindow (defaults to 4 — the locked plan default). The engine
// returns a constructor error if any required field is nil.
type EngineConfig struct {
	// Store is the message persistence dependency.
	Store MessageStore

	// LLM is the summarization-call dependency.
	LLM LLMCaller

	// Capabilities is the optional cap-lookup dependency. When the
	// model has no descriptor, the lookup returns ok=false and the
	// engine skips the pre-flight check (let the provider call surface
	// its own error).
	Capabilities CapabilityLookup

	// Audit is the audit-emission dependency. May be nil — the engine
	// never panics on a missing emitter (see also audit.Emit which
	// no-ops on a nil emitter).
	Audit AuditEmitter

	// RecentWindow is a settings-reader closure: returns the current
	// effective Settings.CompactionRecentWindow. Indirection lets
	// settings change at runtime without re-instantiating the engine.
	// nil falls back to the locked default of 4.
	RecentWindow func() int

	// Tokenizer is the token-counter dependency. Defaults to a wrapper
	// around core/llm/tokenizer.CountRequestTokens; tests can inject a
	// deterministic counter to make span boundaries predictable.
	Tokenizer TokenCounter

	// Now is the injectable clock. Defaults to time.Now.
	Now func() time.Time
}

// MessageStore is the persistence boundary. Implementations live in
// WP08 wiring; this file only depends on the interface.
type MessageStore interface {
	// ListActiveMessages returns messages where archived_at IS NULL,
	// ordered by sequence ascending.
	ListActiveMessages(ctx context.Context, sessionID string) ([]Message, error)

	// ApplyCompaction inserts the summary row + sets compacted_into_id
	// and archived_at on the originals atomically (single transaction).
	// Implementations MUST treat this as one transactional unit — a
	// partial write would leave the session in a broken state.
	ApplyCompaction(ctx context.Context, sessionID string, summary Message,
		originalIDs []string, archivedAt time.Time) error
}

// LLMCaller is the summarization-call boundary. Implementations live in
// WP08 wiring (chat runner / llm registry); this file only depends on
// the interface.
type LLMCaller interface {
	// CallForSummary runs a non-streaming completion against the given
	// provider/model with the given prompt; returns the assistant text
	// plus the input/output token counts. Errors should be classified
	// by the adapter into the typed taxonomy (errors.go in core/llm)
	// so the caller can detect cap-exceeded vs. transport vs. cancelled.
	CallForSummary(ctx context.Context, model ProviderProfileRef,
		systemPrompt, userPrompt string) (text string, inputTokens, outputTokens int, err error)
}

// CapabilityLookup is the cap-lookup boundary. Implementations wrap the
// core/llm/capabilities Catalog and translate ProviderProfileRef into
// the descriptor lookup.
type CapabilityLookup interface {
	// MaxContextTokens returns the model's MaxContextTokens budget.
	// Returns 0, ok=false if the model has no descriptor (unknown).
	MaxContextTokens(model ProviderProfileRef) (max int, ok bool)
}

// AuditEmitter is the audit-emission boundary. Implementations wrap the
// core/context/audit Emitter; this interface trims the surface to a
// fire-and-forget Emit so the engine doesn't have to deal with
// time-wrapping or marshal errors at every call site.
type AuditEmitter interface {
	// Emit records an audit event of the given Kind with payload (any
	// JSON-marshalable struct). Errors are swallowed by convention —
	// audit must never bring down the engine.
	Emit(ctx context.Context, kind audit.Kind, payload any)
}

// TokenCounter abstracts the tokenizer so tests can inject deterministic
// counts. The default implementation in NewEngine wraps
// core/llm/tokenizer.CountRequestTokens.
type TokenCounter interface {
	// Count returns the estimated token count for a system prompt plus
	// a slice of role-tagged text messages.
	Count(systemPrompt string, msgs []TokenizerMessage) int
}

// TokenizerMessage is the minimal shape the TokenCounter consumes. It
// mirrors core/llm/tokenizer.Message but lives here so the engine
// doesn't force its callers to import the tokenizer package directly.
type TokenizerMessage struct {
	Role    string
	Content string
}

// Message is the engine's internal message shape. It carries the
// minimum the algorithm needs:
//
//   - Sequence is monotonic and is what the boundary index walks across.
//   - Role drives the recent-window user-turn counter and the summary
//     row's role tag ("system" — see Compact step 8).
//   - ToolUseID is non-empty on assistant messages that opened a tool
//     call; ToolResultForID is non-empty on tool messages that closed
//     one. The pair is identified by ToolUseID == ToolResultForID.
//   - CreatedAt is informational; the engine itself uses Now() for
//     archived_at and compacted_at timestamps.
type Message struct {
	ID              string
	Role            string
	Content         string
	Sequence        int64
	ToolUseID       string
	ToolResultForID string
	CreatedAt       time.Time
}

// engine is the default Engine implementation. Constructed via NewEngine.
type engine struct {
	store        MessageStore
	llm          LLMCaller
	capabilities CapabilityLookup
	auditEm      AuditEmitter
	recentWindow func() int
	tokenizer    TokenCounter
	now          func() time.Time
}

// defaultRecentWindow is the plan-locked fallback (plan §2.6) used when
// EngineConfig.RecentWindow is nil. Callers wiring the real settings
// reader supply their own closure.
const defaultRecentWindow = 4

// summaryContentPrefix wraps the summarization-model output into the
// canonical "[Earlier conversation summary: ...]" container the chat
// runner and frontend recognize. Centralized so the prefix never drifts
// across files.
const summaryContentPrefix = "[Earlier conversation summary: "
const summaryContentSuffix = "]"

// summarizeUserPromptTemplate is the locked prompt body from plan §2.4
// step 2. The transcript is interpolated between <transcript> tags.
const summarizeUserPromptTemplate = `You are summarizing an excerpt of a longer conversation.
Preserve facts, decisions, tool inputs and outputs, file paths, and any
identifiers verbatim. Compress aggressively; remove pleasantries and
filler. Do not invent. Do not editorialize. Output a single block of
plain text.

<transcript>
%s
</transcript>`

// NewEngine constructs the threshold-mode engine. Returns an error if
// required dependencies are missing. Optional fields (Now,
// RecentWindow, Tokenizer) get sensible defaults.
func NewEngine(cfg EngineConfig) (Engine, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("compaction: EngineConfig.Store is required")
	}
	if cfg.LLM == nil {
		return nil, fmt.Errorf("compaction: EngineConfig.LLM is required")
	}
	if cfg.Capabilities == nil {
		return nil, fmt.Errorf("compaction: EngineConfig.Capabilities is required")
	}

	rw := cfg.RecentWindow
	if rw == nil {
		rw = func() int { return defaultRecentWindow }
	}

	tok := cfg.Tokenizer
	if tok == nil {
		tok = defaultTokenizer{}
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &engine{
		store:        cfg.Store,
		llm:          cfg.LLM,
		capabilities: cfg.Capabilities,
		auditEm:      cfg.Audit,
		recentWindow: rw,
		tokenizer:    tok,
		now:          now,
	}, nil
}

// Compact runs the threshold-mode flow. Each step references the plan
// section it implements.
func (e *engine) Compact(ctx context.Context, sessionID string, model ProviderProfileRef,
	oldestPct float64) (string, error) {
	// Clamp oldestPct into the legal [0, 1] range. Out-of-band values
	// from a malformed setting shouldn't crash the engine; treat <=0 as
	// no-op and >1 as 1.
	if oldestPct <= 0 {
		// Caller asked for nothing; emit the documented no-op audit and
		// return cleanly. Loading messages would be wasted I/O.
		e.emitNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}
	if oldestPct > 1 {
		oldestPct = 1
	}

	// Step 1: Load active messages.
	messages, err := e.store.ListActiveMessages(ctx, sessionID)
	if err != nil {
		// I/O failure before we've done any work — emit failed audit
		// and bubble. ErrorKind classifier covers the storage-error
		// shape so the audit row stays informative.
		e.emitFailedAudit(ctx, sessionID, model, 0, "store_error")
		return "", err
	}
	if len(messages) == 0 {
		// Empty session — nothing to compact.
		e.emitNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}

	// Step 2: Tokenize each message in-order; walk forward accumulating
	// until the running sum reaches oldestPct * totalTokens. The
	// boundary index is the first message NOT to compact (everything
	// strictly less than the boundary will be summarized).
	perMessage := make([]int, len(messages))
	totalTokens := 0
	for i, m := range messages {
		c := e.tokenizer.Count("", []TokenizerMessage{{Role: m.Role, Content: m.Content}})
		perMessage[i] = c
		totalTokens += c
	}

	target := int(float64(totalTokens) * oldestPct)
	boundary := 0
	running := 0
	for i := range messages {
		if running >= target {
			break
		}
		running += perMessage[i]
		boundary = i + 1
	}
	// boundary now points at the first kept message (exclusive upper
	// bound on the span). Apply the two snap helpers in turn:

	// Step 3: Tool-pair clamp. Pure helper in snap.go.
	boundary = snapBoundaryForToolPairs(messages, boundary)

	// Step 4: Recent-window clamp. Pure helper in snap.go.
	rw := e.recentWindow()
	boundary = snapBoundaryForRecentWindow(messages, boundary, rw)

	// If the clamps pulled the boundary to zero (or below) the
	// compaction is a no-op. Emit the documented audit and return
	// cleanly so the chat runner can fall through to ErrSessionFull
	// (plan §2.6).
	if boundary <= 0 {
		// tokensInSpan computes from the original target; it's 0 here
		// because the span itself is empty. compression_ratio = 1.0 by
		// convention (no compression happened).
		e.emitNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}

	// The span is messages[:boundary]. Compute its token total — needed
	// for the pre-flight cap check and the audit payload.
	tokensInSpan := 0
	for i := 0; i < boundary; i++ {
		tokensInSpan += perMessage[i]
	}

	// Step 5: Build the summarization prompt. system_prompt is empty;
	// user_prompt is the locked boilerplate plus the rendered transcript.
	transcript := renderTranscript(messages[:boundary])
	userPrompt := fmt.Sprintf(summarizeUserPromptTemplate, transcript)
	systemPrompt := ""

	// Step 6: Pre-flight cap check. Estimate the prompt's token cost via
	// the tokenizer (system + single user message carrying the user
	// prompt). Compare against the compaction model's MaxContextTokens.
	estimatedPromptTokens := e.tokenizer.Count(systemPrompt, []TokenizerMessage{
		{Role: "user", Content: userPrompt},
	})
	if cap, ok := e.capabilities.MaxContextTokens(model); ok && estimatedPromptTokens > cap {
		err := &ErrCompactionModelTooSmall{
			NeedsTokens:    estimatedPromptTokens,
			ModelMaxTokens: cap,
		}
		e.emitFailedAudit(ctx, sessionID, model, tokensInSpan, "model_too_small")
		return "", err
	}

	// Step 7: Run the call. On error, classify the error_kind and emit
	// failed audit. Originals stay untouched (no store write yet).
	text, _, outputTokens, err := e.llm.CallForSummary(ctx, model, systemPrompt, userPrompt)
	if err != nil {
		e.emitFailedAudit(ctx, sessionID, model, tokensInSpan, classifyLLMError(err))
		return "", err
	}

	// Step 8: Build the synthetic summary message. Sequence = lowest
	// sequence in the span so the summary sits at the head of the
	// surviving history. Role is "system" (plan §2.4 step 5). Content is
	// wrapped in the canonical prefix/suffix.
	summaryID, err := newSummaryID()
	if err != nil {
		// crypto/rand failure is exceedingly rare but possible on
		// constrained kernels — surface it as a typed audit and bubble.
		e.emitFailedAudit(ctx, sessionID, model, tokensInSpan, "id_generation_failed")
		return "", err
	}
	now := e.now()
	summary := Message{
		ID:        summaryID,
		Role:      "system",
		Content:   summaryContentPrefix + text + summaryContentSuffix,
		Sequence:  messages[0].Sequence,
		CreatedAt: now,
	}

	// Step 9: Persist atomically.
	originalIDs := make([]string, boundary)
	for i := 0; i < boundary; i++ {
		originalIDs[i] = messages[i].ID
	}
	if err := e.store.ApplyCompaction(ctx, sessionID, summary, originalIDs, now); err != nil {
		// Persistence failed AFTER a successful LLM call — the worst
		// outcome (we paid the tokens). Emit failed audit and bubble;
		// the chat runner can surface a "compaction prepared but failed
		// to save" message. We tag the error_kind so the audit log
		// distinguishes this from an LLM-side failure.
		e.emitFailedAudit(ctx, sessionID, model, tokensInSpan, "persist_error")
		return "", err
	}

	// Step 10: Success audit. compression_ratio = output / span.
	ratio := 0.0
	if tokensInSpan > 0 {
		ratio = float64(outputTokens) / float64(tokensInSpan)
	}
	if e.auditEm != nil {
		e.auditEm.Emit(ctx, audit.KindSessionCompacted, audit.SessionCompactedPayload{
			SessionID:          sessionID,
			AggressivenessTier: "", // engine doesn't know the user-facing tier name; chat runner can re-emit if it needs richer copy
			ModelUsed:          model.String(),
			TokensInSpan:       tokensInSpan,
			TokensAfterSummary: outputTokens,
			CompressionRatio:   ratio,
		})
	}

	return summary.ID, nil
}

// emitNoOpAudit emits a KindSessionCompacted audit with
// compression_ratio = 1.0 — the documented no-op contract from plan §2.6.
// Centralized so the three no-op return paths above stay consistent.
func (e *engine) emitNoOpAudit(ctx context.Context, sessionID string, model ProviderProfileRef, tokensInSpan int) {
	if e.auditEm == nil {
		return
	}
	e.auditEm.Emit(ctx, audit.KindSessionCompacted, audit.SessionCompactedPayload{
		SessionID:          sessionID,
		AggressivenessTier: "",
		ModelUsed:          model.String(),
		TokensInSpan:       tokensInSpan,
		TokensAfterSummary: tokensInSpan,
		CompressionRatio:   1.0,
	})
}

// emitFailedAudit emits a KindCompactionFailed audit with the given
// error classifier. Centralized for the same reason as emitNoOpAudit.
func (e *engine) emitFailedAudit(ctx context.Context, sessionID string, model ProviderProfileRef, tokensInSpan int, errorKind string) {
	if e.auditEm == nil {
		return
	}
	e.auditEm.Emit(ctx, audit.KindCompactionFailed, audit.CompactionFailedPayload{
		SessionID:          sessionID,
		AggressivenessTier: "",
		ModelUsed:          model.String(),
		TokensInSpan:       tokensInSpan,
		ErrorKind:          errorKind,
	})
}

// renderTranscript concatenates the span messages into a plain-text
// transcript the summarizer prompt embeds between <transcript> tags.
// One line per message, "role: content" — providers handle multi-line
// content fine; we don't need elaborate framing.
func renderTranscript(span []Message) string {
	var b strings.Builder
	for i, m := range span {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}

// classifyLLMError reduces a provider error into the short string the
// CompactionFailedPayload.ErrorKind field documents. The classifier is
// best-effort: known error families (cancellation, transient, cap-exceeded,
// auth) get specific labels; anything else is "provider_error".
//
// We deliberately use string sniffing as a fallback because the engine
// has no concrete typed-error import (those live in core/llm and would
// pull in the registry). The chat runner's wiring layer can re-classify
// upstream of the engine if it wants typed precision.
func classifyLLMError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context_length_exceeded"),
		strings.Contains(msg, "context length"),
		strings.Contains(msg, "max context"),
		strings.Contains(msg, "too large"):
		return "cap_exceeded"
	case strings.Contains(msg, "auth"), strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "auth_error"
	case strings.Contains(msg, "transient"), strings.Contains(msg, "429"), strings.Contains(msg, "5"):
		// 5 is a weak signal but transient classification is best-effort
		// — see comment above. Provider adapters that want a precise
		// tag should classify upstream.
		return "transient"
	}
	return "provider_error"
}

// newSummaryID returns a hex-encoded random id for the synthetic summary
// message. Twenty bytes (40 hex chars) is more than enough entropy and
// matches the project convention used in core/corpus/manager.go.
//
// WP08 may swap to a project-standard id helper (e.g. ULID) if one
// lands in core; the engine has no preference as long as the id is
// unique within the session_messages table.
func newSummaryID() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("compaction: random id: %w", err)
	}
	return "cmp-" + hex.EncodeToString(b), nil
}

// defaultTokenizer wraps core/llm/tokenizer.CountRequestTokens behind
// the TokenCounter interface so the engine has a usable default without
// forcing every caller to construct one. Tests inject a fake counter
// directly via EngineConfig.Tokenizer.
type defaultTokenizer struct{}

// Count translates the engine's TokenizerMessage shape into the
// tokenizer package's Message shape and delegates. The wrapper exists
// only because we don't want the engine package to import the
// tokenizer package symbols directly into its public interface.
func (defaultTokenizer) Count(systemPrompt string, msgs []TokenizerMessage) int {
	out := make([]tokenizer.Message, len(msgs))
	for i, m := range msgs {
		out[i] = tokenizer.Message{Role: m.Role, Content: m.Content}
	}
	return tokenizer.CountRequestTokens(systemPrompt, out)
}
