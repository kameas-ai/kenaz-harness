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
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/httpx"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
	"github.com/kameas-ai/kenaz-harness/core/llm/structured"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Kind is the canonical provider kind ("openrouter").
const Kind = "openrouter"

const (
	defaultEndpoint    = "https://openrouter.ai/api/v1/chat/completions"
	defaultModelsURL   = "https://openrouter.ai/api/v1/models"
	defaultReferer     = "https://github.com/kameas-ai/kenaz-harness"
	defaultAppTitle    = "kenaz-harness"
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
// default the adapter advertises the kenaz-harness GitHub URL.
func WithReferer(s string) Option {
	return func(a *Adapter) {
		if s != "" {
			a.referer = s
		}
	}
}

// WithAppTitle overrides the X-Title ranking header value. By default
// the adapter advertises "kenaz-harness".
func WithAppTitle(s string) Option {
	return func(a *Adapter) {
		if s != "" {
			a.appTitle = s
		}
	}
}

// Adapter implements llm.ProviderAdapter and llm.ModelLister against
// the OpenRouter API.
// Adapter is the OpenRouter ProviderAdapter implementation.
//
// WP04: Adapter now embeds openaiwire.Base for shared body building (WP04).
// The kill-switch HARNESS_LLM_USE_OPENAIWIRE (default "true") selects
// between the openaiwire path and the legacy local builder.
type Adapter struct {
	httpc    *http.Client
	endpoint string
	referer  string
	appTitle string
	// base holds the openaiwire.Base for shared body building (WP04).
	base     openaiwire.Base

	// modelCache caches the live /api/v1/models response so ListProviders
	// (and any other caller that needs per-model context_window) can read
	// the dynamic OpenRouter values without re-issuing the HTTP request on
	// every ListProviders call. The OpenRouter /models endpoint is public
	// (no auth required), so the cache is shared across profiles. Map
	// values are llm.ModelInfo by model ID.
	modelCache       sync.Map      // map[string]llm.ModelInfo
	modelCacheTime   time.Time     // last successful fetch
	modelCacheMu     sync.Mutex    // guards modelCacheTime + in-flight refresh
	modelCacheTTL    time.Duration // entries older than this trigger background refresh
	refreshInFlight  bool          // true while a background refresh is running
	refreshFailedAt  time.Time     // when the last refresh failed (for backoff)
	refreshBackoff   time.Duration // how long to wait after a failure
}

// New constructs an Adapter with the OpenRouter defaults.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		httpc:          &http.Client{Transport: httpx.DefaultTransport()}, // no Timeout — context drives lifetime
		endpoint:       defaultEndpoint,
		referer:        defaultReferer,
		appTitle:       defaultAppTitle,
		base:           openaiwire.NewBase(defaultEndpoint),
		modelCacheTTL:  1 * time.Hour,
		refreshBackoff: 5 * time.Minute,
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

// Compile-time assertions: *Adapter satisfies llm.ProviderAdapter,
// llm.ModelLister, and llm.StructuredOutputAdapter.
var (
	_ llm.ProviderAdapter       = (*Adapter)(nil)
	_ llm.ModelLister           = (*Adapter)(nil)
	_ llm.StructuredOutputAdapter = (*Adapter)(nil)
)

// ApplyResponseFormat implements llm.StructuredOutputAdapter. OpenRouter
// speaks the OpenAI Chat Completions wire shape, so the same response_format
// encoding applies. Grammar mode is unsupported (cloud provider).
//
// (structured-output-and-grammar-01KX5R8A WP03c)
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
				return fmt.Errorf("openrouter: response_format schema parse: %w", err)
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
		// Unknown mode — treat as no-op.
	}
	return nil
}

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
		// Log the transport failure: this is the silent path that the
		// retry middleware previously surfaced only as "retry budget
		// exhausted". ctx_err disambiguates an upstream cancellation
		// (e.g. frontend disconnect on desktop focus loss) from a real
		// network fault.
		logging.L().Warn("openrouter.http.error",
			"model", prof.Model, "endpoint", endpoint,
			"err", err.Error(), "ctx_err", ctx.Err())
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}

	if resp.StatusCode/100 != 2 {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyByteLimit))
		_ = resp.Body.Close()
		cancel()
		// Log the non-2xx status + body snippet. Without this the actual
		// provider rejection (429 rate limit, 5xx, 400 invalid request)
		// was invisible — only the post-retry generic error reached the
		// log. classifyStatus decides retryability; we log it either way.
		classified := classifyStatus(resp.StatusCode, bodySnippet)
		logging.L().Warn("openrouter.http.error",
			"model", prof.Model, "status", resp.StatusCode,
			"body", string(bodySnippet),
			"retryable", llm.IsTransient(classified))
		return nil, classified
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
//
// OpenRouter's /api/v1/models endpoint is publicly accessible — auth is
// optional. When cred is empty we still issue the request (no
// Authorization header) so the AddProvider modal can populate the model
// picker before the user has saved a key.
//
// The result is cached on the adapter so ListProviders can read live
// context_length values via LookupModelInfo without re-fetching. See
// modelCache for cache semantics.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	url := modelsURL(a.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: build models request: %w", err)
	}
	if len(cred) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(cred))
	}
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
		info := llm.ModelInfo{
			ID:            m.ID,
			DisplayName:   display,
			Description:   m.Description,
			ContextWindow: m.ContextLength,
		}
		out = append(out, info)
		// Populate the per-adapter cache so LookupModelInfo can serve
		// ListProviders without re-fetching. context_length on the wire
		// is in tokens and matches the value we surface upstream verbatim.
		a.modelCache.Store(m.ID, info)
	}
	a.modelCacheMu.Lock()
	a.modelCacheTime = time.Now()
	a.refreshFailedAt = time.Time{} // success clears the backoff
	a.modelCacheMu.Unlock()
	logging.L().Info("openrouter.list_models.success",
		"models", len(out),
		"url", url)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	return out, nil
}

