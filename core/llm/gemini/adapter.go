package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/httpx"
)

// Kind is the canonical provider kind for the Gemini adapter.
const Kind = "gemini"

// EnvFlag is the environment variable that gates Gemini adapter registration.
// Set to "on" (default) or "1" to enable; "off" or "0" to disable.
const EnvFlag = "HARNESS_GOOGLE_GEMINI"

// IsEnabled reports whether the Gemini adapter is enabled by the env flag.
// Default is enabled (HARNESS_GOOGLE_GEMINI unset or "on"/"1").
func IsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvFlag)))
	return v != "off" && v != "0" && v != "false"
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// WithCatalog overrides the capability catalog (for testing).
func WithCatalog(cat *capabilities.Catalog) Option {
	return func(a *Adapter) {
		if cat != nil {
			a.cat = cat
		}
	}
}

// Adapter is the Gemini ProviderAdapter implementation.
type Adapter struct {
	httpc *http.Client
	cat   *capabilities.Catalog
}

// New constructs a Gemini Adapter.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		httpc: &http.Client{Transport: httpx.DefaultTransport()},
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind ("gemini").
func (a *Adapter) Kind() string { return Kind }

// Capabilities returns the (provider, model) descriptor from the catalog.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	if a.cat != nil {
		return a.cat.Describe(Kind, model)
	}
	// Conservative fallback when catalog is unavailable.
	return llm.CapabilityDescriptor{
		Provider: Kind,
		Model:    model,
		Supported: map[llm.Capability]bool{
			llm.CapStreaming:      true,
			llm.CapToolCalling:   true,
			llm.CapVision:        true,
			llm.CapUsageReporting: true,
			llm.CapCancellation:  true,
		},
	}
}

// Stream opens an SSE connection to the Gemini API and returns a Stream.
//
// The cred bytes carry either:
//   - API key bytes (for AI Studio endpoint kind)
//   - Service-account JSON bytes (for Vertex endpoint kind with SA auth)
//   - Empty slice (for Vertex endpoint kind with ADC auth)
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	// Resolve endpoint URL and authenticator.
	endpointURL, auth, err := a.resolveEndpoint(prof, cred)
	if err != nil {
		return nil, err
	}

	// Build the Gemini wire request.
	gr, err := ToGeminiRequest(req, prof)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

	body, err := json.Marshal(gr)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: "gemini: marshal request: " + err.Error()}
	}

	// Per-call cancellation.
	streamCtx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-streamCtx.Done():
		}
	}()

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	if err := auth.Authorize(httpReq); err != nil {
		cancel()
		return nil, &llm.ErrAuth{Status: 0, Message: "gemini: auth: " + err.Error()}
	}

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
		return nil, &llm.ErrTransient{Status: 0, Message: "gemini: http: " + err.Error(), Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, classifyStatus(resp.StatusCode, bodySnippet)
	}

	s := &geminiStream{
		resp:   resp,
		cancel: cancel,
		events: make(chan llm.StreamEvent, 16),
		done:   make(chan struct{}),
	}
	go s.pump()
	return s, nil
}

// resolveEndpoint builds the stream URL and authenticator from the profile.
func (a *Adapter) resolveEndpoint(prof llm.ProviderProfile, cred []byte) (string, Authenticator, error) {
	kind := EndpointKind(prof.GeminiEndpointKind)
	if kind == "" {
		kind = EndpointKindAIStudio // default
	}

	switch kind {
	case EndpointKindAIStudio:
		if len(cred) == 0 {
			return "", nil, &llm.ErrAuth{Status: 0, Message: "gemini: AI Studio requires an API key"}
		}
		u, err := AIStudioStreamURL(prof.Model)
		if err != nil {
			return "", nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
		}
		return u, newAPIKeyAuth(cred), nil

	case EndpointKindVertex:
		u, err := VertexStreamURL(prof.Model, prof.Project, prof.Region)
		if err != nil {
			return "", nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
		}
		// Determine auth: SA JSON or ADC.
		if len(cred) > 0 {
			saAuth, err := newServiceAccountAuth(cred)
			if err != nil {
				return "", nil, &llm.ErrAuth{Status: 0, Message: "gemini: " + err.Error()}
			}
			return u, saAuth, nil
		}
		return u, newADCAuth(), nil

	default:
		return "", nil, &llm.ErrInvalidRequest{
			Status:  0,
			Message: fmt.Sprintf("gemini: unknown endpoint kind %q (must be ai_studio or vertex)", kind),
		}
	}
}

