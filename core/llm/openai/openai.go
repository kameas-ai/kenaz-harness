// Package openai is the OpenAI Chat Completions streaming adapter.
//
// The adapter speaks the public Chat Completions API (POST
// /v1/chat/completions, server-sent events) and the public /v1/models
// endpoint. It mirrors the structure of the anthropic adapter:
//
//   - stdlib net/http for transport (no SDK dependency)
//   - manual SSE parser
//   - typed error classification per the connector taxonomy
//   - llm.ProviderAdapter + llm.ModelLister implementation
//
// Authentication is a Bearer token carried in the Authorization header.
// Plaintext credential bytes never leave the adapter and never enter
// log payloads.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/capabilities"
	"github.com/sigil-tech/kaneaz-harness/core/llm/httpx"
	"github.com/sigil-tech/kaneaz-harness/core/llm/structured"
)

// Kind is the canonical provider kind for the OpenAI adapter. It must
// match the ProviderProfile.Kind value used in profile manifests and
// the capability catalog filename (core/llm/capabilities/data/openai.yaml).
const Kind = "openai"

// Default endpoints. Operators can override the chat endpoint via
// ProviderProfile.Endpoint. Tests inject both via WithEndpoint to
// route through httptest.Server.
const (
	defaultChatEndpoint   = "https://api.openai.com/v1/chat/completions"
	defaultModelsEndpoint = "https://api.openai.com/v1/models"
)

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithHTTPClient sets the underlying HTTP client. The client should
// have NO request-level timeout — the adapter relies on context
// cancellation so a long-running stream is not killed mid-flight.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// WithEndpoint overrides the chat completions URL. Useful for fixtures
// served from httptest and self-hosted gateways. The models URL is
// derived from the chat URL by replacing the trailing
// "/chat/completions" with "/models".
func WithEndpoint(u string) Option {
	return func(a *Adapter) {
		if u != "" {
			a.endpoint = u
		}
	}
}

// WithCatalog overrides the capability catalog. Tests that need bespoke
// advertised capabilities inject a fixture catalog here.
func WithCatalog(cat *capabilities.Catalog) Option {
	return func(a *Adapter) {
		if cat != nil {
			a.cat = cat
		}
	}
}

// Adapter is the OpenAI ProviderAdapter implementation.
type Adapter struct {
	httpc    *http.Client
	endpoint string
	cat      *capabilities.Catalog
}

// New constructs an Adapter. Failures are limited to the embedded
// capability catalog load which is generally a programming error
// (corrupt embed). On catalog failure the adapter falls back to a nil
// catalog and Capabilities() returns an inline conservative descriptor.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		httpc:    &http.Client{Transport: httpx.DefaultTransport()}, // no Timeout — context drives lifetime
		endpoint: defaultChatEndpoint,
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind ("openai").
func (a *Adapter) Kind() string { return Kind }

// Capabilities reports the (provider, model) descriptor.
//
// When a capability catalog is loaded, descriptors come from
// capabilities/data/openai.yaml (the canonical source). When the
// catalog is unavailable, an inline conservative descriptor is
// returned: streaming + tool_calling + usage_reporting on for all
// chat models, and vision is enabled for the GPT-4o / GPT-4-vision /
// GPT-4-turbo / GPT-4.1 families that ship multimodal support.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	if a.cat != nil {
		return a.cat.Describe(Kind, model)
	}
	supported := map[llm.Capability]bool{
		llm.CapStreaming:      true,
		llm.CapToolCalling:    true,
		llm.CapUsageReporting: true,
	}
	if isVisionFamily(model) {
		supported[llm.CapVision] = true
	}
	return llm.CapabilityDescriptor{
		Provider:  Kind,
		Model:     model,
		Supported: supported,
		Notes: map[llm.Capability]string{
			llm.CapStreaming: "catalog_unavailable",
		},
	}
}