// TestKey implements llm.KeyTester. Verifies cred by calling ListModels with
// a 5-second deadline; returns nil iff the response contains ≥1 model.
//
// provider-keychain-rotation-01KQ8TD9 WP02.
func (a *Adapter) TestKey(ctx context.Context, cred []byte) error {
	if len(cred) == 0 {
		return &llm.ErrAuth{Status: 0, Message: "openrouter: empty credential"}
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	models, err := a.ListModels(tctx, cred)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return &llm.ErrInvalidRequest{Status: 200, Message: "openrouter: key accepted but returned empty model list"}
	}
	return nil
}

// Compile-time assertion: Adapter implements llm.KeyTester.
var _ llm.KeyTester = (*Adapter)(nil)

// LookupModelInfo returns the cached llm.ModelInfo for the given model
// ID and reports whether the entry was present. ListProviders consults
// this method via the ModelInfoLookup capability interface to surface
// the live context_window value without re-issuing the /models HTTP
// request on every ListProviders call.
//
// A miss (false) signals that either (a) ListModels has never run for
// this adapter, or (b) the OpenRouter catalog doesn't list the model
// id. The caller should kick a background refresh via RefreshModelsAsync
// and fall back to 0 (frontend will use MODEL_CONTEXT_FALLBACK) for the
// current call.
func (a *Adapter) LookupModelInfo(modelID string) (llm.ModelInfo, bool) {
	v, ok := a.modelCache.Load(modelID)
	if !ok {
		return llm.ModelInfo{}, false
	}
	info, ok := v.(llm.ModelInfo)
	if !ok {
		return llm.ModelInfo{}, false
	}
	return info, true
}

// CacheAge reports how long since the last successful ListModels fetch.
// Returns a very large duration when the cache has never been populated.
func (a *Adapter) CacheAge() time.Duration {
	a.modelCacheMu.Lock()
	defer a.modelCacheMu.Unlock()
	if a.modelCacheTime.IsZero() {
		return 365 * 24 * time.Hour
	}
	return time.Since(a.modelCacheTime)
}

