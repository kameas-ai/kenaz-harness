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
//   wfMCPCallerAdapter  — coremcp.Pool (json.RawMessage args) →
//                         corewf.MCPCaller (map[string]any args).
//                         Wraps "unknown server" errors with an
//                         actionable user message (FR-004).
//
//   wfLLMStreamerAdapter — corellm.Registry (GenerationRequest/Stream) →
//                         corewf.LLMStreamer (LLMRequest/LLMStream).
//                         Translates only the text-delta events model_turn
//                         needs; tool-use / image events are discarded.
//
//   wfNotifierAdapter   — satisfies corewf.Notifier via the Wails runtime
//                         notification call. Wired in as Deps.Notifier so
//                         notify steps can surface OS-level toasts.
//                         (The Wails runtime Notify path is handled by the
//                         chassis' existing os.Notify binding; we adapt
//                         here only for the workflow Notifier interface.)

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// ─── MCP adapter ─────────────────────────────────────────────────────────────

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

// ─── LLM adapter ─────────────────────────────────────────────────────────────

// wfLLMStreamerAdapter bridges corellm.Registry onto corewf.LLMStreamer.
//
// The registry's Stream method returns a rich corellm.Stream (with tool-use,
// reasoning, image events etc.); the workflow model_turn runner only needs
// text-delta events and the final aggregated text. This adapter filters to
// text-only and wraps the stream in the narrow LLMStream interface.
//
// Prompt building: the workflow runner supplies a single user_prompt string;
// the registry expects a []Message slice. We construct a minimal user-turn
// message here, mirroring the autotitle wiring approach.
type wfLLMStreamerAdapter struct {
	reg corellm.Registry
}

func (a *wfLLMStreamerAdapter) Stream(ctx context.Context, req corewf.LLMRequest) (corewf.LLMStream, error) {
	if a.reg == nil {
		return nil, fmt.Errorf("workflows: LLM registry not wired")
	}
	inner, err := a.reg.Stream(ctx, corellm.GenerationRequest{
		ProfileID: req.ProfileID,
		Model:     req.Model,
		Messages: []corellm.Message{
			{Role: "user", Content: []corellm.ContentBlock{{Type: "text", Text: req.Prompt}}},
		},
	})
	if err != nil {
		return nil, err
	}
	return &wfLLMStreamBridge{inner: inner}, nil
}

// wfLLMStreamBridge narrows corellm.Stream to corewf.LLMStream.
// It spawns a goroutine that reads corellm.StreamEvent values and pushes only
// text-delta events into the outbound channel. Non-text events (tool-use,
// reasoning, etc.) are discarded so the model_turn runner sees a clean text
// stream without needing to know the richer event taxonomy.
type wfLLMStreamBridge struct {
	inner corellm.Stream
	ch    chan corewf.LLMStreamEvent
}

func (b *wfLLMStreamBridge) Events() <-chan corewf.LLMStreamEvent {
	if b.ch != nil {
		return b.ch
	}
	b.ch = make(chan corewf.LLMStreamEvent, 64)
	go func() {
		defer close(b.ch)
		for ev := range b.inner.Events() {
			if ev.Err != "" {
				b.ch <- corewf.LLMStreamEvent{Err: ev.Err}
				return
			}
			if ev.Text != "" {
				b.ch <- corewf.LLMStreamEvent{Text: ev.Text}
			}
		}
	}()
	return b.ch
}

func (b *wfLLMStreamBridge) Final() (string, error) {
	resp, err := b.inner.Final()
	if err != nil {
		return "", err
	}
	// Aggregate text content blocks from the response.
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}