// isVisionFamily reports whether model belongs to a known vision-
// capable OpenAI family. The list intentionally over-approximates:
// gating logic in the registry's CapabilityGate ultimately defers to
// the YAML catalog for authoritative answers.
func isVisionFamily(model string) bool {
	m := strings.ToLower(model)
	for _, prefix := range []string{"gpt-4o", "gpt-4-vision", "gpt-4-turbo", "gpt-4.1"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

// Compile-time assertion: *Adapter satisfies llm.ProviderAdapter.
var _ llm.ProviderAdapter = (*Adapter)(nil)

// Compile-time assertion: *Adapter satisfies llm.StructuredOutputAdapter.
var _ llm.StructuredOutputAdapter = (*Adapter)(nil)

// ApplyResponseFormat implements llm.StructuredOutputAdapter. It translates
// req.ResponseFormat into the OpenAI Chat Completions wire shape:
//
//   - Mode="json"        → response_format: {type: "json_object"}
//   - Mode="json_schema" → response_format: {type: "json_schema", json_schema: {name: "response", schema: ..., strict: true}}
//     with "additionalProperties": false injected when absent (OpenAI strict requirement).
//   - Mode="grammar"     → ErrUnsupportedFormat (cloud provider, no GBNF support)
//
// (structured-output-and-grammar-01KX5R8A WP03b)
func (a *Adapter) ApplyResponseFormat(req *llm.GenerationRequest, wireBody map[string]any) error {
	if req == nil || req.ResponseFormat == nil {
		return nil
	}
	rf := req.ResponseFormat
	switch rf.Mode {
	case "json":
		wireBody["response_format"] = map[string]any{"type": "json_object"}
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
				return fmt.Errorf("openai: response_format schema parse: %w", err)
			}
		}
		wireBody["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": schemaVal,
				"strict": true,
			},
		}
	case "grammar":
		return &llm.ErrUnsupportedFormat{Provider: Kind, Model: "", Mode: rf.Mode}
	default:
		// Unknown mode — treat as no-op so future modes don't break existing callers.
	}
	return nil
}

// applyJSONMode translates a JSONModeSpec into the OpenAI wire shape.
// Called from buildRequestBody when req.JSONMode is set and req.ResponseFormat
// is nil (ResponseFormat takes precedence when both are present).
//
//   - Schema present → response_format: {type: "json_schema", json_schema: {name, schema, strict}}
//   - Schema absent  → response_format: {type: "json_object"}
//
// (multimodal-io-extended-01KQ8TD2 WP03)
func applyJSONMode(jm *llm.JSONModeSpec, wireBody map[string]any) error {
	if jm == nil || !jm.Enabled {
		return nil
	}
	if len(jm.Schema) == 0 {
		wireBody["response_format"] = map[string]any{"type": "json_object"}
		return nil
	}
	// Schema present: use json_schema shape.
	schema := jm.Schema
	injected, err := structured.InjectAdditionalProperties(schema)
	if err == nil {
		schema = injected
	}
	var schemaVal any
	if err := json.Unmarshal(schema, &schemaVal); err != nil {
		return fmt.Errorf("openai: json_mode schema parse: %w", err)
	}
	name := jm.Name
	if name == "" {
		name = "response"
	}
	wireBody["response_format"] = map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   name,
			"schema": schemaVal,
			"strict": jm.Strict,
		},
	}
	return nil
}

// Stream opens an SSE connection to the Chat Completions API and
// returns a llm.Stream that pumps StreamEvent values to the caller.
//
// Lifetime / cancellation contract:
//   - The HTTP request is created with a derived ctx so that ctx.Done()
//     severs the connection.
//   - The returned Stream additionally exposes Cancel() which cancels
//     the per-call context and closes the response Body.
//   - cred is the resolved Bearer-token bytes; the adapter uses them
//     only to set the Authorization header and never logs them.
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Status: 0, Message: "openai: empty credential"}
	}

	body, err := buildRequestBody(req, prof)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

	endpoint := a.endpoint
	if prof.Endpoint != "" {
		endpoint = prof.Endpoint
	}

	// Per-call cancellation handle: req.WithContext threads ctx into
	// the transport, but Cancel() must work even after ctx scope ends.
	streamCtx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-streamCtx.Done():
		}
	}()

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	// OpenAI uses Bearer-token auth. The cred slice carries the API
	// key bytes; we cross the bytes-to-string boundary exactly once,
	// inline with the header. The registry zeroizes its private copy
	// of cred on Final()/Cancel().
	httpReq.Header.Set("Authorization", "Bearer "+string(cred))

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
		// Network failures are transient by default — connection
		// reset, DNS hiccup, dialer timeout. The retry middleware
		// decides whether to retry given the configured budget.
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, classifyStatus(resp.StatusCode, bodySnippet)
	}

	s := &chatStream{
		resp:   resp,
		cancel: cancel,
		events: make(chan llm.StreamEvent, 16),
		done:   make(chan struct{}),
	}
	go s.pump()
	return s, nil
}

