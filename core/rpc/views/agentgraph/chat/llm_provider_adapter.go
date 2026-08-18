package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/fallback"
	"github.com/kameas-ai/kenaz-harness/core/llm/retry"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	artview "github.com/kameas-ai/kenaz-harness/core/rpc/views/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// LLMProviderAdapter satisfies the agentgraph.LLMProvider seam by
// translating the kernel-side LLMRequest into a corellm.GenerationRequest,
// opening a stream against the existing core/llm.Registry, draining
// the events into the kernel-bound StreamSink, and returning the
// terminal LLMResponse the model executor records on its `response`
// port.
//
// The adapter is intentionally narrow: it delegates every provider
// concern (caching, reasoning, tool-spec translation, retry, audit) to
// the underlying registry. The kernel just sees a synchronous Generate
// that returns once the upstream stream closes.
//
// Stream order parity is the load-bearing property: every text delta
// the registry produces flows to env.StreamSink in the order it lands,
// so the chat surface keeps receiving byte-equal deltas.
type LLMProviderAdapter struct {
	reg corellm.Registry
	// profileID overrides what gets sent to the registry. The kernel-
	// side LLMRequest carries `Provider` + `Model`, but the registry
	// resolves credentials via a profile id — the runner threads the
	// active session's profile id through the adapter at construction
	// time so every dispatch lands on the right credentials.
	profileID string
	// modelOverride is the per-request model selection from the chat
	// surface's model picker. Empty means "use the profile's default
	// model".
	modelOverride string
	// sessionID is the active chat session, retained for the generated-
	// image auto-capture path so OnGeneratedImage can record the session
	// provenance without re-plumbing it through every Generate call.
	sessionID string
	// tools is the tool catalog produced by the chassis-side tool
	// discoverer; the kernel's LLMRequest.Tools slice is just a string
	// allowlist, but the registry needs the full ToolSpec shape.
	tools []corellm.ToolSpec
	// lastRespMu protects lastResp.
	lastRespMu sync.Mutex
	// lastResp stores the most recent llm.Response produced by Generate.
	// The session_write HookPostLLM callback reads this to record usage.
	// Overwritten on every Generate call; safe because each kernel run is
	// sequential (one Generate completes before session_write fires, and
	// session_write fires before the next Generate could start).
	lastResp corellm.Response

	// capturer is the optional generated-image auto-capture pipeline
	// (multimodal-io-extended-01KQ8TD2 WP02). When non-nil, each
	// StreamGeneratedImage event in the drain loop is forwarded to
	// capturer.OnGeneratedImage so the image lands in the artifact store
	// with Source=="model_output". nil disables capture — the event is
	// still forwarded to the kernel StreamSink for frontend rendering.
	capturer artview.GeneratedImageCapturer

	// pendingImagesMu guards pendingImages.
	pendingImagesMu sync.Mutex
	// pendingImages accumulates GeneratedImagePayload values received
	// during a Generate call. The HookPostLLM callback drains this slice
	// (via DrainPendingImages) after session_write has persisted the
	// assistant message and produced a stable messageID.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	pendingImages []artview.GeneratedImagePayload

	// now is the injected clock for the environment-context layer
	// (system-prompt-layers WP03). nil falls back to time.Now so tests
	// can pin a deterministic date while production reads the wall clock.
	now func() time.Time
	// workspaceDir is the absolute agent-workspace path used to render the
	// environment block's workspace line. Empty renders a generic
	// "sandboxed workspace" note instead of a concrete path.
	workspaceDir string
	// workspaceNote is the honest trailing description for the workspace
	// line (spec 089 FR-4): granted mount vs private sandbox vs fallback.
	// Empty keeps the generic pre-089 wording.
	workspaceNote string
	// customInstructions returns the user's chat custom-instructions text,
	// evaluated once per Generate so a Settings edit takes effect on the
	// next turn (system-prompt-layers WP04). nil / empty appends no user
	// layer.
	customInstructions func() string

	// recapStyle returns the resolved autonomy recapStyle knob, or is nil
	// when no autonomy provider is wired (test / boot paths).
	// (autonomy-knobs-live-01PMAG02 WP06)
	recapStyle func() autonomy.RecapMode

	// askOnAmbiguity returns the resolved autonomy askOnAmbiguity knob,
	// or is nil when no autonomy provider is wired (test / boot paths).
	// (autonomy-knobs-live-01PMAG02 WP02)
	askOnAmbiguity func() autonomy.AskMode

	// moves is the turn's move journal
	// (model-moves-transcript-01PMCH01 WP02). Non-nil only on the chat
	// path; the adapter feeds it the two facts only Generate knows —
	// that a chat-bound segment has started streaming, and what that
	// segment's finished text was.
	moves *turnJournal

	// attachments resolves the session's system-kind attachments
	// (first-run-onboarding-01PMOB01 WP02). nil disables the layer
	// entirely — the pre-existing behaviour for every chat session before
	// this field existed. This is the read half of the seam
	// core/rpc/views/sessions/impl.go's SetSystemPrompt writes: before
	// this, nothing in the agentgraph chat path ever called
	// attachments.Manager.ListResolved, so a session-scoped starting
	// context (onboarding's or an ordinary session's, via
	// NewSessionDialog.vue) was persisted correctly but never reached the
	// model. See buildAttachmentsBlock.
	attachments AttachmentsResolver
}

