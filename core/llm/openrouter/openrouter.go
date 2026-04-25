// Package openrouter is the OpenRouter streaming adapter.
//
// OpenRouter exposes an OpenAI-compatible Chat Completions API at
// https://openrouter.ai/api/v1. From the adapter's point of view it
// looks almost identical to a vanilla OpenAI adapter — the only
// differences are:
//
//  1. The base URL points at openrouter.ai.
//  2. Two optional ranking headers (HTTP-Referer, X-Title) identify
//     the calling application on the OpenRouter dashboard.
//  3. The /models endpoint returns a richer payload (id, name,
//     description, context_length, pricing) suitable for populating
//     the AddProvider model picker.
//
// Per-model capabilities (vision, tool calls, reasoning) are decided
// upstream by OpenRouter based on the routed model. The adapter
// surfaces a permissive baseline (streaming + tool calling + usage)
// and defers refinement to the upstream router.
package openrouter

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

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Kind is the canonical provider kind ("openrouter").
const Kind = "openrouter"

const (
	defaultEndpoint    = "https://openrouter.ai/api/v1/chat/completions"
	defaultModelsURL   = "https://openrouter.ai/api/v1/models"
	defaultReferer     = "https://github.com/sigil-tech/kaneaz-harness"
	defaultAppTitle    = "kaneaz-harness"
	errorBodyByteLimit = 4096
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

// WithEndpoint overrides the chat-completions URL. Useful for fixtures
// served from httptest and for self-hosted gateways that proxy the
// OpenRouter surface.
func WithEndpoint(u string) Option {
	return func(a *Adapter) {
		if u != "" {
			a.endpoint = u
		}
	}
}

// WithReferer overrides the HTTP-Referer ranking header value. By
// default the adapter advertises the kaneaz-harness GitHub URL.
func WithReferer(s string) Option {
	return func(a *Adapter) {
		if s != "" {
			a.referer = s
		}
	}
}

// WithAppTitle overrides the X-Title ranking header value. By default
// the adapter advertises "kaneaz-harness".
func WithAppTitle(s string) Option {
	return func(a *Adapter) {
		if s != "" {
			a.appTitle = s
		}
	}
}

// Adapter implements llm.ProviderAdapter and llm.ModelLister against
// the OpenRouter API.
type Adapter struct {
	httpc    *http.Client
	endpoint string
	referer  string
	appTitle string
}

// New constructs an Adapter with the OpenRouter defaults.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		httpc:    &http.Client{}, // no Timeout — context drives lifetime
		endpoint: defaultEndpoint,
		referer:  defaultReferer,
		appTitle: defaultAppTitle,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind ("openrouter").
func (a *Adapter) Kind() string { return Kind }

// Capabilities reports the per-model descriptor. OpenRouter routes to
// a wide and changing set of upstream providers; the adapter advertises
// a permissive baseline (streaming + tool calling + usage_reporting)
// and lets the upstream router enforce per-model restrictions on the
// wire.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{
		Provider: Kind,
		Model:    model,
		Supported: map[llm.Capability]bool{
			llm.CapStreaming:      true,
			llm.CapToolCalling:    true,
			llm.CapUsageReporting: true,
		},
	}
}

// Compile-time assertions: *Adapter satisfies llm.ProviderAdapter and
// llm.ModelLister.
var (
	_ llm.ProviderAdapter = (*Adapter)(nil)
	_ llm.ModelLister     = (*Adapter)(nil)
)

// Stream opens an SSE connection to the chat-completions endpoint and
// returns a llm.Stream that pumps StreamEvent values to the caller.
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Message: "openrouter: empty credential"}
	}

	body, err := buildRequestBody(req, prof)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

	endpoint := a.endpoint
	if prof.Endpoint != "" {
		endpoint = prof.Endpoint
	}

	// Per-call cancellation handle: ctx threads into the transport, but
	// Cancel() must work even after ctx scope ends. We derive a per-call
	// ctx from context.Background() and cancel it on either ctx.Done or
	// explicit Cancel.
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
	httpReq.Header.Set("Authorization", "Bearer "+string(cred))
	if a.referer != "" {
		httpReq.Header.Set("HTTP-Referer", a.referer)
	}
	if a.appTitle != "" {
		httpReq.Header.Set("X-Title", a.appTitle)
	}

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}

	if resp.StatusCode/100 != 2 {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyByteLimit))
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