// classifyStatus maps an HTTP error response to the connector taxonomy.
//
// OpenAI returns errors as JSON envelopes:
//
//	{"error": {"message": "...", "type": "...", "code": "..."}}
//
// The classification is by HTTP status, not by error type field —
// only status guarantees retryability semantics. The body is included
// in the typed error message verbatim (capped) for debugging.
func classifyStatus(status int, body []byte) error {
	msg := extractErrorMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &llm.ErrAuth{Status: status, Message: msg}
	case status == 429:
		return &llm.ErrTransient{Status: status, Message: msg}
	case status >= 500 && status < 600:
		return &llm.ErrTransient{Status: status, Message: msg}
	case status == 408 || status == 425:
		return &llm.ErrTransient{Status: status, Message: msg}
	default:
		// 400 / 404 / 422 and friends: non-retryable client errors.
		return &llm.ErrInvalidRequest{Status: status, Message: msg}
	}
}

// extractErrorMessage best-effort parses an OpenAI error envelope.
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error.Message != "" {
			if env.Error.Type != "" {
				return env.Error.Type + ": " + env.Error.Message
			}
			return env.Error.Message
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// buildRequestBody constructs the JSON body for the Chat Completions
// API. The connector's polymorphic ContentBlocks are flattened into
// OpenAI's simpler {role, content: "string"} message shape — multi-
// part messages join their text parts with newline separators. Vision
// inputs are out of scope for the v1 adapter (the CapabilityGate
// rejects requests that opt into them on this provider).
func buildRequestBody(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
	out := map[string]any{
		"model":          prof.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}

	// Optional sampling knobs surfaced through Params / profile defaults.
	for _, key := range []string{"temperature", "top_p", "max_tokens", "presence_penalty", "frequency_penalty"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		} else if v, ok := prof.Defaults[key]; ok {
			out[key] = v
		}
	}

	// Apply ResponseFormat if set (structured-output-and-grammar-01KX5R8A WP03b).
	if req.ResponseFormat != nil {
		// Build a temporary Adapter to reuse ApplyResponseFormat without
		// adding a package-level singleton. The adapter state (httpc,
		// endpoint) is not used by ApplyResponseFormat.
		a := &Adapter{}
		if err := a.ApplyResponseFormat(&req, out); err != nil {
			return nil, err
		}
	}

	// Apply JSONMode if set (multimodal-io-extended-01KQ8TD2 WP03).
	// JSONMode predates ResponseFormat; when both are set ResponseFormat wins
	// (it's already applied above). When only JSONMode is set, translate it.
	if req.JSONMode != nil && req.JSONMode.Enabled && req.ResponseFormat == nil {
		if err := applyJSONMode(req.JSONMode, out); err != nil {
			return nil, err
		}
	}

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
		// Tool messages get the OpenAI-canonical role name. For v1
		// we don't carry tool_call_id because the connector's
		// ContentBlock shape doesn't surface it; the gate rejects
		// tool requests for the OpenAI adapter for now.
		entry := map[string]any{
			"role":    role,
			"content": buildOpenAIContent(m.Content),
		}
		msgs = append(msgs, entry)
	}
	out["messages"] = msgs

	// Tool serialization — Chat Completions wraps each spec in a
	// {type:"function", function:{...}} envelope. The function's
	// `parameters` field is the JSON Schema, which is the same shape
	// our ToolSpec.InputSchema already carries.
	// Reference: https://platform.openai.com/docs/guides/function-calling
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			fn := map[string]any{
				"name":        t.Name,
				"description": t.Description,
			}
			if len(t.InputSchema) > 0 {
				var schema any
				if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
					return nil, fmt.Errorf("openai: tool %q parameters: %w", t.Name, err)
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
		out["tools"] = tools
	}

	return json.Marshal(out)
}