// ResolvedAttachment is the narrow shape buildAttachmentsBlock consumes.
// Defined here (mirroring core/rpc/views/llm.ResolvedAttachment) so the
// chat package does not need to import core/attachments directly; the
// chassis (core/rpc/api.go) bridges the concrete attachments.Manager into
// this shape.
type ResolvedAttachment struct {
	Content string
	Kind    string
}

// AttachmentsResolver returns the resolved (global+project+session)
// attachment list for a session, in declared injection order. nil means
// "attachments not wired" — buildAttachmentsBlock renders no layer, which
// is byte-identical to every chat turn before this field existed.
type AttachmentsResolver interface {
	ListResolved(ctx context.Context, sessionID string) ([]ResolvedAttachment, error)
}

// NewLLMProviderAdapter constructs an adapter pinned to a specific
// (profileID, sessionID, modelOverride, toolCatalog, capturer) tuple.
// One adapter per chat run; the runner constructs it inside
// StartStream. capturer may be nil to disable image auto-capture.
func NewLLMProviderAdapter(reg corellm.Registry, profileID, modelOverride string, tools []corellm.ToolSpec, capturer artview.GeneratedImageCapturer) *LLMProviderAdapter {
	return &LLMProviderAdapter{
		reg:           reg,
		profileID:     profileID,
		modelOverride: modelOverride,
		tools:         tools,
		capturer:      capturer,
	}
}

// WithMoveJournal attaches the turn's move journal
// (model-moves-transcript-01PMCH01 WP02). Returns the same pointer so
// callers can chain at construction time. A nil journal leaves the
// adapter's behaviour byte-identical to the pre-mission path.
func (a *LLMProviderAdapter) WithMoveJournal(j *turnJournal) *LLMProviderAdapter {
	a.moves = j
	return a
}

// WithSessionID pins the session id onto the adapter so the
// generated-image capture path can record provenance without
// re-plumbing sessionID through every Generate call. Called by the
// chat runner immediately after construction.
func (a *LLMProviderAdapter) WithSessionID(sessionID string) *LLMProviderAdapter {
	a.sessionID = sessionID
	return a
}

// WithEnvContext pins the environment-context inputs (injected clock +
// agent-workspace path + its honest description) onto the adapter so
// Generate can render the dynamic environment layer that stacks on top
// of the composed graph + node-role system prompt. A nil clock falls
// back to time.Now; an empty workspaceDir renders a generic
// sandboxed-workspace note; an empty workspaceNote keeps the pre-089
// generic wording. (system-prompt-layers WP03; spec 089 FR-4)
func (a *LLMProviderAdapter) WithEnvContext(now func() time.Time, workspaceDir, workspaceNote string) *LLMProviderAdapter {
	a.now = now
	a.workspaceDir = workspaceDir
	a.workspaceNote = workspaceNote
	return a
}

// WithAttachments pins the session-attachments resolver onto the adapter
// (first-run-onboarding-01PMOB01 WP02). nil disables the layer.
func (a *LLMProviderAdapter) WithAttachments(resolver AttachmentsResolver) *LLMProviderAdapter {
	a.attachments = resolver
	return a
}

