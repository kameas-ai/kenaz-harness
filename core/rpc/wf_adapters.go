package rpc

// wf_adapters.go — thin adapters that bridge the production MCP pool and
// LLM registry onto the narrow interfaces the workflow runner expects.
//
// Keeping the adapters in a separate file avoids polluting api.go with
// import-heavy struct bodies while satisfying DIRECTIVE_001: no core/
// package imports anything from the rpc layer.
//
// Adapter shapes:
//
//	wfMCPCallerAdapter       — coremcp.Pool (json.RawMessage args) →
//	                           corewf.MCPCaller (map[string]any args).
//	                           Wraps "unknown server" errors with an
//	                           actionable user message (FR-004).
//
//	wfLLMStreamerAdapter     — corellm.Registry (GenerationRequest/Stream) →
//	                           corewf.LLMStreamer (LLMRequest/LLMStream).
//	                           Translates text-delta events and captures
//	                           tool-use blocks for the bounded loop (FR-003).
//
//	wfToolDiscovererAdapter  — corellm.ToolDiscoverer →
//	                           corewf.ToolDiscoverer.
//	                           Reuses the SAME discoverer chat uses so
//	                           one catalog + one permission filter serve
//	                           both surfaces (FR-002).
//
//	wfToolDispatcherAdapter  — toolloop.MCPPool.Call →
//	                           corewf.ToolDispatcher.
//	                           Dispatches through the same BuiltinPool
//	                           chat's tool loop uses (one Cedar path).
//
//	wfNotifierAdapter        — satisfies corewf.Notifier via the Wails
//	                           runtime notification call.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// ─── MCP adapter ──────────────────────────────────────────────────────────────

// wfMCPCallerAdapter bridges coremcp.Pool onto corewf.MCPCaller.
//
// The production pool's Call signature expects json.RawMessage; the
// workflow runner hands us map[string]any. We marshal the map before
// dispatching and unmarshal the raw JSON result into a plain string.
//
// Actionable errors (FR-004 / WP03):
//   - When the server name is not found in the pool, the caller sees
//     "MCP server <name> is not installed — install it from Tools"
//     rather than the internal "stdio: unknown server" message.
//   - All other errors propagate unchanged.
type wfMCPCallerAdapter struct {
	pool coremcp.Pool
}

func (a *wfMCPCallerAdapter) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	if a.pool == nil {
		return "", fmt.Errorf("MCP server %q is not available — MCP is disabled. Enable it in Settings → Tools", server)
	}
	// Marshal args to json.RawMessage (nil args → null).
	var rawArgs json.RawMessage
	if len(args) > 0 {
		var merr error
		rawArgs, merr = json.Marshal(args)
		if merr != nil {
			return "", fmt.Errorf("mcp_call: marshal args for %s.%s: %w", server, tool, merr)
		}
	}
	raw, err := a.pool.Call(ctx, server, tool, rawArgs)
	if err != nil {
		return "", translateMCPError(server, err)
	}
	// Unwrap the json.RawMessage to a plain string the step output field holds.
	// If the result is a JSON string, unquote it; otherwise return the raw JSON.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	return string(raw), nil
}

// translateMCPError converts low-level pool errors into user-facing messages.
func translateMCPError(server string, err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "unknown server") ||
		strings.Contains(low, "not in pool") ||
		strings.Contains(low, "server not found") {
		return fmt.Errorf("MCP server %q is not installed or not authorized — install it from Tools", server)
	}
	return err
}

// ─── LLM adapter ──────────────────────────────────────────────────────────────

// wfLLMStreamerAdapter bridges corellm.Registry onto corewf.LLMStreamer.
//
// When req.Tools is non-empty the adapter projects the tool specs into
// the GenerationRequest and returns a wfLLMStreamBridge that implements
// the optional corewf.ToolCallStream interface so the bounded loop can
// retrieve tool-use calls after the stream drains (FR-002/FR-003).
//
// When req.History is non-empty (multi-turn tool loop), the adapter
// reconstructs the full conversation message sequence.
//
// Prompt building: the workflow runner supplies a single user_prompt
// string; on the first turn it is wrapped in a user-turn message.
// Subsequent turns (req.Prompt == "") carry only history.
type wfLLMStreamerAdapter struct {
	reg corellm.Registry
}

