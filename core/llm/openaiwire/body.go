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
	for _, key := range []string{"temperature", "top_p", "max_tokens", "presence_penalty", "frequency_penalty"} {
		if v, ok := profileDefaults[key]; ok {
			out[key] = v
		}
	}

	// Apply Params (middle precedence).
	for _, key := range []string{"temperature", "top_p", "max_tokens", "presence_penalty", "frequency_penalty"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		}
	}

	// Apply Knobs (highest precedence).
	if k := req.Knobs; k != nil {
		for key, val := range KnobsToParams(k) {
			out[key] = val
		}
		if k.ParallelToolCalls != nil {
			out["parallel_tool_calls"] = *k.ParallelToolCalls
		}
		if k.Reasoning != nil && k.Reasoning.OpenAIEffort != "" {
			out["reasoning_effort"] = k.Reasoning.OpenAIEffort
		}
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
		entry := map[string]any{
			"role":    role,
			"content": BuildOpenAIContent(m.Content),
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

// BuildOpenAIContent emits the JSON value for a message's `content`
// field. Pure-text messages stay as a single string; messages that carry
// image blocks switch to the array-of-parts shape.
func BuildOpenAIContent(parts []llm.ContentBlock) any {
	if hasImageBlock(parts) {
		return buildMultipartContent(parts)
	}
	return flattenTextContent(parts)
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
