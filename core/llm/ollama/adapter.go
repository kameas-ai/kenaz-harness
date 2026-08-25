// Package ollama implements a real adapter kind for local Ollama
// installs (model-settings-reach-the-model-01PMZ101 WP13, register
// D-1: "SHIP A REAL ollama ADAPTER KIND").
//
// Before this package existed every local Ollama install was persisted
// as ProviderProfile{Kind: "custom-openai"} (core/llm/localruntime and
// core/rpc/views/llm/impl_local_runtime.go), which is a real fix for
// the tool-calling P0 (custom-openai.yaml, WP02) but reports Ollama's
// capabilities generically rather than accurately. Per register G-6
// (2026-08-22, Part 10): "register the new kind, leave existing
// custom-openai profiles alone. NO migration." Existing profiles are
// untouched by this package; only a NEW profile with Kind: "ollama"
// resolves here.
//
// The wire protocol is the same OpenAI-compatible Chat Completions SSE
// format custom-openai and azure-openai speak — Ollama has served
// /v1/chat/completions since v0.1.24 — so this adapter routes bodies
// through the shared core/llm/openaiwire encoder (the same DRY
// reasoning as WP03's routing of azure/custom) rather than writing a
// fourth private encoder. What makes this a distinct adapter kind
// rather than another custom-openai endpoint is the capability
// resolution: core/llm/capabilities/data/ollama.yaml describes what
// Ollama models actually support (correctly, as of WP13 — see D-15),
// and ProviderCapabilities (WP14) probes the live daemon's /api/show
// endpoint for the per-model ground truth instead of relying solely on
// the static curated table.
package ollama

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

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/httpx"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
)

// Kind is the canonical provider kind for the Ollama adapter. It must
// match the ProviderProfile.Kind value a NEW Ollama profile is created
// with; no existing "custom-openai" profile ever carries this value
// (register G-6 — no migration).
const Kind = "ollama"

// envFlag is the opt-out environment variable, mirroring the
// custom-openai and azure-openai conditional-registration pattern in
// core/llm/registry/registry.go. Set to "0" to prevent registration.
const envFlag = "HARNESS_OLLAMA"

// DefaultBaseURL is used when a profile's Endpoint is empty — Ollama's
// own default bind address for a local install.
const DefaultBaseURL = "http://localhost:11434"

// Adapter is the Ollama ProviderAdapter.
type Adapter struct {
	httpc   *http.Client
	cat     *capabilities.Catalog
	probeAt string // base URL used by ListModels / ProviderCapabilities, which have no ProviderProfile to read an Endpoint from
}

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithHTTPClient overrides the HTTP client. Used by tests.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// WithCatalog overrides the capability catalog. Used by tests.
func WithCatalog(cat *capabilities.Catalog) Option {
	return func(a *Adapter) {
		if cat != nil {
			a.cat = cat
		}
	}
}

// WithProbeBaseURL overrides the base URL ListModels and
// ProviderCapabilities probe against — both take no ProviderProfile
// (llm.ModelLister and llm.CapabilitiesProvider are
// profile-agnostic interfaces), so they cannot resolve a per-profile
// Endpoint the way Stream does. Defaults to DefaultBaseURL. Used by
// tests to point the probe at an httptest server.
func WithProbeBaseURL(base string) Option {
	return func(a *Adapter) {
		if base != "" {
			a.probeAt = base
		}
	}
}

// New constructs an Adapter. Returns nil when HARNESS_OLLAMA=0 so
// callers (registry.New) can skip registration, matching the
// custom-openai / azure-openai opt-out pattern.
func New(opts ...Option) *Adapter {
	if os.Getenv(envFlag) == "0" {
		return nil
	}
	a := &Adapter{
		httpc:   &http.Client{Transport: httpx.DefaultTransport()},
		probeAt: DefaultBaseURL,
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns "ollama".
func (a *Adapter) Kind() string { return Kind }

// Capabilities returns the curated static descriptor for model from
// the embedded ollama.yaml catalog. The capability GATE (which the
// registry actually calls before every Stream) resolves the same way
// through Catalog.Describe(prof.Kind, prof.Model); this method exists
// to satisfy the synchronous llm.ProviderAdapter shim for callers that
// don't have a *capabilities.Catalog handy.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	if a.cat == nil {
		return llm.CapabilityDescriptor{
			Provider:  Kind,
			Model:     model,
			Supported: map[llm.Capability]bool{llm.CapStreaming: true, llm.CapUsageReporting: true},
		}
	}
	return a.cat.Describe(Kind, model)
}

func baseURL(prof llm.ProviderProfile) string {
	return resolveBase(prof.Endpoint)
}

