package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/capabilities"
)

// Kind is the canonical provider kind for the Anthropic adapter. It
// matches the ProviderProfile.Kind value used in profile manifests and
// the capability catalog filename (core/llm/capabilities/data/anthropic.yaml).
const Kind = "anthropic"

// Default values for the wire transport. Operators can override the
// endpoint via ProviderProfile.Endpoint and the API version via the
// per-request Params map ("api_version" key).
const (
	defaultEndpoint   = "https://api.anthropic.com/v1/messages"
	defaultAPIVersion = "2023-06-01"
)

// Option configures an Adapter at construction time. Tests inject a
// custom http.Client (httptest.Server) and a fixed clock; production
// passes nil for both and gets the stdlib defaults.
type Option func(*Adapter)

// WithHTTPClient sets the underlying HTTP client. The client should
// have NO request-level timeout — the adapter relies on context
// cancellation so that a long-running stream is not killed mid-flight.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// WithEndpoint overrides the Messages API URL. Useful for fixtures
// served from httptest and for self-hosted gateways that proxy the
// public API surface.
func WithEndpoint(u string) Option {
	return func(a *Adapter) {
		if u != "" {
			a.endpoint = u
		}
	}
}

// WithAPIVersion overrides the anthropic-version header value.
func WithAPIVersion(v string) Option {
	return func(a *Adapter) {
		if v != "" {
			a.apiVersion = v
		}
	}
}

// WithCatalog overrides the capability catalog. By default the adapter
// loads the embedded catalog at construction; tests inject a fixture
// catalog when they need bespoke advertised capabilities.
func WithCatalog(cat *capabilities.Catalog) Option {
	return func(a *Adapter) {
		if cat != nil {
			a.cat = cat
		}
	}
}

// Adapter is the Anthropic ProviderAdapter implementation.
type Adapter struct {
	httpc      *http.Client
	endpoint   string
	apiVersion string
	cat        *capabilities.Catalog
}

// New constructs an Adapter. Failures are limited to the embedded
// capability catalog load which is generally a programming error
// (corrupt embed). In that case New falls back to a nil catalog and
// Capabilities() returns the connector's "unknown_provider_default"
// descriptor — the registry's CapabilityGate will still gate vision /
// reasoning for unknown models, so the failure is non-fatal.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		httpc:      &http.Client{}, // no Timeout — context drives lifetime
		endpoint:   defaultEndpoint,
		apiVersion: defaultAPIVersion,
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind ("anthropic").
func (a *Adapter) Kind() string { return Kind }

// Capabilities reports the (provider, model) descriptor.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	if a.cat == nil {
		// Safe baseline: streaming + usage_reporting on, everything else
		// off. Mirrors capabilities.Catalog.Describe's unknown-provider path.
		return llm.CapabilityDescriptor{
			Provider: Kind, Model: model,
			Supported: map[llm.Capability]bool{
				llm.CapStreaming:      true,
				llm.CapUsageReporting: true,
			},
			Notes: map[llm.Capability]string{
				llm.CapStreaming: "catalog_unavailable",
			},
		}
	}
	return a.cat.Describe(Kind, model)
}

// Compile-time assertion: *Adapter satisfies llm.ProviderAdapter.
var _ llm.ProviderAdapter = (*Adapter)(nil)

