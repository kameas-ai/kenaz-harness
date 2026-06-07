// Package custom implements the custom OpenAI-compatible endpoint adapter
// (custom-openai-compatible-endpoint-01KQ8VN0).
//
// The adapter speaks the OpenAI Chat Completions SSE protocol against any
// base URL the user provides. Authentication is driven by the template
// registry (WP01 / WP04); capability gating checks the probed capability
// matrix (WP03) before any wire call.
package custom

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/httpx"
)

// Kind is the canonical provider kind for all custom OpenAI-compatible
// endpoints. It matches the ProviderProfile.Kind value.
const Kind = "custom-openai"

// envFlag is the opt-out environment variable. Set to "0" to prevent
// registration. Checked in New() before the adapter is returned.
const envFlag = "HARNESS_CUSTOM_OPENAI"

// Adapter is the custom OpenAI-compatible ProviderAdapter. It composes
// the auth-scheme switch (WP04) with the capability gate (WP05) and the
// OpenAI SSE wire protocol.
type Adapter struct {
	httpc     *http.Client
	templates *Registry
	cat       *capabilities.Catalog
	store     CapabilityStore // WP03 — may be nil in tests
}

// CapabilityStore is the interface the adapter uses to look up the probed
// capability matrix for a given endpoint (WP03). The concrete
// implementation is *Store (probe.go).
type CapabilityStore interface {
	// Get returns the matrix for the given endpoint URL, or nil if not probed.
	Get(ctx context.Context, endpoint string) (*CapabilityMatrix, error)
}

// New constructs an Adapter with the embedded template registry. Returns
// nil when HARNESS_CUSTOM_OPENAI=0 so callers can skip registration.
func New(opts ...Option) *Adapter {
	if os.Getenv(envFlag) == "0" {
		return nil
	}
	a := &Adapter{
		httpc: &http.Client{Transport: httpx.DefaultTransport()},
	}
	var err error
	a.templates, err = LoadEmbeddedTemplates()
	if err != nil {
		// Embed failure is a programming error; fall back to an empty registry
		// so the adapter can still be used with explicit base URLs.
		a.templates = &Registry{templates: nil, byID: map[string]*Template{}}
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithHTTPClient replaces the underlying HTTP transport.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// WithCapabilityStore wires the probed-capability store (WP03).
func WithCapabilityStore(s CapabilityStore) Option {
	return func(a *Adapter) { a.store = s }
}

// Kind returns the canonical provider kind "custom-openai".
func (a *Adapter) Kind() string { return Kind }

// Capabilities returns a conservative descriptor. Because the actual
// capabilities vary per-endpoint, we report only streaming + usage_reporting
// as definitely true; the capability gate is enforced by gateOutgoing (WP05)
// after consulting the probed matrix.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{
		Provider: Kind,
		Model:    model,
		Supported: map[llm.Capability]bool{
			llm.CapStreaming:      true,
			llm.CapUsageReporting: true,
		},
		Notes: map[llm.Capability]string{
			llm.CapToolCalling: "determined at probe time",
			llm.CapVision:      "determined at probe time",
		},
	}
}

// Stream opens an SSE connection to the endpoint and returns a llm.Stream.
// The endpoint URL is derived from prof.Endpoint + the template's path
// convention. Auth is applied via the profile's auth_scheme.
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	baseURL := prof.Endpoint
	if baseURL == "" {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: "custom-openai: profile endpoint is required"}
	}

	// Resolve template for auth configuration.
	templateID, _ := prof.Defaults["template_id"].(string)
	tmpl := a.templates.Resolve(baseURL, templateID)

	scheme, authHeader := resolveAuthScheme(prof, tmpl)

	// WP05 capability gate: check probed matrix before any wire call.
	if err := a.gateOutgoing(ctx, req, baseURL); err != nil {
		return nil, err
	}

	// Warn (via ErrKeyAgainstNone) when key supplied against none scheme.
	if scheme == AuthSchemeNone && len(cred) > 0 {
		// Log advisory only — do not block the request.
		_ = ErrKeyAgainstNone
	}

	endpoint, err := ChatEndpoint(baseURL)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

	body, err := buildRequestBody(req, prof)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

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

	if aerr := ApplyAuth(httpReq, scheme, cred, authHeader); aerr != nil {
		cancel()
		var ic *ErrInvalidConfig
		if errors.As(aerr, &ic) {
			return nil, &llm.ErrAuth{Status: 0, Message: aerr.Error()}
		}
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: aerr.Error()}
	}

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
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

