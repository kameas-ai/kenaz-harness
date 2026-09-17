package fleet

// usage_emitter.go — the closed vocabulary of usage telemetry.
//
// This is the ONLY writer to the fleet usage lanes (otlp_usage_lane.go). Every
// method takes typed, bounded inputs and builds a fixed-shape body; there is
// no method that accepts a caller-supplied map, string body, or attribute.
// That is the privacy projection: a call site cannot leak what the signature
// has no parameter for.
//
// # Lane routing
//
// Each occurrence goes to exactly ONE lane, chosen by the EFFECTIVE consent
// level at that instant (effective = stored level clamped by org tier, so a
// downgraded account fails closed):
//
//	none       → nothing
//	aggregate  → label-less counters   (metrics lane)
//	full       → kind-tagged events    (logs lane)
//
// Exclusive routing is load-bearing. Fleet rolls both lanes into the same
// per-kind hourly counts; if "full" also incremented the counters, every
// conversation would be counted twice. It is also what keeps the Aggregate
// tier's user-facing promise — "counts only, no log records" — true in code.
//
// # What is projected away
//
//   - conversation ids are random per-segment UUIDs minted by
//     ConversationTracker. The local session id never reaches this file.
//   - tool names: only names in a compiled allowlist pass verbatim. MCP names
//     are user-authored, and a model can invent any name (including a
//     "kenaz__"-prefixed one), so every other tool is ExternalToolName.
//   - model providers pass through a closed allowlist, else "other".
//   - errors carry a closed category enum. Never a message.

import (
	"context"
	"strings"
	"time"

	otellog "go.opentelemetry.io/otel/log"
)

// ExternalToolName is what every non-builtin tool is reported as.
const ExternalToolName = "external_tool"

// knownBuiltinTools is the compiled allowlist of tool names reported verbatim.
//
// A reserved prefix is NOT proof of a compiled name: toolPostDispatch reports
// whatever name the MODEL emitted, including calls that fail as unknown, so a
// model can invent "kenaz__acme_payroll_q3". Only names in this set pass;
// everything else — unknown, malformed, MCP, future — is ExternalToolName.
// TestKnownBuiltinTools_CoverProductionRegistry (core/rpc) fails when a
// registered builtin is missing, so a new tool defaults safe until added.
var knownBuiltinTools = map[string]bool{
	"kenaz__ask_user_question":         true,
	"kenaz__bash":                      true,
	"kenaz__build_knowledge_site":      true,
	"kenaz__edit_file":                 true,
	"kenaz__enter_plan_mode":           true,
	"kenaz__exit_plan_mode":            true,
	"kenaz__glob":                      true,
	"kenaz__grep":                      true,
	"kenaz__list_dir":                  true,
	"kenaz__list_open_worklist":        true,
	"kenaz__list_secrets":              true,
	"kenaz__monitor":                   true,
	"kenaz__read_context_file":         true,
	"kenaz__read_file":                 true,
	"kenaz__request_filesystem_access": true,
	"kenaz__save_artifact":             true,
	"kenaz__save_document":             true,
	"kenaz__skill":                     true,
	"kenaz__sleep":                     true,
	"kenaz__subagent_dispatch":         true,
	"kenaz__todo_write":                true,
	"kenaz__update_artifact":           true,
	"kenaz__update_document":           true,
	"kenaz__web_fetch":                 true,
	"kenaz__web_search":                true,
	"kenaz__write_file":                true,
}

// KnownBuiltinToolName reports whether name is in the compiled allowlist.
func KnownBuiltinToolName(name string) bool { return knownBuiltinTools[name] }

// ProjectToolName maps a runtime tool name onto the exportable vocabulary.
func ProjectToolName(name string) string {
	if knownBuiltinTools[name] {
		return name
	}
	return ExternalToolName
}

// knownModelProviders is the closed set of provider kinds reported verbatim:
// the compiled adapter Kind constants under core/llm/*. A kind is an adapter
// identifier, not a user-chosen profile name — but the value arrives as a
// string from the runtime, so anything outside this set still collapses to
// "other" rather than being trusted.
var knownModelProviders = map[string]bool{
	"anthropic":     true,
	"openai":        true,
	"azure-openai":  true,
	"bedrock":       true,
	"gemini":        true,
	"ollama":        true,
	"openrouter":    true,
	"custom-openai": true,
}

// OtherModelProvider is reported for any provider kind outside the allowlist.
const OtherModelProvider = "other"

// ProjectModelProvider maps a provider kind onto the exportable vocabulary.
func ProjectModelProvider(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if knownModelProviders[k] {
		return k
	}
	return OtherModelProvider
}

// ErrorCategory is the closed set of harness.error categories.
type ErrorCategory string

const (
	ErrorCategoryAuth      ErrorCategory = "auth"
	ErrorCategoryTransient ErrorCategory = "transient"
	ErrorCategoryCancelled ErrorCategory = "cancelled"
	ErrorCategoryBudget    ErrorCategory = "budget"
	ErrorCategoryUnknown   ErrorCategory = "unknown"
)