// RefreshModelsAsync triggers a background ListModels fetch when the
// cache is stale (older than modelCacheTTL) or empty, and de-duplicates
// concurrent refresh attempts. It is a no-op when a refresh is already
// in flight or when the previous refresh failed within the backoff
// window. Safe to call from any goroutine.
//
// OpenRouter's /models endpoint is public, so the refresh succeeds
// without a credential. No credential is accepted here by design: raw
// bytes must not flow through the RPC interface boundary (see
// AdapterRefresher contract in core/rpc/views/llm).
func (a *Adapter) RefreshModelsAsync() {
	a.modelCacheMu.Lock()
	if a.refreshInFlight {
		a.modelCacheMu.Unlock()
		return
	}
	if !a.refreshFailedAt.IsZero() && time.Since(a.refreshFailedAt) < a.refreshBackoff {
		a.modelCacheMu.Unlock()
		logging.L().Debug("openrouter.refresh_models.skipped_backoff",
			"failed_age_ms", time.Since(a.refreshFailedAt).Milliseconds(),
			"backoff_ms", a.refreshBackoff.Milliseconds())
		return
	}
	a.refreshInFlight = true
	a.modelCacheMu.Unlock()

	go func() {
		defer func() {
			a.modelCacheMu.Lock()
			a.refreshInFlight = false
			a.modelCacheMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		logging.L().Info("openrouter.refresh_models.start")
		_, err := a.ListModels(ctx, nil)
		if err != nil {
			a.modelCacheMu.Lock()
			a.refreshFailedAt = time.Now()
			a.modelCacheMu.Unlock()
			logging.L().Warn("openrouter.refresh_models.failed",
				"err", err.Error())
			return
		}
	}()
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

// classifyStatus delegates to the canonical top-level
// llm.ClassifyStatus (WP01 of provider-implementation-uniformity-01KQ8V4F).
// Kept as a thin package-local alias; removed entirely in WP10.
func classifyStatus(status int, body []byte) error {
	return llm.ClassifyStatus(status, body)
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
// useOpenAIWire reports whether the openaiwire body builder is active.
// HARNESS_LLM_USE_OPENAIWIRE=false falls back to the pre-WP04 local
// builder for kill-switch testing. Default is true.
func useOpenAIWire() bool {
	v := os.Getenv("HARNESS_LLM_USE_OPENAIWIRE")
	return v == "" || v == "true" || v == "1"
}

// buildRequestBody constructs the JSON body for the OpenRouter Chat
// Completions API. When HARNESS_LLM_USE_OPENAIWIRE is true (default),
// it delegates to openaiwire.BuildRequestBody for standard fields and
// then applies OpenRouter-specific overrides (model ID, ranking headers,
// OpenRouter usage field). Falls back to the pre-WP04 legacy builder when
// the kill-switch is off.
func buildRequestBody(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
	if useOpenAIWire() {
		model := prof.Model
		if req.Model != "" {
			model = req.Model
		}
		body, err := openaiwire.BuildRequestBody(req, model, prof.Defaults)
		if err != nil {
			return nil, err
		}
		// OpenRouter uses "usage" instead of "stream_options.include_usage".
		delete(body, "stream_options")
		body["usage"] = map[string]any{"include": true}
		return json.Marshal(body)
	}
	return buildRequestBodyLegacy(req, prof)
}

// buildRequestBodyLegacy is the pre-WP04 local builder kept for kill-switch
// fallback (HARNESS_LLM_USE_OPENAIWIRE=false). Connector ContentBlocks are
// flattened into a single string per message (text concatenation). Non-text
// parts are dropped — OpenRouter's router accepts richer content arrays per
// upstream model, but the v1 adapter targets the universal text path.
func buildRequestBodyLegacy(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
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

		// Inspect content blocks for tool_use (assistant turns the model
		// emitted that we're echoing back) and tool_result (kernel's
		// tool-dispatch output we're feeding into the next turn).
		var toolUses []*llm.ToolUse
		var toolResultBlock *llm.ToolResult
		for i := range m.Content {
			blk := m.Content[i]
			if blk.Type == "tool_use" && blk.ToolUse != nil {
				toolUses = append(toolUses, blk.ToolUse)
			}
			if blk.Type == "tool_result" && blk.ToolResult != nil {
				toolResultBlock = blk.ToolResult
			}
		}

		// Tool-result message → OpenAI tool message shape:
		//   {role:"tool", tool_call_id:"...", content:"..."}
		// Without tool_call_id the upstream model can't pair the result
		// to the prior tool_use, and OpenAI-compat providers reject the
		// request (silent 5xx → retry budget exhausted).
		if role == "tool" && toolResultBlock != nil {
			content := string(toolResultBlock.Content)
			// ToolResult.Content is JSON-encoded — strip the surrounding
			// quotes for plain-string payloads so the model sees the raw
			// text rather than `"escaped\nstring"`.
			if len(content) >= 2 && content[0] == '"' && content[len(content)-1] == '"' {
				var unq string
				if err := json.Unmarshal(toolResultBlock.Content, &unq); err == nil {
					content = unq
				}
			}
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": toolResultBlock.ToolUseID,
				"content":      content,
			})
			continue
		}

		// Assistant turn that emitted tool_calls → OpenAI assistant
		// shape:
		//   {role:"assistant", content: text|null,
		//    tool_calls:[{id, type:"function",
		//                 function:{name, arguments}}]}
		// Without tool_calls the next-turn tool_call_id has no parent.
		if len(toolUses) > 0 {
			tcs := make([]map[string]any, 0, len(toolUses))
			for _, tu := range toolUses {
				args := string(tu.Input)
				if args == "" {
					args = "{}"
				}
				tcs = append(tcs, map[string]any{
					"id":   tu.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tu.Name,
						"arguments": args,
					},
				})
			}
			text := flattenContent(m.Content)
			entry := map[string]any{
				"role":       role,
				"tool_calls": tcs,
			}
			if text != "" {
				entry["content"] = text
			} else {
				entry["content"] = nil
			}
			msgs = append(msgs, entry)
			continue
		}

		// For non-tool messages, use the array-of-parts content shape when
		// the message contains image blocks (OpenRouter proxies image_url to
		// the underlying vision model). Document blocks are not serialized
		// (document_input: false in the default YAML; the gate rejects them
		// pre-flight for providers that don't support PDFs).
		msgs = append(msgs, map[string]any{
			"role":    role,
			"content": buildOpenRouterContent(m.Content),
		})
	}
	out["messages"] = msgs

	// Tool serialization — OpenRouter accepts OpenAI's Chat Completions
	// tools envelope: each spec wrapped in {type:"function", function:{
	// name, description, parameters}}. ToolSpec.InputSchema is already
	// JSON Schema. Without this block tools were discovered + threaded
	// through GenerationRequest.Tools but silently dropped at the wire
	// boundary, so the model saw an empty tool catalog and answered
	// "I'm a text-only assistant" on every turn.
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
					return nil, fmt.Errorf("openrouter: tool %q parameters: %w", t.Name, err)
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

	// Optional sampling knobs surfaced through Params / Defaults.
	for _, key := range []string{"temperature", "top_p", "max_tokens"} {
		if v, ok := req.Params[key]; ok {
			out[key] = v
		} else if v, ok := prof.Defaults[key]; ok {
			out[key] = v
		}
	}

	// Apply ResponseFormat if set (structured-output-and-grammar-01KX5R8A WP03c).
	if req.ResponseFormat != nil {
		a := &Adapter{}
		if err := a.ApplyResponseFormat(&req, out); err != nil {
			return nil, err
		}
	}

	// Apply JSONMode if set (multimodal-io-extended-01KQ8TD2 WP03).
	// OpenRouter passes response_format through to the underlying provider.
	if req.JSONMode != nil && req.JSONMode.Enabled && req.ResponseFormat == nil {
		if err := applyJSONModeOpenRouter(req.JSONMode, out); err != nil {
			return nil, err
		}
	}

	return json.Marshal(out)
}