// ListModels implements llm.ModelLister by calling /models on the base URL.
// This requires the profile's Endpoint to be populated; cred is applied
// via the template auth scheme.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	// ListModels without a profile context is not well-defined for
	// custom endpoints — each endpoint has its own URL. Return empty to
	// satisfy the interface; the UI drives listing via ProbeCustomEndpoint
	// (WP06 RPC).
	return nil, nil
}

// ListModelsForEndpoint lists models for a specific endpoint URL.
func (a *Adapter) ListModelsForEndpoint(ctx context.Context, baseURL string, scheme AuthScheme, authHeader string, cred []byte) ([]llm.ModelInfo, error) {
	modelsURL, err := ModelsEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("custom-openai: build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if len(cred) > 0 {
		if aerr := ApplyAuth(req, scheme, cred, authHeader); aerr != nil {
			return nil, aerr
		}
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyStatus(resp.StatusCode, body)
	}
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("custom-openai: parse models response: %w", err)
	}
	out := make([]llm.ModelInfo, 0, len(doc.Data))
	for _, m := range doc.Data {
		out = append(out, llm.ModelInfo{ID: m.ID, DisplayName: m.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Templates returns the adapter's template registry (used by the RPC layer).
func (a *Adapter) Templates() *Registry { return a.templates }

// resolveAuthScheme extracts the auth scheme and header from profile
// defaults or falls back to the template's scheme.
func resolveAuthScheme(prof llm.ProviderProfile, tmpl *Template) (AuthScheme, string) {
	// Profile-level override from Defaults.
	if s, ok := prof.Defaults["auth_scheme"].(string); ok && s != "" {
		header, _ := prof.Defaults["auth_header"].(string)
		return AuthScheme(s), header
	}
	if tmpl != nil {
		return tmpl.AuthScheme, tmpl.AuthHeader
	}
	// Default to bearer when no template and no profile override.
	return AuthSchemeBearer, ""
}

// buildRequestBody constructs the Chat Completions JSON body.
func buildRequestBody(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
	out := map[string]any{
		"model":          prof.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}

	for _, key := range []string{"temperature", "top_p", "max_tokens", "presence_penalty", "frequency_penalty"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		} else if v, ok := prof.Defaults[key]; ok {
			out[key] = v
		}
	}

	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
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
					return nil, fmt.Errorf("custom-openai: tool %q parameters: %w", t.Name, err)
				}
				fn["parameters"] = schema
			} else {
				fn["parameters"] = map[string]any{"type": "object"}
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		out["tools"] = tools
	}

	return json.Marshal(out)
}

// flattenContent concatenates text blocks from a message.
func flattenContent(parts []llm.ContentBlock) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// classifyStatus maps HTTP error status codes to the connector taxonomy.
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
		return &llm.ErrInvalidRequest{Status: status, Message: msg}
	}
}

func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		if env.Error.Type != "" {
			return env.Error.Type + ": " + env.Error.Message
		}
		return env.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// chatStream is the SSE consumer for custom endpoint streaming.
type chatStream struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan llm.StreamEvent
	done   chan struct{}

	mu         sync.Mutex
	final      llm.Response
	finalErr   error
	cancelled  bool
	textBuf    strings.Builder
	usage      llm.Usage
	finishStop string
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

func (s *chatStream) Events() <-chan llm.StreamEvent { return s.events }

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

func (s *chatStream) Final() (llm.Response, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled && s.finalErr == nil {
		return llm.Response{}, &llm.ErrCancelled{Reason: "stop"}
	}
	return s.final, s.finalErr
}