func (a *wfLLMStreamerAdapter) Stream(ctx context.Context, req corewf.LLMRequest) (corewf.LLMStream, error) {
	if a.reg == nil {
		return nil, fmt.Errorf("workflows: LLM registry not wired")
	}

	// Build the messages slice.
	messages, err := buildMessages(req)
	if err != nil {
		return nil, fmt.Errorf("workflows: build messages: %w", err)
	}

	// Project corewf.ToolSpec → corellm.ToolSpec when tools are enabled.
	var llmTools []corellm.ToolSpec
	for _, t := range req.Tools {
		llmTools = append(llmTools, corellm.ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(t.InputSchema),
		})
	}

	inner, err := a.reg.Stream(ctx, corellm.GenerationRequest{
		ProfileID: req.ProfileID,
		Model:     req.Model,
		Messages:  messages,
		Tools:     llmTools,
	})
	if err != nil {
		return nil, err
	}
	return &wfLLMStreamBridge{inner: inner}, nil
}

// buildMessages constructs the corellm.Message slice from req.Prompt
// and req.History. History carries the prior turns of the tool loop.
func buildMessages(req corewf.LLMRequest) ([]corellm.Message, error) {
	var messages []corellm.Message

	// First turn: user prompt only.
	if len(req.History) == 0 {
		if req.Prompt != "" {
			messages = append(messages, corellm.Message{
				Role:    "user",
				Content: []corellm.ContentBlock{{Type: "text", Text: req.Prompt}},
			})
		}
		return messages, nil
	}

	// Multi-turn: seed with the original user prompt from history iteration 0.
	// The very first HistoryMessage from the model may not include the prompt —
	// we prepend it as the opening user turn.
	if req.Prompt != "" {
		messages = append(messages, corellm.Message{
			Role:    "user",
			Content: []corellm.ContentBlock{{Type: "text", Text: req.Prompt}},
		})
	} else if len(req.History) > 0 {
		// Reconstruct messages from history only (subsequent loop iterations).
		// History has alternating user-seed / assistant / tool-result blocks.
		// The caller sets Prompt="" on the second+ iteration and sends history.
		//
		// Reconstruct the opening user turn by scanning history for the first
		// user-role block (the original prompt is already in history[0] via the
		// first assistant turn's context — but we store user prompt separately).
		// On second+ iterations the history already starts with the assistant's
		// prior turn; skip adding an extra user message.
	}

	for _, h := range req.History {
		switch h.Role {
		case "user":
			messages = append(messages, corellm.Message{
				Role:    "user",
				Content: []corellm.ContentBlock{{Type: "text", Text: h.Text}},
			})
		case "assistant":
			var blocks []corellm.ContentBlock
			if h.Text != "" {
				blocks = append(blocks, corellm.ContentBlock{Type: "text", Text: h.Text})
			}
			for _, tu := range h.ToolUses {
				blocks = append(blocks, corellm.ContentBlock{
					Type: "tool_use",
					ToolUse: &corellm.ToolUse{
						ID:    tu.ID,
						Name:  tu.Name,
						Input: json.RawMessage(tu.Input),
					},
				})
			}
			messages = append(messages, corellm.Message{Role: "user", Content: blocks})
		case "tool":
			// Tool results: one block per result, packed into a user-role message
			// (Anthropic's convention: tool results arrive as a user turn).
			var blocks []corellm.ContentBlock
			for _, tr := range h.ToolResults {
				blocks = append(blocks, corellm.ContentBlock{
					Type: "tool_result",
					ToolResult: &corellm.ToolResult{
						ToolUseID: tr.ToolUseID,
						Content:   json.RawMessage(`"` + escapeJSONString(tr.Content) + `"`),
						IsError:   tr.IsError,
					},
				})
			}
			messages = append(messages, corellm.Message{Role: "user", Content: blocks})
		}
	}

	return messages, nil
}

// escapeJSONString escapes a plain string for embedding in a JSON string
// literal. Only handles the common cases (backslash, quote, newline, tab).
func escapeJSONString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// wfLLMStreamBridge narrows corellm.Stream to corewf.LLMStream and
// optionally corewf.ToolCallStream.
//
// It spawns a goroutine that reads corellm.StreamEvent values:
//   - Text events are forwarded to the outbound channel.
//   - Tool events are captured in toolCalls for retrieval via ToolCalls().
//   - All other events are silently dropped.
//
// The bridge implements corewf.ToolCallStream so the bounded tool loop
// can retrieve tool invocations after the Events channel closes (FR-003).
type wfLLMStreamBridge struct {
	inner     corellm.Stream
	once      sync.Once
	ch        chan corewf.LLMStreamEvent
	mu        sync.Mutex
	toolCalls []corewf.ToolUseCall
}