// ProjectErrorCategory maps an arbitrary classifier string onto the closed
// enum. Unrecognised input — including an accidental error MESSAGE — becomes
// "unknown"; it is never forwarded.
func ProjectErrorCategory(s string) ErrorCategory {
	switch ErrorCategory(strings.ToLower(strings.TrimSpace(s))) {
	case ErrorCategoryAuth:
		return ErrorCategoryAuth
	case ErrorCategoryTransient:
		return ErrorCategoryTransient
	case ErrorCategoryCancelled:
		return ErrorCategoryCancelled
	case ErrorCategoryBudget:
		return ErrorCategoryBudget
	default:
		return ErrorCategoryUnknown
	}
}

// ConsentLevelReader is the consent input UsageEmitter needs.
// *TelemetryConsent satisfies it.
type ConsentLevelReader interface {
	EffectiveLevel() ConsentLevel
}

// usageSink is the pipeline surface UsageEmitter writes to.
// *FleetOTLPPipeline satisfies it.
type usageSink interface {
	EmitEvent(ctx context.Context, kind LogEventKind, body []otellog.KeyValue) bool
	AddCount(ctx context.Context, name UsageCounter, n int64) bool
}

// UsageEmitter routes lifecycle facts to the consented lane. Safe for
// concurrent use. A nil *UsageEmitter is a valid no-op.
type UsageEmitter struct {
	sink    usageSink
	consent ConsentLevelReader
}

// NewUsageEmitter binds an emitter to a pipeline and a consent source.
// A nil consent reader is treated as ConsentNone — the emitter sends nothing.
func NewUsageEmitter(p *FleetOTLPPipeline, consent ConsentLevelReader) *UsageEmitter {
	if p == nil {
		return nil
	}
	return &UsageEmitter{sink: p, consent: consent}
}

func (e *UsageEmitter) level() ConsentLevel {
	if e == nil || e.sink == nil || e.consent == nil {
		return ConsentNone
	}
	return e.consent.EffectiveLevel()
}

// ConversationStarted reports a new conversation segment. Returns whether the
// fact was accepted into a lane; ConversationTracker uses that to keep
// started/ended paired.
func (e *UsageEmitter) ConversationStarted(ctx context.Context, conversationID, providerKind string) bool {
	switch e.level() {
	case ConsentAggregate:
		return e.sink.AddCount(ctx, CounterConversationsStarted, 1)
	case ConsentFull:
		if conversationID == "" {
			return false
		}
		return e.sink.EmitEvent(ctx, LogKindHarnessConversationStarted, []otellog.KeyValue{
			otellog.String("conversation_id", conversationID),
			otellog.String("model_provider", ProjectModelProvider(providerKind)),
		})
	default:
		return false
	}
}

// ConversationEnded reports the end of a segment with its totals.
func (e *UsageEmitter) ConversationEnded(
	ctx context.Context,
	conversationID string,
	duration time.Duration,
	tokenIn, tokenOut int64,
	costUSD float64,
) bool {
	if tokenIn < 0 {
		tokenIn = 0
	}
	if tokenOut < 0 {
		tokenOut = 0
	}
	if costUSD < 0 {
		costUSD = 0
	}
	switch e.level() {
	case ConsentAggregate:
		ok := e.sink.AddCount(ctx, CounterConversationsEnded, 1)
		e.sink.AddCount(ctx, CounterTokensInput, tokenIn)
		e.sink.AddCount(ctx, CounterTokensOutput, tokenOut)
		return ok
	case ConsentFull:
		if conversationID == "" {
			return false
		}
		ms := duration.Milliseconds()
		if ms < 1 {
			// Fleet's validator requires duration_ms > 0; a segment that
			// closed inside the same millisecond still happened.
			ms = 1
		}
		return e.sink.EmitEvent(ctx, LogKindHarnessConversationEnded, []otellog.KeyValue{
			otellog.String("conversation_id", conversationID),
			otellog.Int64("duration_ms", ms),
			otellog.Int64("token_in", tokenIn),
			otellog.Int64("token_out", tokenOut),
			otellog.Float64("cost_usd", costUSD),
		})
	default:
		return false
	}
}

// ToolInvoked reports one completed tool call: projected name, latency,
// success. Never arguments, never output.
func (e *UsageEmitter) ToolInvoked(ctx context.Context, toolName string, latency time.Duration, success bool) bool {
	switch e.level() {
	case ConsentAggregate:
		return e.sink.AddCount(ctx, CounterToolInvocations, 1)
	case ConsentFull:
		ms := latency.Milliseconds()
		if ms < 0 {
			ms = 0
		}
		return e.sink.EmitEvent(ctx, LogKindHarnessToolInvoked, []otellog.KeyValue{
			otellog.String("tool_name", ProjectToolName(toolName)),
			otellog.Int64("latency_ms", ms),
			otellog.Bool("success", success),
		})
	default:
		return false
	}
}

// Error reports one harness error by closed category.
func (e *UsageEmitter) Error(ctx context.Context, category ErrorCategory, recoverable bool) bool {
	switch e.level() {
	case ConsentAggregate:
		return e.sink.AddCount(ctx, CounterErrors, 1)
	case ConsentFull:
		return e.sink.EmitEvent(ctx, LogKindHarnessError, []otellog.KeyValue{
			otellog.String("category", string(ProjectErrorCategory(string(category)))),
			otellog.Bool("recoverable", recoverable),
		})
	default:
		return false
	}
}