// flattenText concatenates the text fragments of parts into a single
// string. Non-text parts are ignored (the adapter advertises only
// text capability without a catalog override). An empty content slice
// produces "" — OpenAI tolerates empty strings in user/assistant
// messages but the gate-level path rarely produces this.
func flattenText(parts []llm.ContentBlock) string {
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

// buildOpenAIContent emits the JSON value to put under a message's
// `content` field. Pure-text messages stay as a single string (the
// classic Chat Completions shape); messages that carry image blocks
// switch to the array-of-parts shape per
// https://platform.openai.com/docs/guides/vision.
//
// Document blocks (PDF) are NOT serialized — Chat Completions does not
// accept inline PDFs. The capabilities gate (CheckAttachments) rejects
// PDF blocks pre-flight for OpenAI profiles (document_input: false in
// openai.yaml), so document blocks should never reach this builder.
// If one arrives anyway (e.g. a caller bypassing the registry), it is
// dropped silently so the text portion of the message still goes through
// (multimodal-io-01KQ8TDF FR-012).
func buildOpenAIContent(parts []llm.ContentBlock) any {
	if hasMultimodal(parts) {
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
				// Gate-rejected pre-flight; defensive drop here so the text
				// portion of the turn is not lost if a caller bypasses the registry.
				// The openai.yaml has document_input: false, so the gate
				// (CheckAttachments) returns ErrAttachmentMimeUnsupported before
				// this function is ever called in production.
				continue
			default:
				if p.Text != "" {
					out = append(out, map[string]any{"type": "text", "text": p.Text})
				}
			}
		}
		if len(out) == 0 {
			return ""
		}
		return out
	}
	return flattenText(parts)
}

// hasMultimodal reports whether any block requires the array-of-parts
// content shape (image only — documents are dropped, see TODO above).
func hasMultimodal(parts []llm.ContentBlock) bool {
	for _, p := range parts {
		if p.Type == "image" {
			return true
		}
	}
	return false
}

// dataURL composes a data: URL from a base64 MediaSource. OpenAI's
// vision endpoint accepts both data URLs and remote https URLs; we
// always emit data URLs because the harness stores image bytes in a
// local CAS (see WP02) and never serves them on the public internet.
func dataURL(src *llm.MediaSource) string {
	if src.URI != "" && src.Kind == "uri" {
		return src.URI
	}
	return "data:" + src.MediaType + ";base64," + src.Data
}

// mediaTypeOf safely extracts a MediaSource.MediaType (empty when nil).
func mediaTypeOf(src *llm.MediaSource) string {
	if src == nil {
		return ""
	}
	return src.MediaType
}

// chatStream is the SSE consumer. It owns the http.Response body and
// the cancellation handle. Pump runs in a goroutine; events are
// exposed via Events().
type chatStream struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan llm.StreamEvent
	done   chan struct{}

	mu        sync.Mutex
	final     llm.Response
	finalErr  error
	cancelled bool

	textBuf    strings.Builder
	usage      llm.Usage
	finishStop string

	// tool_calls accumulator. OpenAI streams tool calls as a sequence
	// of deltas keyed by index; we reassemble the JSON-encoded
	// arguments and emit one StreamTool event when the call is
	// finalized (via finish_reason or stream end).
	toolPartial map[int]*toolCallState
	toolOrder   []int
	toolCalls   []llm.ToolUse
}

type toolCallState struct {
	id        string
	name      string
	arguments strings.Builder
	emitted   bool
}

// Events returns the channel of streaming chunks.
func (s *chatStream) Events() <-chan llm.StreamEvent { return s.events }

// Cancel terminates the upstream connection. Safe to call repeatedly
// and from any goroutine.
func (s *chatStream) Cancel() error {
	s.mu.Lock()
	if !s.cancelled {
		s.cancelled = true
	}
	s.mu.Unlock()
	s.cancel()
	if s.resp != nil && s.resp.Body != nil {
		_ = s.resp.Body.Close()
	}
	return nil
}