// buildAttachmentsBlock resolves the session's system-kind attachments and
// joins their content in declared (global, project, session) order, or
// returns "" when nothing is wired / nothing resolves / the resolver
// errors. A resolver error degrades to no layer rather than failing the
// turn — the same fail-open posture buildEnvBlock uses for a workspace
// listing error, and consistent with every other layer here being
// best-effort. Errors are logged so a persistent failure is diagnosable.
//
// This is the read half of FR-004: core/rpc/views/sessions/impl.go's
// SetSystemPrompt (the canonical, attachments-aware seam) writes a
// position-0 inline session-scope attachment; this method is what makes
// that write reach the model on the session's next turn.
func (a *LLMProviderAdapter) buildAttachmentsBlock(ctx context.Context) string {
	if a == nil || a.attachments == nil {
		return ""
	}
	resolved, err := a.attachments.ListResolved(ctx, a.sessionID)
	if err != nil {
		logging.L().Warn("chat.attachments.resolve_failed",
			"session_id", a.sessionID, "err", err.Error())
		return ""
	}
	var parts []string
	for _, att := range resolved {
		if att.Kind != "system" || att.Content == "" {
			continue
		}
		parts = append(parts, att.Content)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// envClockNow reads the injected clock, defaulting to the wall clock.
func (a *LLMProviderAdapter) envClockNow() time.Time {
	if a != nil && a.now != nil {
		return a.now()
	}
	return time.Now()
}

// buildEnvBlock gathers the live environment facts and renders the
// compact environment-context Markdown block. Called once per Generate.
// The workspace entry count is a cheap top-level os.ReadDir; a failure
// (missing dir, permission) degrades gracefully to "path only" rather
// than aborting the turn. (system-prompt-layers WP03)
func (a *LLMProviderAdapter) buildEnvBlock() string {
	in := envContextInput{
		Now:    a.envClockNow(),
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		Model:  a.ActiveModelID(),
		Tools:  a.tools,
	}
	if ws := strings.TrimSpace(a.workspaceDir); ws != "" {
		in.WorkspaceDir = ws
		in.WorkspaceKnown = true
		in.WorkspaceNote = a.workspaceNote
		if entries, err := os.ReadDir(ws); err == nil {
			in.WorkspaceEntries = len(entries)
			in.WorkspaceCounted = true
		}
	}
	return buildEnvContext(in)
}

// WithRecapStyle pins the autonomy recapStyle resolver onto the adapter
// (autonomy-knobs-live-01PMAG02 WP06). Evaluated once per Generate so a
// Settings edit takes effect on the next turn.
//
// recapStyle was one of the resolved-but-unread autonomy knobs: it was
// computed through the three-layer inheritance chain, given a source
// trace, rendered in the Settings panel, and consumed by nothing.
func (a *LLMProviderAdapter) WithRecapStyle(fn func() autonomy.RecapMode) *LLMProviderAdapter {
	a.recapStyle = fn
	return a
}

// autonomy-knobs-live-01PMAG02 WP07: declare buildRecapBlock as the
// runtime consumer of recapStyle.
func init() {
	knobcoverage.Register[autonomy.ResolvedKnobs]("RecapStyle", "chat.LLMProviderAdapter.buildRecapBlock")
}

// buildRecapBlock renders the end-of-turn recap instruction for the
// active recapStyle, or "" when unset.
//
// RecapNone is deliberately NOT "" -- "say nothing extra" is a real
// instruction and differs from having no opinion. Only a nil resolver or
// an unrecognised mode yields no layer, which is what keeps a harness
// with no autonomy provider byte-identical to pre-01PMAG02.
func (a *LLMProviderAdapter) buildRecapBlock() string {
	if a == nil || a.recapStyle == nil {
		return ""
	}
	switch a.recapStyle() {
	case autonomy.RecapNone:
		return "When you finish a multi-step task, state the result only. Do not summarise the steps you took."
	case autonomy.RecapBrief:
		return "When you finish a multi-step task, close with a one-or-two sentence recap of what you did."
	case autonomy.RecapFull:
		return "When you finish a multi-step task, close with a structured recap: what you did, what you found, and anything you could not complete."
	default:
		return ""
	}
}

// WithAskOnAmbiguity pins the autonomy askOnAmbiguity resolver onto the
// adapter (autonomy-knobs-live-01PMAG02 WP02). Evaluated once per
// Generate so a Settings edit takes effect on the next turn.
func (a *LLMProviderAdapter) WithAskOnAmbiguity(fn func() autonomy.AskMode) *LLMProviderAdapter {
	a.askOnAmbiguity = fn
	return a
}

// buildAskBarBlock renders the bar for using kenaz__ask_user_question
// at hard/major, or "" otherwise (spec §3.1 bullet 1).
//
// At proceed/never the tool is withheld from the catalog entirely (see
// StartStream's catalog-shaping filter), so a bar statement would be
// moot — an unreachable tool needs no usage guidance. At "always" the
// model already sees the tool with no special framing (today's
// behaviour, preserved for FR-005): asking freely is the point of that
// setting, so no bar narrows it. Only hard/major need a stated
// threshold, because those are the settings where the tool is present
// but its use is meant to be selective.
func (a *LLMProviderAdapter) buildAskBarBlock() string {
	if a == nil || a.askOnAmbiguity == nil {
		return ""
	}
	switch a.askOnAmbiguity() {
	case autonomy.AskHard:
		return "Only use ask_user_question when a decision is high-stakes or hard to reverse " +
			"(e.g. destructive actions, irreversible external side effects, or a choice that is " +
			"expensive to undo). For anything else, proceed on your own judgement and state the " +
			"assumption you made."
	case autonomy.AskMajor:
		return "Only use ask_user_question when the correct answer meaningfully depends on " +
			"information only the user has, or when the plausible interpretations of the request " +
			"would lead to substantially different outcomes. For minor ambiguities, proceed on " +
			"your own judgement and state the assumption you made."
	default:
		return ""
	}
}

// WithCustomInstructions pins the user chat custom-instructions resolver
// onto the adapter. The resolver is evaluated once per Generate so a
// Settings edit takes effect on the next turn. A nil resolver / empty
// value appends no user layer. (system-prompt-layers WP04)
func (a *LLMProviderAdapter) WithCustomInstructions(fn func() string) *LLMProviderAdapter {
	a.customInstructions = fn
	return a
}

// buildUserInstructionsBlock renders the user custom-instructions layer,
// or "" when unset/blank. It is the FINAL system-prompt layer, stacked
// after the graph base, node role, and environment context so the user's
// standing preferences take precedence in the composed prompt.
// (system-prompt-layers WP04)
func (a *LLMProviderAdapter) buildUserInstructionsBlock() string {
	if a == nil || a.customInstructions == nil {
		return ""
	}
	text := strings.TrimSpace(a.customInstructions())
	if text == "" {
		return ""
	}
	return "## User instructions\n\n" + text
}

// DrainPendingImages returns and clears the buffered GeneratedImagePayload
// values accumulated during the most recent Generate call. The HookPostLLM
// callback invokes this after session_write has produced a stable messageID
// so captured artifacts carry the correct provenance.
// (multimodal-io-extended-01KQ8TD2 WP02)
func (a *LLMProviderAdapter) DrainPendingImages() []artview.GeneratedImagePayload {
	if a == nil {
		return nil
	}
	a.pendingImagesMu.Lock()
	defer a.pendingImagesMu.Unlock()
	out := a.pendingImages
	a.pendingImages = nil
	return out
}

// LastResponse returns the most recently produced llm.Response. Called
// by the HookPostLLM callback registered in buildChatRunner to record
// per-turn usage data once the session_write node has persisted the
// assistant message (token-cost-telemetry WP02).
func (a *LLMProviderAdapter) LastResponse() corellm.Response {
	a.lastRespMu.Lock()
	defer a.lastRespMu.Unlock()
	return a.lastResp
}

// ProviderKind resolves the provider kind string for the active profile
// (e.g. "anthropic", "openai", "bedrock"). Used by the usage hook to
// populate UsageTurn.ProviderKind for token-cost-telemetry alignment
// (backend-context-window-length-01KQ8TD3 WP06). Returns "" when the
// registry is unavailable or the profile cannot be found.
func (a *LLMProviderAdapter) ProviderKind() string {
	if a == nil || a.reg == nil || a.profileID == "" {
		return ""
	}
	prof, err := a.reg.Profile(a.profileID)
	if err != nil {
		return ""
	}
	return prof.Kind
}

// ActiveModelID returns the effective model id for the current turn.
// Prefers modelOverride when set; falls back to the profile's default
// model. Used alongside ProviderKind by the usage hook.
func (a *LLMProviderAdapter) ActiveModelID() string {
	if a == nil {
		return ""
	}
	if a.modelOverride != "" {
		return a.modelOverride
	}
	if a.reg == nil || a.profileID == "" {
		return ""
	}
	prof, err := a.reg.Profile(a.profileID)
	if err != nil || len(prof.AvailableModels()) == 0 {
		return ""
	}
	return prof.AvailableModels()[0]
}

// KernelMessagesToWire translates the kernel-side message slice into the
// content-block shape the registry expects, which is the LAST
// family-agnostic representation before each provider adapter renders
// its own wire format:
//
//	corellm.ContentBlock{Type:"tool_use"}    → Anthropic tool_use block
//	                                         → OpenAI  tool_calls[] entry
//	corellm.ContentBlock{Type:"tool_result"} → Anthropic tool_result block
//	                                         → OpenAI  role:"tool" message
//
// The kernel's seam type is intentionally narrow (Role + Content + Name
// + ToolCalls + ToolCallID + IsError); this maps it onto
// Message{Role, Content: []ContentBlock} and re-attaches assistant
// tool_use blocks when the seam carries any.
//
// Exported (model-moves-transcript-01PMCH01 WP03) so the model-visible
// history composition's per-family goldens can assert the real bytes
// each provider receives instead of a test-local re-implementation of
// this translation. Generate is its production caller, and there is
// exactly one of it: this function is a rename of code that already
// lived inline there, not a second path.
func KernelMessagesToWire(msgs []coreag.Message) []corellm.Message {
	out := make([]corellm.Message, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]corellm.ContentBlock, 0, 1+len(m.ToolCalls))
		// Tool-result message: emit a tool_result block carrying the
		// tool_use_id so the wire encoder can reach it. Without this
		// the second-turn assistant request goes out with a dangling
		// tool_use and no matching tool_call_id, and OpenAI-compat
		// providers (incl. OpenRouter) 5xx the request.
		if m.Role == "tool" {
			content := []byte(m.Content)
			if len(content) == 0 {
				content = []byte(`""`)
			}
			tr := corellm.ToolResult{
				ToolUseID: m.ToolCallID,
				Content:   content,
				IsError:   m.IsError,
			}
			blocks = append(blocks, corellm.ContentBlock{Type: "tool_result", ToolResult: &tr})
			out = append(out, corellm.Message{Role: corellm.Role(m.Role), Content: blocks})
			continue
		}
		if m.Content != "" {
			blocks = append(blocks, corellm.ContentBlock{Type: "text", Text: m.Content})
		}
		for i := range m.ToolCalls {
			tc := m.ToolCalls[i]
			tu := corellm.ToolUse{ID: tc.ID, Name: tc.Name, Input: []byte(tc.Arguments)}
			blocks = append(blocks, corellm.ContentBlock{Type: "tool_use", ToolUse: &tu})
		}
		out = append(out, corellm.Message{Role: corellm.Role(m.Role), Content: blocks})
	}
	return out
}