// applyJSONModeOpenRouter translates JSONModeSpec for OpenRouter.
// OpenRouter passes response_format through to the downstream provider;
// Schema present → json_schema; absent → json_object.
// (multimodal-io-extended-01KQ8TD2 WP03)
func applyJSONModeOpenRouter(jm *llm.JSONModeSpec, wireBody map[string]any) error {
	if jm == nil || !jm.Enabled {
		return nil
	}
	if len(jm.Schema) == 0 {
		wireBody["response_format"] = map[string]any{"type": "json_object"}
		return nil
	}
	var schemaVal any
	if err := json.Unmarshal(jm.Schema, &schemaVal); err != nil {
		return fmt.Errorf("openrouter: json_mode schema parse: %w", err)
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

// buildOpenRouterContent serializes a slice of ContentBlocks into the
// OpenAI-compatible content shape that OpenRouter accepts.
//
//   - Pure-text messages return a single string (classic shape; lighter payload).
//   - Messages with one or more image blocks return []map[string]any with
//     {type:"text"} and {type:"image_url", image_url:{url:"data:..."}} parts
//     (multimodal-io-01KQ8TDF FR-013).
//   - Document blocks are silently dropped: the capability gate rejects PDF
//     attachments for OpenRouter models pre-flight (document_input: false in
//     the YAML), so this path is reached only for model rows that override to
//     document_input: true, in which case the underlying Claude adapter on the
//     OpenRouter side handles the PDF. For now drop to text-only for safety.
func buildOpenRouterContent(parts []llm.ContentBlock) any {
	hasImage := false
	for _, p := range parts {
		if p.Type == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return flattenContent(parts)
	}
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
				"image_url": map[string]any{"url": openRouterDataURL(p.Source)},
			})
		// document: intentionally dropped (gate-rejected pre-flight; see above).
		}
	}
	if len(out) == 0 {
		return ""
	}
	return out
}

