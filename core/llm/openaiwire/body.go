package openaiwire

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/structured"
)

// BuildRequestBody constructs the JSON body for the Chat Completions API.
//
// The caller provides the resolved model name and the merged profile
// defaults; knobs from req.Knobs override profile defaults where both
// are set.
//
// Caller-specific overrides (ranking headers, vendor params) should be
// applied AFTER calling this function by mutating the returned map
// before marshalling.
//
// Wire shape produced:
//
//	{
//	  "model": "<model>",
//	  "stream": true,
//	  "stream_options": {"include_usage": true},
//	  "messages": [...],
//	  "tools": [...],           // optional
//	  "response_format": {...}, // optional
//	  "seed": <n>,              // optional, from Knobs.Seed
//	  "reasoning_effort": "...",// optional, from Knobs.Reasoning.OpenAIEffort
//	  "parallel_tool_calls": <bool>, // optional, from Knobs.ParallelToolCalls
//	  ...sampling knobs...
//	}
func BuildRequestBody(req llm.GenerationRequest, model string, profileDefaults map[string]any) (map[string]any, error) {
	out := map[string]any{
		"model":          model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}

	// Apply profile defaults first (lowest precedence).
	for _, key := range []string{"temperature", "top_p", "max_tokens", "presence_penalty", "frequency_penalty", "seed", "parallel_tool_calls", "stop"} {
		if v, ok := profileDefaults[key]; ok {
			out[key] = v
		}
	}

	// Apply Params (middle precedence). Params is the universal
	// cross-provider channel (model-request-path-live-01PMDL01 WP01/WP05);
	// seed/parallel_tool_calls/stop are included here so a ModelAttrs-driven
	// request that never touches Knobs still reaches the wire. top_k is
	// deliberately excluded — OpenAI Chat Completions has no top_k
	// parameter (KnobsToParams omits it for the same reason).
	for _, key := range []string{"temperature", "top_p", "max_tokens", "presence_penalty", "frequency_penalty", "seed", "parallel_tool_calls", "stop"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		}
	}

	// req.StopSequences (typed field, WP05) sits above Params but below
	// Knobs, mirroring the Reasoning field's precedence.
	if len(req.StopSequences) > 0 {
		out["stop"] = req.StopSequences
	}

	// Apply Knobs (highest precedence).
	reasoningEffortFromKnobs := false
	if k := req.Knobs; k != nil {
		for key, val := range KnobsToParams(k) {
			out[key] = val
		}
		if k.ParallelToolCalls != nil {
			out["parallel_tool_calls"] = *k.ParallelToolCalls
		}
		if k.Reasoning != nil && k.Reasoning.OpenAIEffort != "" {
			out["reasoning_effort"] = k.Reasoning.OpenAIEffort
			reasoningEffortFromKnobs = true
		}
	}

	// req.Reasoning (typed field, model-request-path-live-01PMDL01 WP05)
	// maps to reasoning_effort when Knobs did not already set it (D-4:
	// Knobs keep precedence). Before this, only Knobs.Reasoning.OpenAIEffort
	// reached the wire here — but the only writer of Knobs.Reasoning is
	// /effort (core/slashcmd/cmd_effort.go:61), which spec.md §1.6(a)
	// records as broken, and req.Reasoning is what
	// llm_provider_adapter.go:635 actually sets from the live
	// ReasoningControl UI. Without this branch, the OpenAI, OpenRouter and
	// custom-openai request paths silently dropped the user's reasoning
	// budget (model-settings-reach-the-model-01PMZ101 WP08, spec FR-007).
	//
	// mapReasoningEffort is reused, not reimplemented — moved here from
	// azure/adapter.go (model-settings-reach-the-model-01PMZ101 WP03+WP08,
	// spec D-14): azure previously mapped req.Reasoning to reasoning_effort
	// itself, and WP03 routing azure through this shared encoder would have
	// silently turned that off with no way back on (llm.Request.Knobs has
	// no production writer) had this branch not moved with it.
	if !reasoningEffortFromKnobs && req.Reasoning != nil && req.Reasoning.Enabled {
		out["reasoning_effort"] = mapReasoningEffort(req.Reasoning.BudgetTokens)
	}

	// ResponseFormat / JSONMode (ResponseFormat takes precedence).
	if req.ResponseFormat != nil {
		if err := applyResponseFormat(req.ResponseFormat, out); err != nil {
			return nil, err
		}
	} else if req.JSONMode != nil && req.JSONMode.Enabled {
		if err := applyJSONMode(req.JSONMode, out); err != nil {
			return nil, err
		}
	}

	// Messages.
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = string(llm.RoleUser)
		}

		// Tool-result blocks each become their own OpenAI "tool" message
		// carrying the tool_call_id that pairs the result to its parent tool
		// call. Omitting tool_call_id makes OpenAI-compatible providers reject
		// the turn (e.g. Moonshot: "tool_call_id is not found", 400).
		emittedToolResult := false
		for _, blk := range m.Content {
			if blk.Type == "tool_result" && blk.ToolResult != nil {
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": blk.ToolResult.ToolUseID,
					"content":      toolResultContent(blk.ToolResult),
				})
				emittedToolResult = true
			}
		}
		if emittedToolResult {
			continue
		}

		// Assistant turns that emitted tool calls carry a tool_calls array;
		// without it the next turn's tool_call_id has no parent.
		var toolCalls []map[string]any
		for _, blk := range m.Content {
			if blk.Type == "tool_use" && blk.ToolUse != nil {
				args := string(blk.ToolUse.Input)
				if args == "" {
					args = "{}"
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   blk.ToolUse.ID,
					"type": "function",
					"function": map[string]any{
						"name":      blk.ToolUse.Name,
						"arguments": args,
					},
				})
			}
		}

		entry := map[string]any{"role": role}
		if len(toolCalls) > 0 {
			entry["tool_calls"] = toolCalls
			// OpenAI accepts null content alongside tool_calls; include any
			// text the assistant emitted with the calls.
			if txt := flattenTextContent(m.Content); txt != "" {
				entry["content"] = txt
			} else {
				entry["content"] = nil
			}
		} else {
			entry["content"] = BuildOpenAIContent(m.Content)
		}
		msgs = append(msgs, entry)
	}
	out["messages"] = msgs

	// Tools.
	if len(req.Tools) > 0 {
		tools, err := SerializeTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}

	return out, nil
}