// Stream opens an SSE connection to the Messages API and returns a
// llm.Stream that pumps StreamEvent values to the caller.
//
// Lifetime / cancellation contract:
//   - The HTTP request is created with ctx so that ctx.Done() severs
//     the connection.
//   - The returned Stream additionally exposes Cancel() which calls
//     the request's cancel function (req.Body.Close path) to terminate
//     the stream from outside the original ctx scope.
//   - cred is the resolved API-key bytes; the adapter uses them only
//     to set the x-api-key header and never logs them.
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Status: 0, Message: "anthropic: empty credential bytes"}
	}

	body, err := buildRequestBody(req, prof)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

	endpoint := a.endpoint
	if prof.Endpoint != "" {
		endpoint = prof.Endpoint
	}
	apiVersion := a.apiVersion
	if v, ok := req.Params["api_version"].(string); ok && v != "" {
		apiVersion = v
	}

	// Per-call cancellation handle: req.WithContext threads ctx into the
	// transport, but Cancel() must work even after ctx scope ends. We
	// derive a per-call ctx from a context.Background() and cancel it on
	// either ctx.Done or explicit Cancel.
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
	httpReq.Header.Set("anthropic-version", apiVersion)
	// x-api-key uses the resolved credential bytes. We copy into a
	// header value via string conversion; net/http retains a string
	// reference, so this is the unavoidable boundary where bytes
	// become a string. The registry's auditedStream zeroizes its copy
	// on Final/Cancel; the wider zeroization story for header strings
	// is tracked under WP14 hardening and is deliberately out of scope
	// for the v1 adapter (R2: no plaintext in payloads).
	httpReq.Header.Set("x-api-key", string(cred))

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
		// Network failures are transient by default — connection reset,
		// DNS hiccup, dialer timeout. The retry middleware decides
		// whether to retry given the configured budget.
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		// Drain a small body so we have a redaction-safe error message.
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, classifyStatus(resp.StatusCode, bodySnippet)
	}

	s := &stream{
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
// The Messages API returns:
//
//   - 401 → invalid api_key (auth — non-retryable)
//   - 403 → permission_denied / forbidden (auth — non-retryable)
//   - 400 → invalid_request_error (non-retryable, includes content
//     policy refusals reported as 400 with type=invalid_request_error)
//   - 408/425/429 → transient
//   - 5xx → transient
//
// Bodies follow:
//
//	{"type":"error","error":{"type":"...","message":"..."}}
func classifyStatus(status int, body []byte) error {
	msg := extractErrorMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &llm.ErrAuth{Status: status, Message: msg}
	case status == 408 || status == 425 || status == 429:
		return &llm.ErrTransient{Status: status, Message: msg}
	case status >= 500 && status < 600:
		return &llm.ErrTransient{Status: status, Message: msg}
	default:
		// 400 (invalid_request_error including content-policy refusals),
		// 404, 422, etc. — all non-retryable.
		return &llm.ErrInvalidRequest{Status: status, Message: msg}
	}
}

