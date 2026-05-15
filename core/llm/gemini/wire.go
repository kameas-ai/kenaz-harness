package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// ─── Gemini wire types (request side) ────────────────────────────────────────

// geminiRequest is the JSON body sent to the streamGenerateContent endpoint.
type geminiRequest struct {
	// SystemInstruction carries the system prompt.
	SystemInstruction *geminiContent      `json:"systemInstruction,omitempty"`
	Contents          []geminiContent     `json:"contents"`
	Tools             []geminiToolDef     `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig    `json:"generationConfig,omitempty"`
}

// geminiContent is one turn in the conversation.
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart is a polymorphic content fragment.
// Exactly one of the fields is non-zero.
type geminiPart struct {
	Text             string               `json:"text,omitempty"`
	InlineData       *geminiInlineData    `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall  `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse  `json:"functionResponse,omitempty"`
}

// geminiInlineData carries base64-encoded media.
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// geminiFunctionCall is a model-emitted function invocation.
type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// geminiFuncResponse is the result of a function call returned by the user.
type geminiFuncResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// geminiToolDef wraps a list of function declarations.
type geminiToolDef struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

// geminiFuncDecl is one callable function.
type geminiFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// geminiGenConfig holds generation parameters.
type geminiGenConfig struct {
	Temperature      *float64         `json:"temperature,omitempty"`
	TopP             *float64         `json:"topP,omitempty"`
	TopK             *int             `json:"topK,omitempty"`
	MaxOutputTokens  *int             `json:"maxOutputTokens,omitempty"`
	StopSequences    []string         `json:"stopSequences,omitempty"`
	CandidateCount   *int             `json:"candidateCount,omitempty"`
	ResponseMimeType string           `json:"responseMimeType,omitempty"`
	ThinkingConfig   *geminiThinking  `json:"thinkingConfig,omitempty"`
}

// geminiThinking enables Gemini 2.5 extended-thinking mode.
type geminiThinking struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

// ─── Gemini wire types (response side) ───────────────────────────────────────

// geminiResponse is one SSE frame from the streamGenerateContent endpoint.
type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata *geminiUsage        `json:"usageMetadata,omitempty"`
	ModelVersion  string              `json:"modelVersion,omitempty"`
}

// geminiCandidate is one generation candidate (we always request 1).
type geminiCandidate struct {
	Content       *geminiContent   `json:"content,omitempty"`
	FinishReason  string           `json:"finishReason,omitempty"`
	Index         int              `json:"index"`
}

// geminiUsage holds token counts from the terminal usageMetadata frame.
type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

// ─── Translation: llm → Gemini ───────────────────────────────────────────────

// ToGeminiRequest converts a GenerationRequest into the Gemini wire body.
// The system prompt is placed in systemInstruction, NOT in contents.
func ToGeminiRequest(req llm.GenerationRequest, prof llm.ProviderProfile) (*geminiRequest, error) {
	gr := &geminiRequest{}

	// System prompt → systemInstruction (not a content turn).
	if req.System != "" {
		gr.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.System}},
		}
	}

	// Translate messages.
	contents, err := toGeminiContents(req.Messages)
	if err != nil {
		return nil, err
	}
	gr.Contents = contents

	// Translate tools.
	if len(req.Tools) > 0 {
		decls := make([]geminiFuncDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decl := geminiFuncDecl{
				Name:        t.Name,
				Description: t.Description,
			}
			if len(t.InputSchema) > 0 {
				decl.Parameters = t.InputSchema
			}
			decls = append(decls, decl)
		}
		gr.Tools = []geminiToolDef{{FunctionDeclarations: decls}}
	}

	// Generation config.
	gc := buildGenerationConfig(req, prof)
	if gc != nil {
		gr.GenerationConfig = gc
	}

	return gr, nil
}

// toGeminiContents converts llm.Message slices to Gemini content turns.
// Role mapping:
//   - user → user
//   - assistant → model
//   - tool → user (with functionResponse parts)
//   - system → error (system goes into systemInstruction)
func toGeminiContents(msgs []llm.Message) ([]geminiContent, error) {
	out := make([]geminiContent, 0, len(msgs))
	for _, m := range msgs {
		role, err := toGeminiRole(m.Role)
		if err != nil {
			return nil, err
		}
		parts, err := toGeminiParts(m.Content)
		if err != nil {
			return nil, err
		}
		out = append(out, geminiContent{
			Role:  role,
			Parts: parts,
		})
	}
	return out, nil
}

func toGeminiRole(r llm.Role) (string, error) {
	switch r {
	case llm.RoleUser:
		return "user", nil
	case llm.RoleAssistant:
		return "model", nil
	case llm.RoleTool:
		// Tool results are surfaced as user turns with functionResponse parts.
		return "user", nil
	case llm.RoleSystem:
		// System messages should be in systemInstruction; if one reaches here
		// it means the caller constructed the messages array incorrectly.
		return "", &llm.ErrInvalidRequest{
			Status:  0,
			Message: "gemini: system role must use GenerationRequest.System, not messages",
		}
	default:
		return "user", nil
	}
}

// toGeminiParts translates ContentBlocks into Gemini parts.
func toGeminiParts(blocks []llm.ContentBlock) ([]geminiPart, error) {
	parts := make([]geminiPart, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "", "text":
			if b.Text != "" {
				parts = append(parts, geminiPart{Text: b.Text})
			}
		case "image":
			if b.Source == nil {
				continue
			}
			p, err := encodeImagePart(b.Source)
			if err != nil {
				return nil, err
			}
			parts = append(parts, p)
		case "document":
			// Gemini accepts files via the File API only; inline PDF bytes
			// are out of scope. Return a typed error so the caller knows.
			return nil, &llm.ErrInvalidRequest{
				Status:  0,
				Message: "gemini: document blocks require the File API (out of scope); use image blocks instead",
			}
		case "tool_use":
			if b.ToolUse == nil {
				continue
			}
			args := b.ToolUse.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: b.ToolUse.Name,
					Args: args,
				},
			})
		case "tool_result":
			// Tool results are wrapped in functionResponse parts.
			if b.ToolResult == nil && len(b.ToolData) == 0 {
				continue
			}
			var respPayload json.RawMessage
			if b.ToolResult != nil && len(b.ToolResult.Content) > 0 {
				respPayload = b.ToolResult.Content
			} else if len(b.ToolData) > 0 {
				respPayload = b.ToolData
			} else {
				respPayload = json.RawMessage(`{}`)
			}
			// Gemini wants the function name in functionResponse. We derive
			// it from ToolResult.ToolUseID or fall back to "unknown".
			name := "unknown"
			if b.ToolResult != nil && b.ToolResult.ToolUseID != "" {
				name = b.ToolResult.ToolUseID
			}
			// Wrap response in {output: <content>} envelope as Gemini expects.
			wrapped, err := wrapFunctionResponse(name, respPayload)
			if err != nil {
				return nil, err
			}
			parts = append(parts, wrapped)
		default:
			// Unknown block types: skip gracefully.
			if b.Text != "" {
				parts = append(parts, geminiPart{Text: b.Text})
			}
		}
	}
	return parts, nil
}

// wrapFunctionResponse builds a functionResponse part from a name and JSON payload.
func wrapFunctionResponse(name string, payload json.RawMessage) (geminiPart, error) {
	// Gemini wants: {"name": "<fn>", "response": {"output": <payload>}}
	// If the payload is already an object, we wrap it as-is.
	wrapped := json.RawMessage(`{"output":` + string(payload) + `}`)
	return geminiPart{
		FunctionResponse: &geminiFuncResponse{
			Name:     name,
			Response: wrapped,
		},
	}, nil
}

// buildGenerationConfig translates request params into geminiGenConfig.
func buildGenerationConfig(req llm.GenerationRequest, prof llm.ProviderProfile) *geminiGenConfig {
	gc := &geminiGenConfig{}
	hasAny := false

	setFloat := func(key string) {
		if v, ok := req.Params[key]; ok {
			if f, ok := toFloat64(v); ok {
				switch key {
				case "temperature":
					gc.Temperature = &f
				case "top_p":
					gc.TopP = &f
				}
				hasAny = true
			}
		} else if v, ok := prof.Defaults[key]; ok {
			if f, ok := toFloat64(v); ok {
				switch key {
				case "temperature":
					gc.Temperature = &f
				case "top_p":
					gc.TopP = &f
				}
				hasAny = true
			}
		}
	}
	setFloat("temperature")
	setFloat("top_p")

	setInt := func(key string) {
		if v, ok := req.Params[key]; ok {
			if n, ok := toInt(v); ok {
				switch key {
				case "max_tokens", "max_output_tokens":
					gc.MaxOutputTokens = &n
				case "top_k":
					gc.TopK = &n
				}
				hasAny = true
			}
		} else if v, ok := prof.Defaults[key]; ok {
			if n, ok := toInt(v); ok {
				switch key {
				case "max_tokens", "max_output_tokens":
					gc.MaxOutputTokens = &n
				case "top_k":
					gc.TopK = &n
				}
				hasAny = true
			}
		}
	}
	setInt("max_tokens")
	setInt("max_output_tokens")
	setInt("top_k")

	// JSON mode → responseMimeType
	if req.JSONMode != nil && req.JSONMode.Enabled {
		gc.ResponseMimeType = "application/json"
		hasAny = true
	}

	// Reasoning (thinkingConfig) for Gemini 2.5 models.
	if req.Reasoning != nil && req.Reasoning.Enabled {
		budget := req.Reasoning.BudgetTokens
		if budget <= 0 {
			budget = 8192 // sensible default
		}
		gc.ThinkingConfig = &geminiThinking{ThinkingBudget: budget}
		hasAny = true
	}

	if !hasAny {
		return nil
	}
	return gc
}

// ─── Translation: Gemini → llm ───────────────────────────────────────────────

// FromGeminiResponse converts one geminiResponse into a partial llm.Response.
// It does NOT fill Cost or Attempts — those are handled by the registry.
func FromGeminiResponse(gr geminiResponse) (llm.Response, error) {
	var resp llm.Response

	if gr.UsageMetadata != nil {
		resp.Usage = llm.Usage{
			InputTokens:     gr.UsageMetadata.PromptTokenCount,
			OutputTokens:    gr.UsageMetadata.CandidatesTokenCount,
			CachedInputRead: gr.UsageMetadata.CachedContentTokenCount,
			ReasoningTokens: gr.UsageMetadata.ThoughtsTokenCount,
		}
	}

	for _, cand := range gr.Candidates {
		if cand.FinishReason != "" {
			resp.FinishReason = strings.ToLower(cand.FinishReason)
		}
		if cand.Content == nil {
			continue
		}
		for _, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				resp.ToolCalls = append(resp.ToolCalls, llm.ToolUse{
					// Gemini doesn't assign IDs; synthesise positional ones.
					ID:    fmt.Sprintf("call_%d", len(resp.ToolCalls)),
					Name:  p.FunctionCall.Name,
					Input: p.FunctionCall.Args,
				})
			case p.Text != "":
				resp.Content = append(resp.Content, llm.ContentBlock{
					Type: "text",
					Text: p.Text,
				})
			}
		}
	}

	return resp, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}