// resolveBase applies the same "empty means DefaultBaseURL" fallback
// Stream uses via baseURL, factored out so ProviderCapabilitiesAt (H4
// fix) can apply it to an endpoint string that did not come from a
// ProviderProfile.
func resolveBase(endpoint string) string {
	if strings.TrimSpace(endpoint) != "" {
		return strings.TrimRight(endpoint, "/")
	}
	return DefaultBaseURL
}

// Stream opens an SSE connection to the Ollama daemon's OpenAI-
// compatible Chat Completions endpoint.
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	base := baseURL(prof)
	model := prof.Model
	if req.Model != "" {
		model = req.Model
	}

	body, err := openaiwire.BuildRequestBody(req, model, prof.Defaults)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	payload, err := json.Marshal(body)
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

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	// Most local Ollama installs run unauthenticated; a bearer token is
	// applied when the profile carries one (e.g. Ollama behind a
	// reverse proxy) so the adapter doesn't hard-refuse that setup.
	if len(cred) > 0 {
		httpReq.Header.Set("Authorization", "Bearer "+string(cred))
	}

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, classifyStatus(resp.StatusCode, snippet)
	}

	s := &chatStream{resp: resp, cancel: cancel, events: make(chan llm.StreamEvent, 16), done: make(chan struct{})}
	go s.pump()
	return s, nil
}

// ollamaTagsResponse mirrors GET /api/tags — Ollama's native model
// listing endpoint (distinct from the OpenAI-compat /v1/models shim,
// which some Ollama builds don't serve).
type ollamaTagsResponse struct {
	Models []struct {
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		ModifiedAt string `json:"modified_at"`
	} `json:"models"`
}

// ListModels implements llm.ModelLister via Ollama's native /api/tags.
// cred is unused for the default unauthenticated local case; a bearer
// token is applied when supplied, matching Stream.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	return a.listModelsAt(ctx, a.probeAt, cred)
}