// extractErrorMessage best-effort parses an Anthropic error envelope.
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
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
	// Fall back to a trimmed snippet, never the full body, to keep
	// audit payloads predictable.
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// buildRequestBody constructs the JSON body for the Messages API.
//
// The Messages API shape (relevant subset):
//
//	{
//	  "model": "claude-sonnet-4-5",
//	  "system": "...",
//	  "messages": [
//	    {"role": "user", "content": [{"type":"text","text":"hi"}]},
//	    ...
//	  ],
//	  "tools": [{"name":"...","description":"...","input_schema":{...}}],
//	  "max_tokens": 1024,
//	  "stream": true,
//	  "temperature": 0.7,
//	  ...
//	}
func buildRequestBody(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
	out := map[string]any{
		"model":  prof.Model,
		"stream": true,
	}

	// max_tokens is required by the Messages API. Default to a generous
	// but bounded value so the v1 adapter never sends a request the API
	// rejects up front.
	maxTokens := 1024
	if v, ok := req.Params["max_tokens"]; ok {
		if i, ok := numToInt(v); ok && i > 0 {
			maxTokens = i
		}
	} else if v, ok := prof.Defaults["max_tokens"]; ok {
		if i, ok := numToInt(v); ok && i > 0 {
			maxTokens = i
		}
	}
	out["max_tokens"] = maxTokens

	// Optional sampling knobs surfaced through Params.
	for _, key := range []string{"temperature", "top_p", "top_k"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		} else if v, ok := prof.Defaults[key]; ok {
			out[key] = v
		}
	}

	// system: a single string at top-level (Anthropic's contract). We
	// merge a dedicated System field with any role=system messages for
	// convenience — RoleSystem in the connector represents either form.
	systemParts := []string{}
	if req.System != "" {
		systemParts = append(systemParts, req.System)
	}

	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			for _, c := range m.Content {
				if c.Type == "text" || c.Type == "" {
					if c.Text != "" {
						systemParts = append(systemParts, c.Text)
					}
				}
			}
			continue
		}
		role := string(m.Role)
		if role == "" {
			role = string(llm.RoleUser)
		}
		// Anthropic uses "user" / "assistant"; tool messages are folded
		// into user-role messages with a tool_result content block.
		converted := map[string]any{
			"role":    role,
			"content": convertContent(m.Content),
		}
		if m.Role == llm.RoleTool {
			converted["role"] = "user"
		}
		msgs = append(msgs, converted)
	}
	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n\n")
	}
	out["messages"] = msgs

	// Tools — basic shape (FR-006).
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			entry := map[string]any{
				"name":        t.Name,
				"description": t.Description,
			}
			if len(t.InputSchema) > 0 {
				var schema any
				if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
					return nil, fmt.Errorf("anthropic: tool %q input_schema: %w", t.Name, err)
				}
				entry["input_schema"] = schema
			} else {
				// Anthropic requires a JSON Schema object. An empty
				// tool spec is rejected by the API, so we default to an
				// open object schema.
				entry["input_schema"] = map[string]any{"type": "object"}
			}
			tools = append(tools, entry)
		}
		out["tools"] = tools
	}

	return json.Marshal(out)
}

// convertContent maps connector ContentParts to Anthropic content blocks.
func convertContent(parts []llm.ContentPart) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "", "text":
			if p.Text == "" {
				continue
			}
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		case "tool_use":
			if p.ToolUse == nil {
				continue
			}
			block := map[string]any{
				"type": "tool_use",
				"id":   p.ToolUse.ID,
				"name": p.ToolUse.Name,
			}
			if len(p.ToolUse.Input) > 0 {
				var input any
				_ = json.Unmarshal(p.ToolUse.Input, &input)
				block["input"] = input
			} else {
				block["input"] = map[string]any{}
			}
			out = append(out, block)
		case "tool_result":
			block := map[string]any{
				"type": "tool_result",
			}
			if len(p.ToolData) > 0 {
				var content any
				_ = json.Unmarshal(p.ToolData, &content)
				block["content"] = content
			}
			out = append(out, block)
		default:
			// Unknown types are passed through as text using the typed
			// text field (defense-in-depth: never put raw bytes in body).
			if p.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": p.Text})
			}
		}
	}
	if len(out) == 0 {
		// Anthropic rejects empty content arrays; ensure at least one
		// block so the API call is well-formed.
		out = append(out, map[string]any{"type": "text", "text": ""})
	}
	return out
}

// numToInt accepts both int and float64 (JSON unmarshals to float64).
func numToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// stream is the SSE consumer. It owns the http.Response body and the
// cancellation handle. Pump runs in a goroutine; events are exposed
// via Events().
type stream struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan llm.StreamEvent
	done   chan struct{}

	mu        sync.Mutex
	final     llm.Response
	finalErr  error
	cancelled bool

	// accumulators populated as the SSE pump runs.
	textBuf   strings.Builder
	toolCalls []llm.ToolUse
	// pending tool_use blocks indexed by content-block index so partial
	// JSON deltas can be reassembled.
	toolPartial map[int]*toolPartialState
	usage       llm.Usage
	finishStop  string
}

type toolPartialState struct {
	id       string
	name     string
	rawInput strings.Builder
}

// Events returns the channel of streaming chunks.
func (s *stream) Events() <-chan llm.StreamEvent { return s.events }