// mapReasoningEffort maps a token budget to a discrete reasoning_effort
// string: low (<4000), medium (4000-14999), high (15000+), defaulting to
// high when budgetTokens is 0 (unset). Moved from azure/adapter.go
// (model-settings-reach-the-model-01PMZ101 WP08, spec D-14) — do not
// write a second copy for another OpenAI-wire adapter; call this one.
func mapReasoningEffort(budgetTokens int) string {
	switch {
	case budgetTokens == 0 || budgetTokens >= 15000:
		return "high"
	case budgetTokens >= 4000:
		return "medium"
	default:
		return "low"
	}
}

// BuildOpenAIContent emits the JSON value for a message's `content`
// field. Pure-text messages stay as a single string; messages that carry
// image blocks switch to the array-of-parts shape.
func BuildOpenAIContent(parts []llm.ContentBlock) any {
	if hasImageBlock(parts) {
		return buildMultipartContent(parts)
	}
	return flattenTextContent(parts)
}

// toolResultContent renders a tool result's payload for the OpenAI "tool"
// message content. ToolResult.Content is JSON-encoded; unwrap a plain JSON
// string so the model sees raw text rather than an escaped, quoted blob.
func toolResultContent(tr *llm.ToolResult) string {
	content := string(tr.Content)
	if len(content) >= 2 && content[0] == '"' && content[len(content)-1] == '"' {
		var unq string
		if err := json.Unmarshal(tr.Content, &unq); err == nil {
			content = unq
		}
	}
	return content
}

func flattenTextContent(parts []llm.ContentBlock) string {
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "", "text":
			if p.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		default:
			if p.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

func buildMultipartContent(parts []llm.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "", "text":
			if p.Text == "" {
				continue
			}
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		case "image":
			if p.Source == nil {
				continue
			}
			out = append(out, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": dataURL(p.Source)},
			})
		case "document":
			// Gate-rejected pre-flight for OpenAI Chat Completions;
			// defensive drop here so the text portion survives.
			continue
		default:
			if p.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": p.Text})
			}
		}
	}
	return out
}

func hasImageBlock(parts []llm.ContentBlock) bool {
	for _, p := range parts {
		if p.Type == "image" {
			return true
		}
	}
	return false
}

func dataURL(src *llm.MediaSource) string {
	if src.URI != "" && src.Kind == "uri" {
		return src.URI
	}
	return "data:" + src.MediaType + ";base64," + src.Data
}

// SerializeTools converts the harness ToolSpec slice into the
// OpenAI tools wire shape.
func SerializeTools(specs []llm.ToolSpec) ([]map[string]any, error) {
	tools := make([]map[string]any, 0, len(specs))
	for _, t := range specs {
		fn := map[string]any{
			"name":        t.Name,
			"description": t.Description,
		}
		if len(t.InputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("openaiwire: tool %q parameters: %w", t.Name, err)
			}
			fn["parameters"] = schema
		} else {
			fn["parameters"] = map[string]any{"type": "object"}
		}
		tools = append(tools, map[string]any{
			"type":     "function",
			"function": fn,
		})
	}
	return tools, nil
}

// applyResponseFormat translates a ResponseFormat into the OpenAI wire
// shape. Modifies body in place.
func applyResponseFormat(rf *llm.ResponseFormat, body map[string]any) error {
	switch rf.Mode {
	case "json":
		body["response_format"] = map[string]any{"type": "json_object"}
	case "json_schema":
		schema := rf.Schema
		if len(schema) > 0 {
			injected, err := structured.InjectAdditionalProperties(schema)
			if err == nil {
				schema = injected
			}
		}
		var schemaVal any
		if len(schema) > 0 {
			if err := json.Unmarshal(schema, &schemaVal); err != nil {
				return fmt.Errorf("openaiwire: response_format schema parse: %w", err)
			}
		}
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": schemaVal,
				"strict": true,
			},
		}
	case "grammar":
		return &llm.ErrUnsupportedFormat{Provider: "openai-wire", Model: "", Mode: rf.Mode}
	}
	return nil
}

// applyJSONMode translates a JSONModeSpec into the OpenAI wire shape.
func applyJSONMode(jm *llm.JSONModeSpec, body map[string]any) error {
	if jm == nil || !jm.Enabled {
		return nil
	}
	if len(jm.Schema) == 0 {
		body["response_format"] = map[string]any{"type": "json_object"}
		return nil
	}
	schema := jm.Schema
	injected, err := structured.InjectAdditionalProperties(schema)
	if err == nil {
		schema = injected
	}
	var schemaVal any
	if err := json.Unmarshal(schema, &schemaVal); err != nil {
		return fmt.Errorf("openaiwire: json_mode schema parse: %w", err)
	}
	name := jm.Name
	if name == "" {
		name = "response"
	}
	body["response_format"] = map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   name,
			"schema": schemaVal,
			"strict": jm.Strict,
		},
	}
	return nil
}