func (a *Adapter) listModelsAt(ctx context.Context, base string, cred []byte) ([]llm.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if len(cred) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(cred))
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyStatus(resp.StatusCode, respBody)
	}
	var doc ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("ollama: parse /api/tags response: %w", err)
	}
	out := make([]llm.ModelInfo, 0, len(doc.Models))
	for _, m := range doc.Models {
		out = append(out, llm.ModelInfo{ID: m.Name, DisplayName: m.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ollamaShowResponse mirrors the fields of POST /api/show this adapter
// reads. Modern Ollama (0.4+) returns a top-level "capabilities" array
// (values seen in the wild: "completion", "tools", "insert", "vision",
// "embedding"). Older daemons omit the field entirely, which this
// adapter treats as "the live probe could not determine anything" —
// see ProviderCapabilities below.
type ollamaShowResponse struct {
	Capabilities []string `json:"capabilities"`
}

// probeShow calls POST /api/show {"name": modelID} and reports whether
// the live daemon advertises tool-calling / vision support for model.
// ok is false when the daemon's response carries no "capabilities"
// field at all (pre-0.4 Ollama) — the caller must not treat that as
// "false", only as "unknown; do not overlay this field".
func (a *Adapter) probeShow(ctx context.Context, base, modelID string) (toolCalling, vision bool, err error) {
	reqBody, _ := json.Marshal(map[string]string{"name": modelID})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/show", bytes.NewReader(reqBody))
	if err != nil {
		return false, false, fmt.Errorf("ollama: build /api/show request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		return false, false, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return false, false, classifyStatus(resp.StatusCode, respBody)
	}
	var doc ollamaShowResponse
	if derr := json.NewDecoder(resp.Body).Decode(&doc); derr != nil {
		return false, false, fmt.Errorf("ollama: parse /api/show response: %w", derr)
	}
	if doc.Capabilities == nil {
		return false, false, errNoCapabilityField
	}
	for _, c := range doc.Capabilities {
		switch c {
		case "tools":
			toolCalling = true
		case "vision":
			vision = true
		}
	}
	return toolCalling, vision, nil
}

// errNoCapabilityField signals a successful /api/show call from a
// daemon too old to report the "capabilities" field. This is
// deliberately an error (not a false-everything result): a probe that
// cannot determine tool-calling support must not overlay
// CapVision/CapToolCalling=false onto a profile whose static baseline
// (ollama.yaml) already says true — that would be exactly the
// half-fix R-5 warns about, just triggered from the probe side instead
// of the attachment-gate side.
var errNoCapabilityField = errors.New("ollama: /api/show response has no capabilities field (daemon predates Ollama 0.4)")

// ProviderCapabilities implements llm.CapabilitiesProvider —
// the first live implementor of that interface anywhere in the tree
// (model-settings-reach-the-model-01PMZ101 WP14 / FR-017, spec §5.12
// "Change" item 1). It probes the adapter's process-wide default
// endpoint (a.probeAt) — used only by callers with no ProviderProfile
// in scope. Every production caller that HAS a profile
// (registry.Registry.refreshCapabilities) must go through
// ProviderCapabilitiesAt instead (review finding H4): a single
// *Adapter instance is shared by every "ollama"-kind profile
// (registry.go registers one adapter per Kind), so probing via
// a.probeAt regardless of which profile asked would cache a REMOTE
// profile's capabilities from whatever host happens to be the
// process-wide default, under that remote profile's ID.
func (a *Adapter) ProviderCapabilities(ctx context.Context, modelID string) (llm.ProviderCapabilities, error) {
	return a.ProviderCapabilitiesAt(ctx, a.probeAt, modelID)
}

// ProviderCapabilitiesAt implements llm.EndpointCapabilitiesProvider
// (H4 fix). It starts from the static catalog descriptor for (Kind,
// modelID) — DescribeRich already curates every field this probe
// doesn't check — and overlays only ToolCalling/Vision from a live
// /api/show call against endpoint (resolveBase applies the same
// "empty means DefaultBaseURL" fallback Stream uses via baseURL). Per
// A-5's merge order ("probe → cache → CapabilityHints reader ... the
// static baseline lands FIRST and stays"), a probe error returns
// before any override happens, so the caller (Registry, via
// Refresher.MaybeRefresh) never Puts a cache entry and the static
// baseline in ollama.yaml keeps deciding Gate.Check on its own — a
// probe failure degrades to "no live data yet", never to "everything
// unsupported".
func (a *Adapter) ProviderCapabilitiesAt(ctx context.Context, endpoint, modelID string) (llm.ProviderCapabilities, error) {
	if a.cat == nil {
		return llm.ProviderCapabilities{}, errors.New("ollama: no capability catalog loaded")
	}
	base := resolveBase(endpoint)
	toolCalling, vision, err := a.probeShow(ctx, base, modelID)
	if err != nil {
		return llm.ProviderCapabilities{}, fmt.Errorf("ollama: capability probe: %w", err)
	}
	caps := a.cat.DescribeRich(Kind, modelID)
	caps.ToolCalling = toolCalling
	caps.Vision = vision
	// M5 fix: name exactly the two fields this probe actually
	// determined. Without this, registry.mergeCapabilityHints (via
	// ProbedSupported) would have no way to distinguish "the probe
	// verified this" from "DescribeRich's static baseline happened to
	// carry this value" — and a stale cache hit would shadow all 12
	// Supported keys for up to cacheTTL, not just these two.
	caps.ProbedCapabilities = []llm.Capability{llm.CapToolCalling, llm.CapVision}
	return caps, nil
}

// Compile-time witness: *Adapter satisfies the H4-fix extension
// interface as well as plain llm.CapabilitiesProvider (already
// witnessed below).
var _ llm.EndpointCapabilitiesProvider = (*Adapter)(nil)

// classifyStatus maps HTTP error status codes to the connector
// taxonomy, mirroring core/llm/custom's classifyStatus (a private
// function in a package this adapter does not import from).
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
	// Ollama's error shape: {"error": "model 'x' not found"}. The
	// OpenAI-compat surface sometimes nests it: {"error":{"message":...}}.
	var flat struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &flat); err == nil && flat.Error != "" {
		return flat.Error
	}
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && nested.Error.Message != "" {
		return nested.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// chatStream is the SSE consumer for Ollama streaming, identical in
// shape to custom-openai's since both speak the same OpenAI-compatible
// delta format (core/llm/openaiwire is what both adapters build
// requests through).
type chatStream struct {
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
	s.cancelled = true
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
		s.cancel()
		s.mu.Lock()
		// B2 fix: flush any tool-call deltas that never saw a trailing
		// finish_reason=="tool_calls" (e.g. Ollama finishing with "stop"
		// after tool_calls deltas, or a mid-stream drop) BEFORE closing
		// s.events. flushPendingToolCallsLocked sends on s.events; doing
		// that after close(s.events) is a "send on closed channel" panic
		// that kills the whole desktop app (there is no recover on this
		// goroutine).
		s.flushPendingToolCallsLocked()
		close(s.events)
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
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
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
		case errors.Is(err, context.Canceled):
			s.finalErr = &llm.ErrCancelled{Reason: "context"}
		default:
			s.finalErr = &llm.ErrTransient{Message: "ollama: stream read: " + err.Error(), Cause: err}
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
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "ollama: malformed SSE frame: " + err.Error()}
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
		tool := llm.ToolUse{ID: state.id, Name: state.name, Input: json.RawMessage(args)}
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
var _ llm.CapabilitiesProvider = (*Adapter)(nil)
