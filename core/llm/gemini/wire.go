package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// ─── Gemini wire types (request side) ────────────────────────────────────────

// geminiRequest is the JSON body sent to the streamGenerateContent endpoint.
type geminiRequest struct {
	// SystemInstruction carries the system prompt.
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiToolDef  `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
}

// geminiContent is one turn in the conversation.
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart is a polymorphic content fragment.
// Exactly one of the fields is non-zero.
type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	InlineData       *geminiInlineData   `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse `json:"functionResponse,omitempty"`
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
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	CandidateCount   *int            `json:"candidateCount,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	// ResponseSchema carries a structured-output constraint
	// (structured-output-is-reachable-01PMZE14 WP04). Gemini's schema
	// dialect is an OpenAPI 3.0 Schema Object subset, not JSON Schema —
	// see translateSchemaForGemini and geminiUnsupportedSchemaKeywords.
	ResponseSchema json.RawMessage `json:"responseSchema,omitempty"`
	ThinkingConfig *geminiThinking `json:"thinkingConfig,omitempty"`
}

// geminiThinking enables Gemini 2.5 extended-thinking mode.
type geminiThinking struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

// ─── Gemini wire types (response side) ───────────────────────────────────────

// geminiResponse is one SSE frame from the streamGenerateContent endpoint.
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
}

// geminiCandidate is one generation candidate (we always request 1).
type geminiCandidate struct {
	Content      *geminiContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
	Index        int            `json:"index"`
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
	gc, err := buildGenerationConfig(req, prof)
	if err != nil {
		return nil, err
	}
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
func buildGenerationConfig(req llm.GenerationRequest, prof llm.ProviderProfile) (*geminiGenConfig, error) {
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

	// StopSequences (model-request-path-live-01PMDL01 WP05): typed field
	// on GenerationRequest, maps directly onto the wire's stopSequences.
	if len(req.StopSequences) > 0 {
		gc.StopSequences = req.StopSequences
		hasAny = true
	}

	// ResponseFormat (structured-output-is-reachable-01PMZE14 WP04) takes
	// precedence over JSONMode, matching every other adapter's arm
	// (openaiwire/body.go:81 "ResponseFormat takes precedence";
	// azure/adapter.go:409's `&& req.ResponseFormat == nil`).
	switch {
	case req.ResponseFormat != nil:
		switch req.ResponseFormat.Mode {
		case "json":
			gc.ResponseMimeType = "application/json"
			hasAny = true
		case "json_schema":
			gc.ResponseMimeType = "application/json"
			hasAny = true
			if len(req.ResponseFormat.Schema) > 0 {
				translated, err := translateSchemaForGemini(req.ResponseFormat.Schema)
				if err != nil {
					return nil, err
				}
				gc.ResponseSchema = translated
			}
		case "grammar":
			return nil, &llm.ErrUnsupportedFormat{Provider: Kind, Model: prof.Model, Mode: req.ResponseFormat.Mode}
		}
	case req.JSONMode != nil && req.JSONMode.Enabled:
		// JSON mode → responseMimeType. Before WP04, JSONModeSpec.Schema
		// was silently discarded here — spec §1.3's trailing paragraph:
		// "Mode:'json' requests no capability at all... on gemini and
		// custom it produces an unconstrained wire call" that the
		// registry's repair-once loop then papers over, making it look
		// like it worked.
		gc.ResponseMimeType = "application/json"
		hasAny = true
		if len(req.JSONMode.Schema) > 0 {
			translated, err := translateSchemaForGemini(req.JSONMode.Schema)
			if err != nil {
				return nil, err
			}
			gc.ResponseSchema = translated
		}
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
		return nil, nil
	}
	return gc, nil
}

// geminiUnsupportedSchemaKeywords are the JSON-Schema keywords Gemini's
// responseSchema field — an OpenAPI 3.0 Schema Object subset, not JSON
// Schema — does not accept (structured-output-is-reachable-01PMZE14
// WP04, spec §5.2/§14 R-5).
//
// Source: Google's published Gemini API structured-output documentation
// (ai.google.dev/gemini-api/docs/structured-output), which states the
// schema is "a subset of the OpenAPI 3.0 Schema object" and excludes
// JSON-Schema-only combinators/refs. NOT independently re-verified
// against a live API call in this sandbox (no network access) — spec
// §13.3 flags this exact gap explicitly and requires an implementer to
// re-check it before shipping. Notably, "additionalProperties" is
// excluded deliberately: unlike azure/openai (which require
// structured.InjectAdditionalProperties to inject
// "additionalProperties": false for OpenAI's strict mode), Gemini's
// schema dialect does not have this keyword at all, so this adapter
// does NOT call InjectAdditionalProperties — doing so would inject a
// keyword Gemini rejects.
var geminiUnsupportedSchemaKeywords = []string{
	"$ref", "$defs", "definitions",
	"oneOf", "allOf", "anyOf", "not",
	"if", "then", "else",
	"patternProperties", "additionalProperties", "unevaluatedProperties",
	"const",
	"dependentSchemas", "dependentRequired",
	"contentEncoding", "contentMediaType",
}

// ErrUnsupportedSchemaKeyword is returned when an authored json_schema
// uses a keyword Gemini's responseSchema dialect cannot represent. What
// must NOT happen (spec §5.2) is a silent drop to responseMimeType
// alone — the registry's repair-once loop (registry.go:533) would then
// make an unconstrained call look successful, which is the same lie in
// a new costume.
type ErrUnsupportedSchemaKeyword struct {
	Keyword string
	Path    string
}

func (e *ErrUnsupportedSchemaKeyword) Error() string {
	if e.Path != "" {
		return "gemini: schema keyword " + e.Keyword + " (at " + e.Path + ") is not supported by Gemini's responseSchema dialect (OpenAPI 3.0 Schema subset)"
	}
	return "gemini: schema keyword " + e.Keyword + " is not supported by Gemini's responseSchema dialect (OpenAPI 3.0 Schema subset)"
}

// translateSchemaForGemini validates that schema uses only keywords
// Gemini's responseSchema dialect supports, recursively (the unsupported
// keywords can appear nested under "properties"/"items"/array-of-schema
// fields, not just at the top level). Returns the schema unchanged when
// it translates cleanly — no injection is performed, unlike azure/openai
// — or a typed *ErrUnsupportedSchemaKeyword naming the offending keyword
// and its path when it does not.
func translateSchemaForGemini(schema json.RawMessage) (json.RawMessage, error) {
	var doc any
	if err := json.Unmarshal(schema, &doc); err != nil {
		// Not parseable JSON at all; let the caller's own JSON handling
		// surface this — nothing keyword-specific to report.
		return schema, nil
	}
	if err := checkGeminiSchemaKeywords(doc, ""); err != nil {
		return nil, err
	}
	return schema, nil
}

func checkGeminiSchemaKeywords(node any, path string) error {
	switch v := node.(type) {
	case map[string]any:
		for _, kw := range geminiUnsupportedSchemaKeywords {
			if _, ok := v[kw]; ok {
				p := path
				if p == "" {
					p = "$"
				}
				return &ErrUnsupportedSchemaKeyword{Keyword: kw, Path: p}
			}
		}
		for k, child := range v {
			childPath := path + "." + k
			if path == "" {
				childPath = k
			}
			if err := checkGeminiSchemaKeywords(child, childPath); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range v {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if err := checkGeminiSchemaKeywords(child, childPath); err != nil {
				return err
			}
		}
	}
	return nil
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
					// Gemini doesn't assign IDs; synthesise positional ones
					// via the shared helper (see core/llm/toolcall_id.go).
					ID:    llm.SynthesizeToolCallID("", len(resp.ToolCalls)),
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