// TestKey implements llm.KeyTester. It verifies a credential is accepted
// by the provider without modifying state. Uses the /models endpoint.
func (a *Adapter) TestKey(ctx context.Context, cred []byte) error {
	// Apply a 5-second deadline per the KeyTester contract.
	ctx, cancel := context.WithTimeout(ctx, 5e9) // 5 seconds
	defer cancel()

	if len(cred) == 0 {
		return &llm.ErrAuth{Status: 0, Message: "gemini: empty credential"}
	}

	u := AIStudioModelsURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	auth := newAPIKeyAuth(cred)
	_ = auth.Authorize(req)

	resp, err := a.httpc.Do(req)
	if err != nil {
		return &llm.ErrTransient{Status: 0, Message: "gemini: test key: " + err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return classifyStatus(resp.StatusCode, body)
}

// ListModels implements llm.ModelLister. Lists available Gemini models
// for the AI Studio endpoint using the provided API key.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10e9)
	defer cancel()

	u := AIStudioModelsURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	auth := newAPIKeyAuth(cred)
	_ = auth.Authorize(req)

	resp, err := a.httpc.Do(req)
	if err != nil {
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyStatus(resp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB
	if err != nil {
		return nil, &llm.ErrTransient{Status: 0, Message: "gemini: read models: " + err.Error()}
	}

	var envelope struct {
		Models []struct {
			Name              string `json:"name"`
			DisplayName       string `json:"displayName"`
			Description       string `json:"description"`
			InputTokenLimit   int    `json:"inputTokenLimit"`
			OutputTokenLimit  int    `json:"outputTokenLimit"`
			SupportedMethods  []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: "gemini: parse models: " + err.Error()}
	}

	out := make([]llm.ModelInfo, 0, len(envelope.Models))
	for _, m := range envelope.Models {
		// Only include models that support generateContent.
		supportsGenerate := false
		for _, method := range m.SupportedMethods {
			if method == "generateContent" || method == "streamGenerateContent" {
				supportsGenerate = true
				break
			}
		}
		if !supportsGenerate {
			continue
		}
		// Strip the "models/" prefix from the model name.
		id := strings.TrimPrefix(m.Name, "models/")
		out = append(out, llm.ModelInfo{
			ID:              id,
			DisplayName:     m.DisplayName,
			Description:     m.Description,
			ContextWindow:   m.InputTokenLimit,
			MaxOutputTokens: m.OutputTokenLimit,
		})
	}
	return out, nil
}

// ─── geminiStream ─────────────────────────────────────────────────────────────

// geminiStream consumes the SSE response body and emits StreamEvents.
type geminiStream struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan llm.StreamEvent
	done   chan struct{}

	mu          sync.Mutex
	final       llm.Response
	finalErr    error
	cancelled   bool
	textBuf     strings.Builder
	usage       llm.Usage
	finishStop  string
	toolCalls   []llm.ToolUse
	toolCounter int
}

func (s *geminiStream) Events() <-chan llm.StreamEvent { return s.events }

func (s *geminiStream) Cancel() error {
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

func (s *geminiStream) Final() (llm.Response, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled && s.finalErr == nil {
		return llm.Response{}, &llm.ErrCancelled{Reason: "stop"}
	}
	return s.final, s.finalErr
}

// pump reads SSE frames from the Gemini streaming response.
//
// Gemini's SSE format:
//
//	data: {"candidates":[...],"usageMetadata":{...}}
//	data: {"candidates":[...],"usageMetadata":{...}}
//	(stream ends when the HTTP body is exhausted)
func (s *geminiStream) pump() {
	defer func() {
		_ = s.resp.Body.Close()
		close(s.events)
		s.cancel()
		s.mu.Lock()
		if s.finalErr == nil && !s.cancelled {
			content := []llm.ContentBlock{}
			if text := s.textBuf.String(); text != "" {
				content = append(content, llm.ContentBlock{Type: "text", Text: text})
			}
			s.final = llm.Response{
				Content:      content,
				ToolCalls:    s.toolCalls,
				FinishReason: s.finishStop,
				Usage:        s.usage,
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()

	buf := make([]byte, 0, 64*1024)
	readBuf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(readBuf)
		if n > 0 {
			buf = append(buf, readBuf[:n]...)
			buf = s.processBuffer(buf)
		}
		if err != nil {
			if err == io.EOF {
				// Flush any remaining buffered data.
				_ = s.processBuffer(buf)
				return
			}
			s.mu.Lock()
			cancelled := s.cancelled
			s.mu.Unlock()
			if cancelled {
				return
			}
			s.mu.Lock()
			s.finalErr = &llm.ErrTransient{Message: "gemini: stream read: " + err.Error(), Cause: err}
			s.mu.Unlock()
			select {
			case s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}:
			default:
			}
			return
		}
	}
}

// processBuffer scans buf for complete SSE frames and processes them.
// Returns the unconsumed portion of buf.
func (s *geminiStream) processBuffer(buf []byte) []byte {
	for {
		// Find the next newline-terminated line.
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			return buf
		}
		line := buf[:idx]
		buf = buf[idx+1:]

		// Trim carriage return if present.
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte(":")) {
			continue // comment / keepalive
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data:"))
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 {
			continue
		}
		s.handleFrame(payload)
	}
}

// handleFrame processes one SSE data payload.
func (s *geminiStream) handleFrame(raw []byte) {
	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "gemini: malformed SSE frame: " + err.Error()}
		return
	}

	// Update usage from every frame; last frame wins (Gemini accumulates).
	if gr.UsageMetadata != nil {
		s.mu.Lock()
		s.usage = llm.Usage{
			InputTokens:     gr.UsageMetadata.PromptTokenCount,
			OutputTokens:    gr.UsageMetadata.CandidatesTokenCount,
			CachedInputRead: gr.UsageMetadata.CachedContentTokenCount,
			ReasoningTokens: gr.UsageMetadata.ThoughtsTokenCount,
		}
		s.mu.Unlock()
	}

	for _, cand := range gr.Candidates {
		if cand.FinishReason != "" {
			s.mu.Lock()
			s.finishStop = strings.ToLower(cand.FinishReason)
			s.mu.Unlock()
			s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: s.finishStop}
		}
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			switch {
			case part.Text != "":
				s.mu.Lock()
				s.textBuf.WriteString(part.Text)
				s.mu.Unlock()
				s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: part.Text, Raw: raw}

			case part.FunctionCall != nil:
				s.mu.Lock()
				id := fmt.Sprintf("call_%d", s.toolCounter)
				s.toolCounter++
				tu := llm.ToolUse{
					ID:    id,
					Name:  part.FunctionCall.Name,
					Input: part.FunctionCall.Args,
				}
				s.toolCalls = append(s.toolCalls, tu)
				s.mu.Unlock()
				s.events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tu, Raw: raw}
			}
		}
	}
}

// Compile-time assertions.
var _ llm.ProviderAdapter = (*Adapter)(nil)
var _ llm.ModelLister = (*Adapter)(nil)
var _ llm.KeyTester = (*Adapter)(nil)