// Generate satisfies agentgraph.LLMProvider. Translates the kernel
// request → corellm.GenerationRequest, opens a stream, fans events
// into the kernel's StreamSink (pulled from ctx via the kernel-pinned
// helper), and returns the final response.
func (a *LLMProviderAdapter) Generate(ctx context.Context, req coreag.LLMRequest) (coreag.LLMResponse, error) {
	if a == nil || a.reg == nil {
		return coreag.LLMResponse{}, errors.New("chat: nil llm registry adapter")
	}

	llmMsgs := KernelMessagesToWire(req.Messages)

	// Layer the session's resolved attachments, the dynamic environment
	// context, and the user custom instructions on top of the composed
	// graph-base + node-role system prompt (WP01/WP02 populate
	// req.SystemPrompt). Only the seam has the harness runtime context
	// (platform, clock, workspace, live tool catalog), the session's
	// resolved attachments, and the user settings, so the layering
	// happens here rather than graph-side. Order: base → attachments →
	// environment → user instructions, so the user's standing preferences
	// come last and a session's starting context (onboarding's starter
	// prompt, or an ordinary session's attached context) sits right after
	// the graph's generic role framing — establishing who the assistant
	// is for THIS session before the dynamic environment facts and
	// behavioural bars. (system-prompt-layers WP03/WP04;
	// first-run-onboarding-01PMOB01 WP02 added the attachments layer)
	gen := corellm.GenerationRequest{
		ProfileID: a.profileID,
		Model:     a.modelOverride,
		// tmpl is nil: nothing resolves a live ModelProfile on this
		// request path yet (versioned-model-profile-01PMDL04 WP02+ is
		// the (provider, model) -> ModelProfile lookup; per-family-
		// message-shaping-01PMDL06 WP01 only wires the mechanism).
		// composeSystemPrompt's default renderer reproduces today's
		// plain join byte-for-byte, so this is a deliberate, documented
		// gap rather than a half-wire.
		// Recap sits before the user's custom instructions so a user
		// instruction about verbosity still wins the last word.
		System:   composeSystemPrompt(nil, req.SystemPrompt, a.buildAttachmentsBlock(ctx), a.buildEnvBlock(), a.buildRecapBlock(), a.buildAskBarBlock(), a.buildUserInstructionsBlock()),
		Messages: llmMsgs,
		Tools:    a.tools,
	}

	// Per-request tool-catalog visibility: log exactly what's attached to
	// THIS outbound LLM request, at the point of attachment. This is
	// distinct from (and fires more often than) chat_runner.go's
	// "chat.tool_discovery.ok", which logs once per StartStream when the
	// catalog is discovered — a single StartStream can drive several
	// Generate() calls (the tool-call loop), and this line answers "what
	// tools did the model actually see" for each individual request
	// straight from the log, without cross-referencing discovery output
	// against loop iteration counts. One line per request, names only
	// (no argument schemas) to keep it compact.
	toolNames := make([]string, 0, len(gen.Tools))
	for _, t := range gen.Tools {
		toolNames = append(toolNames, t.Name)
	}
	sort.Strings(toolNames)
	logging.L().Info("llm.request.tools",
		"session_id", a.sessionID,
		"count", len(toolNames),
		"tools", toolNames,
	)

	// Carry the per-node sampling knobs already threaded through the
	// kernel seam (agentgraph.LLMRequest.MaxTokens/Temperature, populated
	// from ModelAttrs in exec_compute.go) onto the wire request. Every
	// production adapter reads max_tokens/temperature from
	// GenerationRequest.Params (anthropic.go:434,446; gemini/wire.go:293;
	// azure/adapter.go:381; openaiwire/body.go:51 as the fallback layer
	// under Knobs), so Params is the universal cross-provider channel —
	// not the OpenAI-only Knobs struct. Zero/nil means "no override; let
	// the provider/profile default apply," matching ModelAttrs' documented
	// zero-value semantics. Before this fix Generate() dropped both fields
	// entirely, silently discarding every per-node sampling knob authored
	// in ModelAttrs graph-wide (model-request-path-live-01PMDL01 WP01).
	if req.MaxTokens > 0 || req.Temperature != nil {
		gen.Params = make(map[string]any, 2)
		if req.MaxTokens > 0 {
			gen.Params["max_tokens"] = req.MaxTokens
		}
		if req.Temperature != nil {
			gen.Params["temperature"] = *req.Temperature
		}
	}

	// Widen the knob surface to the rest of core/llm.RequestKnobs plus
	// stop sequences (model-request-path-live-01PMDL01 WP05). Same
	// Params channel as MaxTokens/Temperature above — every adapter reads
	// these keys directly (anthropic.go, gemini/wire.go, azure/adapter.go)
	// or via KnobsToParams' Params-first precedence layer (openaiwire).
	// StopSequences is carried as a typed field (mirrors Reasoning's
	// shape) rather than folded into Params.
	if req.TopP != nil {
		if gen.Params == nil {
			gen.Params = make(map[string]any, 6)
		}
		gen.Params["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		if gen.Params == nil {
			gen.Params = make(map[string]any, 6)
		}
		gen.Params["top_k"] = *req.TopK
	}
	if req.FrequencyPenalty != nil {
		if gen.Params == nil {
			gen.Params = make(map[string]any, 6)
		}
		gen.Params["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		if gen.Params == nil {
			gen.Params = make(map[string]any, 6)
		}
		gen.Params["presence_penalty"] = *req.PresencePenalty
	}
	if req.Seed != nil {
		if gen.Params == nil {
			gen.Params = make(map[string]any, 6)
		}
		gen.Params["seed"] = *req.Seed
	}
	if req.ParallelToolCalls != nil {
		if gen.Params == nil {
			gen.Params = make(map[string]any, 6)
		}
		gen.Params["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if len(req.StopSequences) > 0 {
		gen.StopSequences = req.StopSequences
	}

	// ReasoningBudgetTokens -> GenerationRequest.Reasoning
	// (model-request-path-live-01PMDL01 WP06b). Nil/zero means "no
	// override; reasoning stays off unless the profile/provider default
	// enables it" — mirrors StopSequences' typed-field shape rather than
	// folding into Params, matching how anthropic.go/bedrock already read
	// req.Reasoning (WP06a).
	if req.ReasoningBudgetTokens != nil && *req.ReasoningBudgetTokens > 0 {
		gen.Reasoning = &corellm.ReasoningSpec{
			Enabled:      true,
			BudgetTokens: *req.ReasoningBudgetTokens,
		}
	}

	// WP02 (long-turn-resilience) / WP02 (model-request-path-live-
	// 01PMDL01): wrap the stream open with classified retry-with-backoff,
	// driven by the resolved profile's retry.Policy rather than a
	// hardcoded literal — a bundle author's per-profile Retry config
	// (retry.FromLLM(prof.Retry)) now reaches this layer, not just the
	// registry's own internal RetryMiddleware. Falls back to
	// retry.StreamPolicyFromLLM's defaults (matching DefaultRetryPolicy)
	// when the profile can't be resolved. Pre-stream transient errors
	// (5xx, network blips) are retried with exponential backoff ±jitter.
	// Mid-stream transient errors that have not yet emitted any content
	// are silently retried by the retryableStream wrapper. Non-transient
	// errors (auth, invalid-request, cancelled) propagate immediately.
	var profileRetry *corellm.RetryPolicy
	if prof, profErr := a.reg.Profile(a.profileID); profErr == nil {
		profileRetry = prof.Retry
	}
	retryPolicy := retry.StreamPolicyFromLLM(profileRetry)

	// FallbackChainId routing (model-request-path-live-01PMDL01 WP07,
	// last mile): when the firing node authored a fallback_chain_id, wrap
	// the registry in a fallback.Runner so a failing primary call walks
	// the resolved chain instead of surfacing the error directly. The
	// node-attr override (WithChainIDOverride) is the highest-priority
	// level of the 3-level hierarchy documented on the fallback package;
	// StoreResolver falls through to the bundled defaults when no
	// operator-saved chain matches. When FallbackChainId is empty the
	// streamFn closure is byte-for-byte the pre-WP07 call — no Runner is
	// constructed and behaviour is unchanged.
	streamFn := func() (corellm.Stream, error) {
		return a.reg.Stream(ctx, gen)
	}
	if req.FallbackChainId != "" {
		runner := fallback.NewRunner(a.reg, &fallback.StoreResolver{})
		fbCtx := fallback.WithChainIDOverride(ctx, req.FallbackChainId)
		streamFn = func() (corellm.Stream, error) {
			return runner.Stream(fbCtx, gen)
		}
	}
	stream, err := retry.RetryStream(ctx, retryPolicy, streamFn)
	if err != nil {
		return coreag.LLMResponse{}, fmt.Errorf("chat: registry stream: %w", err)
	}

	// Drain into the kernel-bound StreamSink (which the modelExecutor
	// pinned onto ctx via withStreamSink). When no sink is wired we
	// still drain the events channel so the upstream goroutine doesn't
	// block — only the per-token fan-out goes silent.
	//
	// WP02 (multimodal-io-extended-01KQ8TD2): buffer StreamGeneratedImage
	// events so the HookPostLLM callback can capture them after
	// session_write has produced a stable messageID. Images are not
	// captured inline here — the messageID isn't available until the
	// session_write node fires after Generate returns.
	// model-moves-transcript-01PMCH01 WP02: this request is one move of
	// the chat transcript only when the model node that issued it said
	// so. req.StreamToChat carries that node's own attr; the review
	// gate, the escalation ladder, the fused router and the compaction
	// strategy all reach this same adapter and none of them is the
	// user's assistant turn.
	recordMoves := req.StreamToChat && a.moves.records()

	sink, _ := coreag.StreamSinkFromContext(ctx)
	for ev := range stream.Events() {
		// Open the move BEFORE the first token of the segment reaches
		// the surface — a boundary that arrives after the text cannot
		// separate it from the previous segment, which is the run-on
		// paragraph the mission exists to fix.
		if recordMoves && ev.Kind == corellm.StreamText && ev.Text != "" {
			a.moves.OpenAssistantSegment()
		}
		if ev.Kind == corellm.StreamGeneratedImage && ev.GeneratedImage != nil && a.capturer != nil {
			gi := ev.GeneratedImage
			a.pendingImagesMu.Lock()
			a.pendingImages = append(a.pendingImages, artview.GeneratedImagePayload{
				Data:          gi.Data,
				URL:           gi.URL,
				MimeType:      gi.MimeType,
				RevisedPrompt: gi.RevisedPrompt,
				Index:         gi.Index,
			})
			a.pendingImagesMu.Unlock()
		}
		if sink == nil {
			continue
		}
		sink.Emit(translateLLMStreamEvent(ev))
	}

	resp, ferr := stream.Final()
	if ferr != nil {
		// On error the kernel surfaces the error up the run; the
		// runner's terminal goroutine is responsible for emitting the
		// stream-closed payload with reason=backend-error.
		return coreag.LLMResponse{}, fmt.Errorf("chat: stream final: %w", ferr)
	}

	// Store the response for the HookPostLLM callback (token-cost-
	// telemetry WP02). The session_write node fires after Generate
	// returns and reads LastResponse() inside its PostHook to record
	// usage against the freshly-persisted messageID.
	a.lastRespMu.Lock()
	a.lastResp = resp
	a.lastRespMu.Unlock()

	out := coreag.LLMResponse{
		Content:      flattenContent(resp.Content),
		FinishReason: resp.FinishReason,
		TokensUsed:   resp.Usage.InputTokens + resp.Usage.OutputTokens,
		CostUSD:      resp.Cost.Total,
	}
	if len(resp.ToolCalls) > 0 {
		calls := make([]coreag.ToolCallRequest, 0, len(resp.ToolCalls))
		for i := range resp.ToolCalls {
			tu := resp.ToolCalls[i]
			calls = append(calls, coreag.ToolCallRequest{
				ID:        tu.ID,
				Name:      tu.Name,
				Arguments: string(tu.Input),
			})
		}
		out.ToolCalls = calls
	}
	// One model fire = one move. Park its text; the journal decides
	// whether it becomes an assistant_move or, if nothing follows it,
	// the turn's `final` (model-moves-transcript-01PMCH01 WP02).
	if recordMoves {
		a.moves.RecordAssistantMove(ctx, out.Content)
	}
	return out, nil
}

// flattenContent returns the joined text of every text-typed block in
// declaration order. The kernel's LLMResponse.Content is a single
// string; native ContentBlock slices live on the corellm path only.
func flattenContent(blocks []corellm.ContentBlock) string {
	var sb strings.Builder
	for i, b := range blocks {
		if b.Type != "" && b.Type != "text" {
			continue
		}
		if b.Text == "" {
			continue
		}
		if i > 0 && sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(b.Text)
	}
	return sb.String()
}

// translateLLMStreamEvent maps a corellm.StreamEvent → the kernel's
// agentgraph.StreamEvent shape. Inverse of translateAGStreamEvent in
// stream_bridge.go.
func translateLLMStreamEvent(ev corellm.StreamEvent) coreag.StreamEvent {
	out := coreag.StreamEvent{
		Text:   ev.Text,
		Finish: ev.Finish,
		ErrMsg: ev.Err,
	}
	if ev.Tool != nil {
		out.ToolID = ev.Tool.ID
		out.ToolName = ev.Tool.Name
		out.ToolArgs = string(ev.Tool.Input)
	}
	if ev.Reasoning != nil {
		out.Reasoning = ev.Reasoning.Content
	}
	if ev.Usage != nil {
		out.UsageInputTokens = ev.Usage.InputTokens
		out.UsageOutputTokens = ev.Usage.OutputTokens
		out.UsageReasoningTokens = ev.Usage.ReasoningTokens
	}
	switch ev.Kind {
	case corellm.StreamText:
		out.Kind = coreag.StreamEventText
	case corellm.StreamTool:
		out.Kind = coreag.StreamEventTool
	case corellm.StreamReasoning:
		out.Kind = coreag.StreamEventReasoning
	case corellm.StreamUsage:
		out.Kind = coreag.StreamEventUsage
	case corellm.StreamFinish:
		out.Kind = coreag.StreamEventFinish
	case corellm.StreamError:
		out.Kind = coreag.StreamEventError
	default:
		out.Kind = coreag.StreamEventKind(string(ev.Kind))
	}
	return out
}