// openRouterDataURL composes a data: URL from a base64 MediaSource for the
// OpenRouter image_url content part (mirrors openai.dataURL).
func openRouterDataURL(src *llm.MediaSource) string {
	if src.URI != "" && src.Kind == "uri" {
		return src.URI
	}
	return "data:" + src.MediaType + ";base64," + src.Data
}

// flattenContent concatenates the text fragments of a content slice
// into a single string. Tool / attachment fragments are dropped on the
// v1 adapter — tool calling round-trips through the upstream model's
// own contract, and OpenRouter accepts the OpenAI tool-call schema for
// callers that need it (out of scope for the streaming text MVP).
func flattenContent(parts []llm.ContentBlock) string {
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
	textBuf         strings.Builder
	usage           llm.Usage
	providerCostUSD *float64 // non-nil when OpenRouter reports usage.cost
	finishStop      string
	// respID is the generation id ("gen-…") the provider stamps on every SSE
	// frame. Used to synthesize a stable tool_call id for providers (e.g.
	// Moonshot/kimi) that stream tool calls without one — see Final().
	respID string
	// toolCalls is the index-keyed accumulator for streamed tool calls.
	// OpenAI's wire format sends `id` and `function.name` once on the
	// opening fragment of a given `index`, then fragments `arguments`
	// across many subsequent deltas. We splice them back into a single
	// `llm.ToolUse` per index when the stream closes.
	toolCalls map[int]*toolCallAccum
}

