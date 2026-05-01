package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
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
}

// NewLLMProviderAdapter constructs an adapter pinned to a specific
// (profileID, modelOverride, toolCatalog) tuple. One adapter per chat
// run; the runner constructs it inside StartStream.
func NewLLMProviderAdapter(reg corellm.Registry, profileID, modelOverride string, tools []corellm.ToolSpec) *LLMProviderAdapter {
	return &LLMProviderAdapter{
		reg:           reg,
		profileID:     profileID,
		modelOverride: modelOverride,
		tools:         tools,
	}
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

	gen := corellm.GenerationRequest{
		ProfileID: a.profileID,
		Model:     a.modelOverride,
		System:    req.SystemPrompt,
		Messages:  llmMsgs,
		Tools:     a.tools,
	}

	stream, err := a.reg.Stream(ctx, gen)
	if err != nil {
		return coreag.LLMResponse{}, fmt.Errorf("chat: registry stream: %w", err)
	}

	// Drain into the kernel-bound StreamSink (which the modelExecutor
	// pinned onto ctx via withStreamSink). When no sink is wired we
	// still drain the events channel so the upstream goroutine doesn't
	// block — only the per-token fan-out goes silent.
	sink, _ := coreag.StreamSinkFromContext(ctx)
	for ev := range stream.Events() {
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
