package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/retry"
	artview "github.com/kameas-ai/kenaz-harness/core/rpc/views/artifacts"
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
	// customInstructions returns the user's chat custom-instructions text,
	// evaluated once per Generate so a Settings edit takes effect on the
	// next turn (system-prompt-layers WP04). nil / empty appends no user
	// layer.
	customInstructions func() string
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

// WithSessionID pins the session id onto the adapter so the
// generated-image capture path can record provenance without
// re-plumbing sessionID through every Generate call. Called by the
// chat runner immediately after construction.
func (a *LLMProviderAdapter) WithSessionID(sessionID string) *LLMProviderAdapter {
	a.sessionID = sessionID
	return a
}

// WithEnvContext pins the environment-context inputs (injected clock +
// agent-workspace path) onto the adapter so Generate can render the
// dynamic environment layer that stacks on top of the composed graph +
// node-role system prompt. A nil clock falls back to time.Now; an empty
// workspaceDir renders a generic sandboxed-workspace note.
// (system-prompt-layers WP03)
func (a *LLMProviderAdapter) WithEnvContext(now func() time.Time, workspaceDir string) *LLMProviderAdapter {
	a.now = now
	a.workspaceDir = workspaceDir
	return a
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
		if entries, err := os.ReadDir(ws); err == nil {
			in.WorkspaceEntries = len(entries)
			in.WorkspaceCounted = true
		}
	}
	return buildEnvContext(in)
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

// Generate satisfies agentgraph.LLMProvider. Translates the kernel
// request → corellm.GenerationRequest, opens a stream, fans events
// into the kernel's StreamSink (pulled from ctx via the kernel-pinned
// helper), and returns the final response.
func (a *LLMProviderAdapter) Generate(ctx context.Context, req coreag.LLMRequest) (coreag.LLMResponse, error) {
	if a == nil || a.reg == nil {
		return coreag.LLMResponse{}, errors.New("chat: nil llm registry adapter")
	}

	// Translate the kernel-side message slice into the wire shape the
	// registry expects. The kernel's seam type is intentionally narrow
	// (Role + Content + Name + ToolCalls); we map onto Message{Role,
	// Content: []ContentBlock} and re-attach assistant tool_use blocks
	// when the kernel's seam carries any.
	llmMsgs := make([]corellm.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
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
			}
			blocks = append(blocks, corellm.ContentBlock{Type: "tool_result", ToolResult: &tr})
			llmMsgs = append(llmMsgs, corellm.Message{Role: corellm.Role(m.Role), Content: blocks})
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
		llmMsgs = append(llmMsgs, corellm.Message{Role: corellm.Role(m.Role), Content: blocks})
	}

	// Layer the dynamic environment context and the user custom
	// instructions on top of the composed graph-base + node-role system
	// prompt (WP01/WP02 populate req.SystemPrompt). Only the seam has the
	// harness runtime context (platform, clock, workspace, live tool
	// catalog) and the user settings, so the layering happens here rather
	// than graph-side. Order: base → environment → user instructions, so
	// the user's standing preferences come last. (system-prompt-layers
	// WP03/WP04)
	gen := corellm.GenerationRequest{
		ProfileID: a.profileID,
		Model:     a.modelOverride,
		System:    composeSystemPrompt(req.SystemPrompt, a.buildEnvBlock(), a.buildUserInstructionsBlock()),
		Messages:  llmMsgs,
		Tools:     a.tools,
	}

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
	stream, err := retry.RetryStream(ctx, retryPolicy, func() (corellm.Stream, error) {
		return a.reg.Stream(ctx, gen)
	})
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
	sink, _ := coreag.StreamSinkFromContext(ctx)
	for ev := range stream.Events() {
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