// Cancel terminates the upstream connection. Safe to call repeatedly
// and from any goroutine.
func (s *stream) Cancel() error {
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
func (s *stream) Final() (llm.Response, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled && s.finalErr == nil {
		return llm.Response{}, &llm.ErrCancelled{Reason: "caller"}
	}
	return s.final, s.finalErr
}

// pump reads SSE frames, translates them into StreamEvents, and
// terminates by closing s.events and s.done.
func (s *stream) pump() {
	defer func() {
		_ = s.resp.Body.Close()
		close(s.events)
		s.cancel() // release the goroutine watching ctx
		// Build final Response from accumulators.
		s.mu.Lock()
		if s.finalErr == nil && !s.cancelled {
			s.final = llm.Response{
				Content:      []llm.ContentPart{{Type: "text", Text: s.textBuf.String()}},
				ToolCalls:    s.toolCalls,
				FinishReason: s.finishStop,
				Usage:        s.usage,
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()

	scanner := bufio.NewScanner(s.resp.Body)
	// Anthropic sends frames that may exceed the default scanner buffer
	// (large tool inputs, big text deltas). 1 MiB is plenty for v1.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataBuf bytes.Buffer
	var lastEventType string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// End of one SSE record. Process accumulated data.
			if dataBuf.Len() > 0 {
				s.handleSSEData(lastEventType, dataBuf.Bytes())
				dataBuf.Reset()
				lastEventType = ""
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, ":"):
			// Comment / keepalive.
			continue
		case strings.HasPrefix(line, "event:"):
			lastEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		default:
			// Unknown SSE field — ignore (forward-compat).
		}
	}
	// Flush any trailing data record.
	if dataBuf.Len() > 0 {
		s.handleSSEData(lastEventType, dataBuf.Bytes())
	}
	if err := scanner.Err(); err != nil {
		// Mid-stream disconnect: signal a final error and emit a stream
		// error chunk so the audit pipeline records it. Per R3 these
		// are post-first-chunk failures and the retry middleware does
		// not see them — they arrive on the events channel.
		s.mu.Lock()
		switch {
		case s.cancelled:
			// Caller-driven cancel — keep finalErr nil so Final returns
			// ErrCancelled.
		case errors.Is(err, context.Canceled):
			s.finalErr = &llm.ErrCancelled{Reason: "context"}
		default:
			s.finalErr = &llm.ErrTransient{Message: "anthropic: stream read: " + err.Error(), Cause: err}
		}
		s.mu.Unlock()
		select {
		case s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}:
		default:
		}
	}
}

// handleSSEData parses one SSE record and emits the corresponding
// StreamEvents. The Messages streaming protocol is a sequence of typed
// events; we only inspect the JSON "type" field, which lets us ignore
// the SSE event-type header (it duplicates "type" in the payload).
//
// Reference: https://docs.anthropic.com/en/api/messages-streaming
func (s *stream) handleSSEData(_ string, raw []byte) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	if bytes.Equal(trimmed, []byte("[DONE]")) {
		return
	}
	var env struct {
		Type    string          `json:"type"`
		Index   int             `json:"index"`
		Message json.RawMessage `json:"message"`
		Error   *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		// Malformed frame; surface but don't poison the stream.
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "anthropic: malformed SSE frame: " + err.Error()}
		return
	}

	switch env.Type {
	case "message_start":
		// Usage may be present here with input tokens; record but don't
		// emit a usage event yet — Anthropic refines OutputTokens at
		// message_delta.
		if env.Message != nil {
			var ms struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(env.Message, &ms); err == nil && ms.Usage != nil {
				s.mu.Lock()
				s.usage.InputTokens = ms.Usage.InputTokens
				if s.usage.OutputTokens == 0 {
					s.usage.OutputTokens = ms.Usage.OutputTokens
				}
				s.mu.Unlock()
			}
		}
	case "content_block_start":
		// content_block_start declares the kind of the content block
		// being opened (text or tool_use). Capture tool_use metadata so
		// subsequent input_json_delta frames can be reassembled.
		var cb struct {
			ContentBlock struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(trimmed, &cb); err == nil {
			if cb.ContentBlock.Type == "tool_use" {
				s.mu.Lock()
				if s.toolPartial == nil {
					s.toolPartial = map[int]*toolPartialState{}
				}
				s.toolPartial[env.Index] = &toolPartialState{
					id:   cb.ContentBlock.ID,
					name: cb.ContentBlock.Name,
				}
				if len(cb.ContentBlock.Input) > 0 && string(cb.ContentBlock.Input) != "null" {
					s.toolPartial[env.Index].rawInput.Write(cb.ContentBlock.Input)
				}
				s.mu.Unlock()
			}
		}
	case "content_block_delta":
		var d struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(trimmed, &d); err != nil {
			return
		}
		switch d.Delta.Type {
		case "text_delta":
			if d.Delta.Text == "" {
				return
			}
			s.mu.Lock()
			s.textBuf.WriteString(d.Delta.Text)
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: d.Delta.Text, Raw: trimmed}
		case "input_json_delta":
			s.mu.Lock()
			if state, ok := s.toolPartial[env.Index]; ok && d.Delta.PartialJSON != "" {
				state.rawInput.WriteString(d.Delta.PartialJSON)
			}
			s.mu.Unlock()
		case "thinking_delta":
			if d.Delta.Thinking == "" {
				return
			}
			s.events <- llm.StreamEvent{
				Kind: llm.StreamReasoning,
				Reasoning: &llm.ReasoningBlock{
					Type:    "thinking",
					Content: d.Delta.Thinking,
				},
			}
		}
	case "content_block_stop":
		// Finalize a tool_use block: emit a StreamTool event with the
		// reassembled input.
		s.mu.Lock()
		state, ok := s.toolPartial[env.Index]
		if ok {
			delete(s.toolPartial, env.Index)
		}
		s.mu.Unlock()
		if ok {
			input := []byte(state.rawInput.String())
			if len(input) == 0 {
				input = []byte("{}")
			}
			tool := llm.ToolUse{ID: state.id, Name: state.name, Input: input}
			s.mu.Lock()
			s.toolCalls = append(s.toolCalls, tool)
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tool}
		}
	case "message_delta":
		// Final usage refinement and stop_reason.
		var md struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				CacheCreationInput int `json:"cache_creation_input_tokens"`
				CacheReadInput     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(trimmed, &md); err == nil {
			s.mu.Lock()
			if md.Delta.StopReason != "" {
				s.finishStop = md.Delta.StopReason
			}
			if md.Usage != nil {
				if md.Usage.InputTokens > 0 {
					s.usage.InputTokens = md.Usage.InputTokens
				}
				if md.Usage.OutputTokens > 0 {
					s.usage.OutputTokens = md.Usage.OutputTokens
				}
				if md.Usage.CacheReadInput > 0 {
					s.usage.CachedInputRead = md.Usage.CacheReadInput
				}
				if md.Usage.CacheCreationInput > 0 {
					s.usage.CachedInputWrite = md.Usage.CacheCreationInput
				}
				usage := s.usage
				s.mu.Unlock()
				s.events <- llm.StreamEvent{Kind: llm.StreamUsage, Usage: &usage}
			} else {
				s.mu.Unlock()
			}
		}
	case "message_stop":
		s.mu.Lock()
		finish := s.finishStop
		if finish == "" {
			finish = "end_turn"
			s.finishStop = finish
		}
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: finish}
	case "ping":
		// Keepalive — ignore.
	case "error":
		msg := "anthropic: stream error"
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Type + ": " + env.Error.Message
		}
		s.mu.Lock()
		s.finalErr = &llm.ErrTransient{Message: msg}
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: msg}
	default:
		// Unknown event types are forwarded as raw debugging frames.
		s.events <- llm.StreamEvent{Kind: llm.StreamEventKind(env.Type), Raw: trimmed}
	}
}