// ListModels implements llm.ModelLister. It calls OpenRouter's /models
// endpoint with the supplied API key and returns the parsed list. The
// caller (rpc layer) zeros the cred buffer before this method's frame
// returns so the plaintext key never lingers.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Message: "openrouter: empty credential"}
	}
	url := modelsURL(a.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: build models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(cred))
	req.Header.Set("Accept", "application/json")
	if a.referer != "" {
		req.Header.Set("HTTP-Referer", a.referer)
	}
	if a.appTitle != "" {
		req.Header.Set("X-Title", a.appTitle)
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyByteLimit))
		return nil, classifyStatus(resp.StatusCode, body)
	}
	var doc modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("openrouter: parse models response: %w", err)
	}
	out := make([]llm.ModelInfo, 0, len(doc.Data))
	for _, m := range doc.Data {
		display := m.Name
		if display == "" {
			display = m.ID
		}
		out = append(out, llm.ModelInfo{
			ID:          m.ID,
			DisplayName: display,
			Description: m.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	return out, nil
}

// modelsResponse mirrors the JSON shape returned by /api/v1/models.
type modelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		ContextLength int    `json:"context_length"`
	} `json:"data"`
}

// modelsURL derives the /models URL from the configured chat URL. The
// production endpoint ends in "/chat/completions" → ".../models".
// Custom endpoints that don't end in that suffix get "/models" appended
// to the trimmed host.
func modelsURL(chatURL string) string {
	if strings.HasSuffix(chatURL, "/chat/completions") {
		return strings.TrimSuffix(chatURL, "/chat/completions") + "/models"
	}
	return strings.TrimRight(chatURL, "/") + "/models"
}

// classifyStatus maps an HTTP error response to the connector taxonomy.
//
//   - 401 / 403       → ErrAuth          (non-retryable)
//   - 400 / 404 / 422 → ErrInvalidRequest (non-retryable)
//   - 429             → ErrTransient
//   - 5xx             → ErrTransient
//   - everything else → ErrInvalidRequest (defensive default)
func classifyStatus(status int, body []byte) error {
	msg := extractErrorMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &llm.ErrAuth{Status: status, Message: msg}
	case status == 429:
		return &llm.ErrTransient{Status: 429, Message: msg}
	case status >= 500 && status < 600:
		return &llm.ErrTransient{Status: status, Message: msg}
	case status == 400 || status == 404 || status == 422:
		return &llm.ErrInvalidRequest{Status: status, Message: msg}
	default:
		return &llm.ErrInvalidRequest{Status: status, Message: msg}
	}
}

// extractErrorMessage best-effort parses an OpenRouter / OpenAI-style
// error envelope. The OpenAI shape is {"error":{"message":"...","type":"...","code":"..."}}.
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error.Message != "" {
			if env.Error.Type != "" {
				return env.Error.Type + ": " + env.Error.Message
			}
			return env.Error.Message
		}
		if env.Message != "" {
			return env.Message
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// buildRequestBody constructs the JSON body for the chat-completions
// API. OpenRouter accepts the canonical OpenAI shape:
//
//	{
//	  "model": "anthropic/claude-3.5-sonnet",
//	  "messages": [{"role":"user","content":"hi"}, ...],
//	  "stream": true,
//	  "usage": {"include": true}
//	}
//
// Connector ContentParts are flattened into a single string per message
// (text concatenation). Non-text parts are dropped — OpenRouter's
// router accepts richer content arrays per upstream model, but the v1
// adapter targets the universal text path.
func buildRequestBody(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
	out := map[string]any{
		"model":  prof.Model,
		"stream": true,
		"usage":  map[string]any{"include": true},
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
		msgs = append(msgs, map[string]any{
			"role":    role,
			"content": flattenContent(m.Content),
		})
	}
	out["messages"] = msgs

	// Optional sampling knobs surfaced through Params / Defaults.
	for _, key := range []string{"temperature", "top_p", "max_tokens"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		} else if v, ok := prof.Defaults[key]; ok {
			out[key] = v
		}
	}

	return json.Marshal(out)
}