func (b *wfLLMStreamBridge) Events() <-chan corewf.LLMStreamEvent {
	b.once.Do(func() {
		b.ch = make(chan corewf.LLMStreamEvent, 64)
		go func() {
			defer close(b.ch)
			for ev := range b.inner.Events() {
				switch {
				case ev.Err != "":
					b.ch <- corewf.LLMStreamEvent{Err: ev.Err}
					return
				case ev.Kind == corellm.StreamTool && ev.Tool != nil:
					// Capture tool-use call; don't forward to text channel.
					b.mu.Lock()
					b.toolCalls = append(b.toolCalls, corewf.ToolUseCall{
						ID:    ev.Tool.ID,
						Name:  ev.Tool.Name,
						Input: []byte(ev.Tool.Input),
					})
					b.mu.Unlock()
				case ev.Text != "":
					b.ch <- corewf.LLMStreamEvent{Text: ev.Text}
				}
			}
		}()
	})
	return b.ch
}

func (b *wfLLMStreamBridge) Final() (string, error) {
	resp, err := b.inner.Final()
	if err != nil {
		return "", err
	}
	// Capture any tool_use blocks from the final response that weren't
	// surfaced as streaming events (some adapters batch them in Final).
	b.mu.Lock()
	for _, block := range resp.Content {
		if block.Type == "tool_use" && block.ToolUse != nil {
			// Deduplicate by ID to avoid double-counting streaming + final.
			alreadyCaptured := false
			for _, existing := range b.toolCalls {
				if existing.ID == block.ToolUse.ID {
					alreadyCaptured = true
					break
				}
			}
			if !alreadyCaptured {
				b.toolCalls = append(b.toolCalls, corewf.ToolUseCall{
					ID:    block.ToolUse.ID,
					Name:  block.ToolUse.Name,
					Input: []byte(block.ToolUse.Input),
				})
			}
		}
	}
	b.mu.Unlock()

	// Aggregate text content blocks from the response.
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}

// ToolCalls implements corewf.ToolCallStream. Must be called after
// the Events channel closes (i.e. after Final() has been called).
func (b *wfLLMStreamBridge) ToolCalls() []corewf.ToolUseCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.toolCalls) == 0 {
		return nil
	}
	out := make([]corewf.ToolUseCall, len(b.toolCalls))
	copy(out, b.toolCalls)
	return out
}

// Verify wfLLMStreamBridge satisfies both interfaces.
var _ corewf.LLMStream = (*wfLLMStreamBridge)(nil)
var _ corewf.ToolCallStream = (*wfLLMStreamBridge)(nil)

// ─── Tool discoverer adapter ───────────────────────────────────────────────────

// wfToolDiscovererAdapter bridges corellm.ToolDiscoverer onto
// corewf.ToolDiscoverer. It reuses the SAME discoverer the chat surface
// uses so model_turn steps share one catalog and one permission filter
// with chat — no new egress bypass (FR-002).
//
// The sessionID forwarded to the underlying discoverer is empty because
// workflow engine steps have no chat session context; the permission
// resolver falls back to the global policy in that case.
type wfToolDiscovererAdapter struct {
	inner corellm.ToolDiscoverer
}

func (a *wfToolDiscovererAdapter) Discover(ctx context.Context) ([]corewf.ToolSpec, error) {
	if a.inner == nil {
		return nil, nil
	}
	// Pass empty sessionID — workflows have no chat session.
	specs, err := a.inner.Tools(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]corewf.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, corewf.ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: []byte(s.InputSchema),
		})
	}
	return out, nil
}

// ─── Tool dispatcher adapter ───────────────────────────────────────────────────

// wfToolDispatcherAdapter bridges toolloop.MCPPool onto
// corewf.ToolDispatcher. It dispatches through the same BuiltinPool
// the chat tool loop uses so the same Cedar/permission path applies —
// no new egress bypass (FR-003).
type wfToolDispatcherAdapter struct {
	pool toolloop.MCPPool
}

func (a *wfToolDispatcherAdapter) Dispatch(ctx context.Context, name string, input []byte) (string, bool, error) {
	if a.pool == nil {
		return "", true, fmt.Errorf("tool dispatcher not wired (no MCP pool)")
	}
	// Split the namespaced "server__tool" name back into (server, tool).
	server, tool := splitToolName(name)
	raw, err := a.pool.Call(ctx, server, tool, json.RawMessage(input))
	if err != nil {
		return "", true, err
	}
	// Unwrap JSON string result to plain text.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false, nil
	}
	return string(raw), false, nil
}

// splitToolName splits "server__tool" into ("server", "tool").
// Falls back to ("", name) when no separator is found.
func splitToolName(name string) (server, tool string) {
	const sep = "__"
	idx := strings.Index(name, sep)
	if idx < 0 {
		return "", name
	}
	return name[:idx], name[idx+len(sep):]
}