// Final blocks until the pump finishes and returns the accumulated
// Response. After Cancel, Final returns ErrCancelled.
func (s *chatStream) Final() (llm.Response, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled && s.finalErr == nil {
		return llm.Response{}, &llm.ErrCancelled{Reason: "stop"}
	}
	return s.final, s.finalErr
}

// pump reads SSE frames, translates them into StreamEvents, and
// terminates by closing s.events and s.done.
//
// OpenAI's SSE protocol:
//
//	data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}
//	data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}
//	data: {"choices":[{"delta":{},"finish_reason":"stop"}]}
//	data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}
//	data: [DONE]
//
// The terminal [DONE] sentinel closes the stream cleanly. A usage
// frame may arrive immediately before [DONE] when the request opted
// into stream_options.include_usage=true (we always do).
func (s *chatStream) pump() {
	defer func() {
		_ = s.resp.Body.Close()
		close(s.events)
		s.cancel()
		// Build final Response from accumulators.
		s.mu.Lock()
		s.flushPendingToolCallsLocked()
		if s.finalErr == nil && !s.cancelled {
			s.final = llm.Response{
				Content:      []llm.ContentBlock{{Type: "text", Text: s.textBuf.String()}},
				ToolCalls:    s.toolCalls,
				FinishReason: s.finishStop,
				Usage:        s.usage,
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()

	scanner := bufio.NewScanner(s.resp.Body)
	// 1 MiB max line — defensive headroom for unusually large frames
	// (e.g. JSON-mode responses with embedded blobs).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			// Comment / keepalive (": OPENAI PROCESSING").
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			// Ignore unknown SSE fields for forward-compat.
			continue
		}
		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			return
		}
		s.handleFrame([]byte(payload))
	}
	if err := scanner.Err(); err != nil {
		s.mu.Lock()
		switch {
		case s.cancelled:
			// Caller-driven cancel — keep finalErr nil so Final
			// returns ErrCancelled.
		case errors.Is(err, context.Canceled):
			s.finalErr = &llm.ErrCancelled{Reason: "context"}
		default:
			s.finalErr = &llm.ErrTransient{Message: "openai: stream read: " + err.Error(), Cause: err}
		}
		s.mu.Unlock()
		select {
		case s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}:
		default:
		}
	}
}

// handleFrame parses one SSE data payload and emits the corresponding
// StreamEvents.
//
// tool_calls deltas follow OpenAI's chunked function-calling shape
// (https://platform.openai.com/docs/guides/function-calling): each
// delta carries one or more entries keyed by `index` whose `function.
// arguments` string fragment must be concatenated across frames. The
// terminating `finish_reason: "tool_calls"` triggers the StreamTool
// emission with the reassembled JSON arguments.
func (s *chatStream) handleFrame(raw []byte) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	var env struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "openai: malformed SSE frame: " + err.Error()}
		return
	}

	for _, ch := range env.Choices {
		if ch.Delta.Content != "" {
			s.mu.Lock()
			s.textBuf.WriteString(ch.Delta.Content)
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: ch.Delta.Content, Raw: trimmed}
		}
		for _, tc := range ch.Delta.ToolCalls {
			s.mu.Lock()
			if s.toolPartial == nil {
				s.toolPartial = map[int]*toolCallState{}
			}
			state, ok := s.toolPartial[tc.Index]
			if !ok {
				state = &toolCallState{}
				s.toolPartial[tc.Index] = state
				s.toolOrder = append(s.toolOrder, tc.Index)
			}
			if tc.ID != "" {
				state.id = tc.ID
			}
			if tc.Function.Name != "" {
				state.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				state.arguments.WriteString(tc.Function.Arguments)
			}
			s.mu.Unlock()
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			s.mu.Lock()
			s.finishStop = *ch.FinishReason
			if *ch.FinishReason == "tool_calls" {
				s.flushPendingToolCallsLocked()
			}
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: *ch.FinishReason}
		}
	}
	if env.Usage != nil {
		s.mu.Lock()
		s.usage.InputTokens = env.Usage.PromptTokens
		s.usage.OutputTokens = env.Usage.CompletionTokens
		usage := s.usage
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamUsage, Usage: &usage}
	}
}