// toolCallAccum accumulates the streamed fragments of a single tool
// call. `index` is the OpenAI Chat Completions streaming index field.
type toolCallAccum struct {
	id      string
	name    string
	argsBuf strings.Builder
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
			// Order accumulated tool calls by index so callers see them
			// in the same order the model emitted them.
			var calls []llm.ToolUse
			if len(s.toolCalls) > 0 {
				idxs := make([]int, 0, len(s.toolCalls))
				for i := range s.toolCalls {
					idxs = append(idxs, i)
				}
				sort.Ints(idxs)
				calls = make([]llm.ToolUse, 0, len(idxs))
				for _, i := range idxs {
					a := s.toolCalls[i]
					id := a.id
					if id == "" {
						// Some providers (e.g. Moonshot/kimi via OpenRouter)
						// stream tool calls with no `id`. A tool result must
						// echo a non-empty tool_call_id or the provider rejects
						// the next turn ("tool_call_id  is not found"), so
						// synthesize a stable one from the generation id +
						// index. It is stored on the assistant message and
						// reused verbatim for the tool result, keeping the pair
						// matched.
						if s.respID != "" {
							id = fmt.Sprintf("%s-%d", s.respID, i)
						} else {
							id = fmt.Sprintf("call_%d", i)
						}
					}
					calls = append(calls, llm.ToolUse{
						ID:    id,
						Name:  a.name,
						Input: json.RawMessage(a.argsBuf.String()),
					})
				}
			}
			var cost llm.Cost
			if s.providerCostUSD != nil {
				cost = llm.Cost{
					Currency: "USD",
					Total:    *s.providerCostUSD,
					Source:   "provider",
				}
			}
			s.final = llm.Response{
				Content:      []llm.ContentBlock{{Type: "text", Text: s.textBuf.String()}},
				ToolCalls:    calls,
				FinishReason: s.finishStop,
				Usage:        s.usage,
				Cost:         cost,
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

// truncForLog returns up to n bytes from b suitable for an slog field.
// Multi-byte UTF-8 sequences split across the truncation boundary are
// trimmed back so the log line stays valid UTF-8.
func truncForLog(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	cut := n
	for cut > 0 && (b[cut]&0xC0) == 0x80 {
		cut--
	}
	return string(b[:cut]) + "…"
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
		logging.L().Info("openrouter.sse.done", "bytes", len(raw))
		return
	}
	// One log entry per SSE record so corruption can be reproduced
	// from the on-disk transcript at ~/.kenaz/harness.log. The full
	// frame goes in `raw` (capped to the first 512 bytes to keep the
	// log compact); rare full-frame inspection lives at LevelDebug.
	logging.L().Info("openrouter.sse.frame",
		"bytes", len(trimmed),
		"head", truncForLog(trimmed, 512),
	)
	var env struct {
		ID      string `json:"id"`
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				Role      string `json:"role"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id,omitempty"`
					Type     string `json:"type,omitempty"`
					Function struct {
						Name      string `json:"name,omitempty"`
						Arguments string `json:"arguments,omitempty"`
					} `json:"function"`
				} `json:"tool_calls,omitempty"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int      `json:"prompt_tokens"`
			CompletionTokens int      `json:"completion_tokens"`
			TotalTokens      int      `json:"total_tokens"`
			Cost             *float64 `json:"cost,omitempty"`
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

	// Remember the generation id from the first frame that carries one, so
	// Final() can synthesize tool_call ids for providers that omit them.
	if env.ID != "" {
		s.mu.Lock()
		if s.respID == "" {
			s.respID = env.ID
		}
		s.mu.Unlock()
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

	// Text deltas, tool-call deltas, and finish reasons travel through choices[].
	for _, c := range env.Choices {
		// Tool-call deltas: the OpenAI-compat wire format streams `id`
		// and `function.name` once on the opening fragment of a given
		// `index`, then fragments `function.arguments` as JSON-string
		// pieces over many subsequent deltas. Splice them into the
		// per-index accumulator. Without this branch the model's
		// tool_calls stream is silently discarded and Final.ToolCalls
		// stays empty — the kernel's tool_dispatch then sees no calls
		// and the agent loop exits after one iteration.
		if len(c.Delta.ToolCalls) > 0 {
			s.mu.Lock()
			if s.toolCalls == nil {
				s.toolCalls = make(map[int]*toolCallAccum)
			}
			for _, tc := range c.Delta.ToolCalls {
				acc, ok := s.toolCalls[tc.Index]
				if !ok {
					acc = &toolCallAccum{}
					s.toolCalls[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.argsBuf.WriteString(tc.Function.Arguments)
				}
			}
			s.mu.Unlock()
		}
		if c.Delta.Content != "" {
			s.mu.Lock()
			s.textBuf.WriteString(c.Delta.Content)
			cumulative := s.textBuf.Len()
			s.mu.Unlock()
			// Per-delta INFO log so the on-disk transcript at
			// ~/.kenaz/harness.log carries every chunk. `delta` is
			// truncated to keep the log line compact; the full delta
			// is preserved in StreamEvent.Raw for the chat surface.
			logging.L().Info("openrouter.delta",
				"len", len(c.Delta.Content),
				"cumulative", cumulative,
				"delta", truncForLog([]byte(c.Delta.Content), 256),
			)
			s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: c.Delta.Content, Raw: trimmed}
		}
		if c.FinishReason != nil && *c.FinishReason != "" {
			s.mu.Lock()
			s.finishStop = *c.FinishReason
			finish := s.finishStop
			total := s.textBuf.Len()
			s.mu.Unlock()
			logging.L().Info("openrouter.finish", "reason", finish, "total_bytes", total)
			s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: finish}
		}
	}

	if env.Usage != nil {
		s.mu.Lock()
		s.usage.InputTokens = env.Usage.PromptTokens
		s.usage.OutputTokens = env.Usage.CompletionTokens
		if env.Usage.Cost != nil && *env.Usage.Cost > 0 {
			v := *env.Usage.Cost
			s.providerCostUSD = &v
		}
		usage := s.usage
		s.mu.Unlock()
		s.events <- llm.StreamEvent{Kind: llm.StreamUsage, Usage: &usage}
	}
}