// flattenContent concatenates the text fragments of a content slice
// into a single string. Tool / attachment fragments are dropped on the
// v1 adapter — tool calling round-trips through the upstream model's
// own contract, and OpenRouter accepts the OpenAI tool-call schema for
// callers that need it (out of scope for the streaming text MVP).
func flattenContent(parts []llm.ContentPart) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// chatStream is the SSE consumer. It owns the http.Response body and
// the cancellation handle. Pump runs in a goroutine; events are exposed
// via Events().
type chatStream struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan llm.StreamEvent
	done   chan struct{}

	mu        sync.Mutex
	final     llm.Response
	finalErr  error
	cancelled bool

	// accumulators populated as the SSE pump runs.
	textBuf    strings.Builder
	usage      llm.Usage
	finishStop string
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
func (s *chatStream) pump() {
	defer func() {
		_ = s.resp.Body.Close()
		close(s.events)
		s.cancel() // release the goroutine watching ctx
		s.mu.Lock()
		if s.finalErr == nil && !s.cancelled {
			s.final = llm.Response{
				Content:      []llm.ContentPart{{Type: "text", Text: s.textBuf.String()}},
				FinishReason: s.finishStop,
				Usage:        s.usage,
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()

	scanner := bufio.NewScanner(s.resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataBuf bytes.Buffer

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// End of one SSE record. Process accumulated data.
			if dataBuf.Len() > 0 {
				s.handleSSEData(dataBuf.Bytes())
				dataBuf.Reset()
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, ":"):
			// Comment / keepalive ("ping") frame — silently skip.
			continue
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		case strings.HasPrefix(line, "event:"):
			// OpenRouter does not rely on event-name framing; the JSON
			// payload is self-describing. Ignored for forward-compat.
		default:
			// Unknown SSE field — ignore (forward-compat).
		}
	}
	// Flush any trailing data record.
	if dataBuf.Len() > 0 {
		s.handleSSEData(dataBuf.Bytes())
	}
	if err := scanner.Err(); err != nil {
		s.mu.Lock()
		switch {
		case s.cancelled:
			// Caller-driven cancel — keep finalErr nil so Final returns
			// ErrCancelled.
		case errors.Is(err, context.Canceled):
			s.finalErr = &llm.ErrCancelled{Reason: "context"}
		default:
			s.finalErr = &llm.ErrTransient{Message: "openrouter: stream read: " + err.Error(), Cause: err}
		}
		s.mu.Unlock()
		select {
		case s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}:
		default:
		}
	}
}

// handleSSEData parses one SSE record and emits the corresponding
// StreamEvents. The OpenAI-compatible streaming protocol is a sequence
// of JSON chunks of shape:
//
//	{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}
//
// Final frame: literal "[DONE]". Usage frames carry an empty choices
// array and a populated usage object.
func (s *chatStream) handleSSEData(raw []byte) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	if bytes.Equal(trimmed, []byte("[DONE]")) {
		return
	}
	var env struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
				Role    string `json:"role"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "openrouter: malformed SSE frame: " + err.Error()}
		return
	}

	// Mid-stream error frame (rare, but OpenRouter forwards upstream
	// failures this way).
	if env.Error != nil && env.Error.Message != "" {
		msg := env.Error.Message
		if env.Error.Type != "" {
			msg = env.Error.Type + ": " + msg
		}
		s.mu.Lock()
		s.finalErr = &llm.ErrTransient{Message: msg}
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: msg}
		return
	}

	// Text deltas and finish reasons travel through choices[].
	for _, c := range env.Choices {
		if c.Delta.Content != "" {
			s.mu.Lock()
			s.textBuf.WriteString(c.Delta.Content)
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: c.Delta.Content, Raw: trimmed}
		}
		if c.FinishReason != nil && *c.FinishReason != "" {
			s.mu.Lock()
			s.finishStop = *c.FinishReason
			finish := s.finishStop
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: finish}
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