// flushPendingToolCallsLocked materializes any reassembled tool_calls
// into ToolUse values, emitting one StreamTool event per call. Caller
// must hold s.mu. Re-entrant: already-emitted calls are skipped so the
// flush triggered by finish_reason "tool_calls" doesn't double-emit
// when pump's defer also calls it.
func (s *chatStream) flushPendingToolCallsLocked() {
	for _, idx := range s.toolOrder {
		state, ok := s.toolPartial[idx]
		if !ok || state.emitted {
			continue
		}
		args := state.arguments.String()
		if args == "" {
			args = "{}"
		}
		tool := llm.ToolUse{
			ID:    state.id,
			Name:  state.name,
			Input: json.RawMessage(args),
		}
		s.toolCalls = append(s.toolCalls, tool)
		state.emitted = true
		// Send without holding lock to avoid blocking on a slow consumer
		// while the mutex is held. The buffered channel typically absorbs.
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tool}
		s.mu.Lock()
	}
}

// ListModels implements llm.ModelLister. It calls OpenAI's /v1/models
// endpoint with the supplied API key and returns a filtered+sorted
// list of chat-capable model entries.
//
// The endpoint URL is derived from the adapter's configured chat URL
// by replacing the trailing "/chat/completions" with "/models" so a
// custom endpoint (httptest fixture, self-hosted gateway) is honoured.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	if len(cred) == 0 {
		return nil, errors.New("openai: empty credential")
	}
	url := modelsURL(a.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("openai: build models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(cred))
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyStatus(resp.StatusCode, body)
	}
	var doc modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("openai: parse models response: %w", err)
	}
	out := make([]llm.ModelInfo, 0, len(doc.Data))
	for _, m := range doc.Data {
		if !isChatCapable(m.ID) {
			continue
		}
		cw := 0
		if a.cat != nil {
			cw = a.cat.ContextWindow(Kind, m.ID)
		}
		out = append(out, llm.ModelInfo{
			ID:            m.ID,
			DisplayName:   m.ID,
			ContextWindow: cw,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// TestKey implements llm.KeyTester. Verifies cred by calling ListModels with
// a 5-second deadline; returns nil iff the response contains ≥1 model.
//
// provider-keychain-rotation-01KQ8TD9 WP02.
func (a *Adapter) TestKey(ctx context.Context, cred []byte) error {
	if len(cred) == 0 {
		return &llm.ErrAuth{Status: 0, Message: "openai: empty credential"}
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	models, err := a.ListModels(tctx, cred)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return &llm.ErrInvalidRequest{Status: 200, Message: "openai: key accepted but returned empty model list"}
	}
	return nil
}

// Compile-time assertion: Adapter implements llm.KeyTester.
var _ llm.KeyTester = (*Adapter)(nil)

// modelsResponse mirrors the JSON shape OpenAI returns from /v1/models.
type modelsResponse struct {
	Data []struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	} `json:"data"`
}

// modelsURL derives the /models URL from the configured chat URL.
// Production: ".../v1/chat/completions" → ".../v1/models". Custom
// endpoints that don't end in "/chat/completions" get "/models"
// appended (or, if the URL already ends in "/models", returned as-is).
func modelsURL(chatURL string) string {
	if strings.HasSuffix(chatURL, "/chat/completions") {
		return strings.TrimSuffix(chatURL, "/chat/completions") + "/models"
	}
	if strings.HasSuffix(chatURL, "/models") {
		return chatURL
	}
	return strings.TrimRight(chatURL, "/") + "/models"
}

// isChatCapable filters out non-chat model IDs returned by /v1/models.
// OpenAI's catalog includes audio, embedding, image, and moderation
// models that are not addressable through Chat Completions; surfacing
// them in the AddProvider model picker would be confusing.
func isChatCapable(id string) bool {
	lower := strings.ToLower(id)
	for _, needle := range []string{"whisper", "tts", "dall-e", "embedding", "moderation", "audio"} {
		if strings.Contains(lower, needle) {
			return false
		}
	}
	return true
}