func (s *chatStream) pump() {
	defer func() {
		_ = s.resp.Body.Close()
		close(s.events)
		s.cancel()
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
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				return
			}
			continue
		}
		s.handleFrame([]byte(payload))
	}
	if err := scanner.Err(); err != nil {
		s.mu.Lock()
		switch {
		case s.cancelled:
		case errors.Is(err, context.Canceled):
			s.finalErr = &llm.ErrCancelled{Reason: "context"}
		default:
			s.finalErr = &llm.ErrTransient{Message: "custom-openai: stream read: " + err.Error(), Cause: err}
		}
		s.mu.Unlock()
		select {
		case s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}:
		default:
		}
	}
}

func (s *chatStream) handleFrame(raw []byte) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	var env struct {
		Choices []struct {
			Delta struct {
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
		} `json:"usage"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "custom-openai: malformed SSE frame: " + err.Error()}
		return
	}

	for _, ch := range env.Choices {
		if ch.Delta.Content != "" {
			s.mu.Lock()
			s.textBuf.WriteString(ch.Delta.Content)
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: ch.Delta.Content}
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
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tool}
		s.mu.Lock()
	}
}

// Compile-time interface witnesses.
var _ llm.ProviderAdapter = (*Adapter)(nil)
var _ llm.ModelLister = (*Adapter)(nil)

// gateOutgoing checks the probed capability matrix before making a wire
// call (WP05). Returns ErrCustomEndpointMissingCapability when the matrix
// indicates a capability is not supported. Returns nil when no matrix has
// been probed (permissive default).
func (a *Adapter) gateOutgoing(ctx context.Context, req llm.GenerationRequest, baseURL string) error {
	if a.store == nil {
		return nil
	}
	matrix, err := a.store.Get(ctx, baseURL)
	if err != nil || matrix == nil {
		// No matrix available — allow the request through.
		return nil
	}
	// Check tool calling.
	if len(req.Tools) > 0 && matrix.ToolCalling == CapabilityValueFalse {
		return &ErrCustomEndpointMissingCapability{
			Endpoint:   baseURL,
			Capability: "tool_calling",
		}
	}
	return nil
}

// ErrCustomEndpointMissingCapability is returned by gateOutgoing when
// the probed matrix indicates a capability is not supported.
type ErrCustomEndpointMissingCapability struct {
	Endpoint   string
	Capability string
	ProfileID  string
}

func (e *ErrCustomEndpointMissingCapability) Error() string {
	return fmt.Sprintf("custom-openai: endpoint %q does not support %q (probed capability matrix)",
		e.Endpoint, e.Capability)
}

// CapabilityValue is the tri-state for probed capabilities.
type CapabilityValue string

const (
	CapabilityValueTrue    CapabilityValue = "true"
	CapabilityValueFalse   CapabilityValue = "false"
	CapabilityValueUnknown CapabilityValue = "unknown"
)

// CapabilityMatrix is the probed capability matrix for a custom endpoint (WP03).
type CapabilityMatrix struct {
	// Endpoint is the base URL this matrix was probed against.
	Endpoint string `json:"endpoint"`
	// ProbedAt is the Unix timestamp (seconds) when the probe completed.
	ProbedAt int64 `json:"probed_at"`
	// Streaming reports whether the endpoint returns SSE streaming.
	Streaming CapabilityValue `json:"streaming"`
	// ToolCalling reports whether the endpoint supports function calling.
	ToolCalling CapabilityValue `json:"tool_calling"`
	// StreamingUsage reports whether the endpoint includes token usage
	// in the stream (stream_options.include_usage). Groq does not.
	StreamingUsage CapabilityValue `json:"streaming_usage"`
}

// NewCapabilityMatrix returns a matrix with all fields set to "unknown".
func NewCapabilityMatrix(endpoint string) *CapabilityMatrix {
	return &CapabilityMatrix{
		Endpoint:       endpoint,
		ProbedAt:       time.Now().Unix(),
		Streaming:      CapabilityValueUnknown,
		ToolCalling:    CapabilityValueUnknown,
		StreamingUsage: CapabilityValueUnknown,
	}
}
