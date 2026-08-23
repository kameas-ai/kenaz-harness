package azure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
)

// Kind is the canonical provider kind for the Azure OpenAI adapter.
// It must match the ProviderProfile.Kind value in personal profiles.
const Kind = "azure-openai"

// defaultAPIVersion is the stable GA api-version used for TestKey and as
// a fallback when no deployment entry specifies one.
const defaultAPIVersion = "2024-10-21"

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithHTTPClient overrides the HTTP client. The client should have NO
// request-level timeout — context cancellation drives stream lifetime.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// WithDeploymentRegistry overrides the deployment registry. Used by tests.
func WithDeploymentRegistry(r *DeploymentRegistry) Option {
	return func(a *Adapter) {
		if r != nil {
			a.deployments = r
		}
	}
}

// WithCatalog overrides the capability catalog. Tests inject fixture catalogs here.
func WithCatalog(cat *capabilities.Catalog) Option {
	return func(a *Adapter) {
		if cat != nil {
			a.cat = cat
		}
	}
}

// AzureOpenAIEnabled reports whether the Azure OpenAI adapter is enabled.
// Default on — set HARNESS_AZURE_OPENAI=0 to disable.
func AzureOpenAIEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("HARNESS_AZURE_OPENAI")))
	return v != "0" && v != "false" && v != "off"
}

// Adapter is the Azure OpenAI ProviderAdapter implementation.
// It satisfies llm.ProviderAdapter and llm.ModelLister.
type Adapter struct {
	httpc       *http.Client
	deployments *DeploymentRegistry
	cat         *capabilities.Catalog
}

// Compile-time assertions.
var _ llm.ProviderAdapter = (*Adapter)(nil)
var _ llm.ModelLister = (*Adapter)(nil)

// New constructs an Adapter.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		httpc: &http.Client{Transport: httpx.DefaultTransport()},
	}
	// Load default (empty) deployment registry — operators supply a real
	// one via HARNESS_AZURE_DEPLOYMENTS_PATH or by calling WithDeploymentRegistry.
	if reg, err := LoadDefaultDeployments(); err == nil {
		a.deployments = reg
	} else {
		a.deployments = &DeploymentRegistry{index: map[deploymentKey]DeploymentEntry{}}
	}
	if envPath := strings.TrimSpace(os.Getenv("HARNESS_AZURE_DEPLOYMENTS_PATH")); envPath != "" {
		if reg, err := LoadDeployments(envPath); err == nil {
			a.deployments = reg
		}
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind string.
func (a *Adapter) Kind() string { return Kind }

// Capabilities reports the (provider, model) descriptor. Azure OpenAI
// uses the OpenAI capability catalog — the wire shapes are identical.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	if a.cat != nil {
		// Query with provider="openai" — Azure mirrors OpenAI's capability set.
		desc := a.cat.Describe("openai", model)
		// Rewrite provider field so callers see "azure-openai" in audit trails.
		desc.Provider = Kind
		return desc
	}
	// Fallback inline descriptor (catalog unavailable).
	supported := map[llm.Capability]bool{
		llm.CapStreaming:      true,
		llm.CapToolCalling:    true,
		llm.CapUsageReporting: true,
	}
	return llm.CapabilityDescriptor{
		Provider:  Kind,
		Model:     model,
		Supported: supported,
		Notes:     map[llm.Capability]string{llm.CapStreaming: "catalog_unavailable"},
	}
}

// Stream opens an SSE connection to the Azure OpenAI Chat Completions
// endpoint and returns a llm.Stream.
//
// The ProviderProfile.Endpoint field, when non-empty, is used as the
// verbatim URL (for tests using httptest). When Endpoint is empty the
// adapter builds the URL from Profile.Region (used as the resource host)
// and the deployment registry.
func (a *Adapter) Stream(
	ctx context.Context,
	req llm.GenerationRequest,
	prof llm.ProviderProfile,
	cred []byte,
) (llm.Stream, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Status: 0, Message: "azure-openai: empty credential"}
	}

	var endpoint string
	if prof.Endpoint != "" {
		// Test / override path — use the given URL verbatim.
		endpoint = prof.Endpoint
	} else {
		// Production path: build from host (profile.Region) + deployment registry.
		host := prof.Region
		if host == "" {
			return nil, &llm.ErrInvalidRequest{Status: 0, Message: "azure-openai: resource host not configured (set profile.Region to your Azure resource hostname)"}
		}
		modelID := prof.Model
		if req.Model != "" {
			modelID = req.Model
		}
		entry, err := a.deployments.Resolve(host, modelID)
		if err != nil {
			return nil, err
		}
		u, err := buildChatURL(host, entry.Deployment, entry.APIVersion)
		if err != nil {
			return nil, &llm.ErrInvalidRequest{Status: 0, Message: "azure-openai: " + err.Error()}
		}
		endpoint = u
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
	// Azure OpenAI authenticates with "api-key" header (not Bearer token).
	setAPIKeyHeader(httpReq, cred)

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		cancel()
		return nil, &llm.ErrTransient{Status: 0, Message: "azure-openai: " + err.Error(), Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, classifyStatus(resp.StatusCode, bodySnippet)
	}

	s := &azureChatStream{
		resp:   resp,
		cancel: cancel,
		events: make(chan llm.StreamEvent, 16),
		done:   make(chan struct{}),
	}
	go s.pump()
	return s, nil
}

// ListModels enumerates the deployments known to the registry as a list
// of ModelInfo values. Azure OpenAI does not expose a /models endpoint
// that returns available model IDs the way OpenAI does — the deployment
// list is operator-configured. We surface it so the model picker can
// show something useful.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Status: 0, Message: "azure-openai: empty credential"}
	}
	entries := a.deployments.Entries()
	out := make([]llm.ModelInfo, 0, len(entries))
	seen := map[string]struct{}{}
	for _, e := range entries {
		if _, ok := seen[e.ModelID]; ok {
			continue
		}
		seen[e.ModelID] = struct{}{}
		out = append(out, llm.ModelInfo{
			ID:          e.ModelID,
			DisplayName: e.ModelID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// TestKey validates that cred is accepted by the Azure OpenAI resource.
// It calls the /openai/models endpoint on the resource host and returns
// the structured TestKeyResult. Implements a 5-second deadline internally.
//
// host is the Azure resource hostname (e.g. "myresource.openai.azure.com").
// apiVersion defaults to defaultAPIVersion when empty.
func (a *Adapter) TestKey(ctx context.Context, host string, cred []byte) (TestKeyResult, error) {
	if len(cred) == 0 {
		return TestKeyResult{}, &llm.ErrAuth{Status: 0, Message: "azure-openai: empty credential"}
	}
	apiVer := defaultAPIVersion
	// Pick the api-version from the first matching deployment entry if available.
	entries := a.deployments.Entries()
	for _, e := range entries {
		if e.Host == host && e.APIVersion != "" {
			apiVer = e.APIVersion
			break
		}
	}

	modURL, err := buildModelsURL(host, apiVer)
	if err != nil {
		return TestKeyResult{}, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}

	tCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(tCtx, http.MethodGet, modURL, nil)
	if err != nil {
		return TestKeyResult{}, &llm.ErrInvalidRequest{Status: 0, Message: "azure-openai: build models request: " + err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	setAPIKeyHeader(req, cred)

	resp, err := a.httpc.Do(req)
	if err != nil {
		return TestKeyResult{}, &llm.ErrTransient{Status: 0, Message: "azure-openai: " + err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	deprecation := extractDeprecationWarning(resp)

	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		modelCount := countModelsInResponse(body)
		return TestKeyResult{OK: true, ModelCount: modelCount, DeprecationWarning: deprecation}, nil
	}

	bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	err = classifyStatus(resp.StatusCode, bodySnippet)
	return TestKeyResult{OK: false, DeprecationWarning: deprecation}, err
}

// countModelsInResponse best-effort counts the models in a /openai/models
// list response. Returns 0 on parse failure (treated as unknown count).
func countModelsInResponse(body []byte) int {
	var env struct {
		Value []json.RawMessage `json:"value"`
		// Some endpoints use "data" instead of "value".
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return 0
	}
	if len(env.Value) > 0 {
		return len(env.Value)
	}
	return len(env.Data)
}

// classifyStatus maps an HTTP error response to the connector taxonomy.
// Identical classification logic as the openai adapter.
func classifyStatus(status int, body []byte) error {
	msg := extractErrorMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &llm.ErrAuth{Status: status, Message: "azure-openai: " + msg}
	case status == 429:
		return &llm.ErrTransient{Status: status, Message: "azure-openai: " + msg}
	case status == 404:
		// 404 on Azure may mean wrong deployment name — surface as invalid request.
		return &llm.ErrInvalidRequest{Status: status, Message: "azure-openai: " + msg}
	case status >= 500 && status < 600:
		return &llm.ErrTransient{Status: status, Message: "azure-openai: " + msg}
	case status == 408 || status == 425:
		return &llm.ErrTransient{Status: status, Message: "azure-openai: " + msg}
	default:
		return &llm.ErrInvalidRequest{Status: status, Message: "azure-openai: " + msg}
	}
}

// extractErrorMessage best-effort parses an Azure OpenAI error envelope.
// Azure returns errors in the same shape as OpenAI.
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return env.Error.Code + ": " + env.Error.Message
		}
		return env.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// buildRequestBody constructs the JSON body for the Azure Chat Completions
// API. The wire shape is identical to OpenAI's Chat Completions body, so
// the body itself is built by the shared openaiwire encoder
// (model-settings-reach-the-model-01PMZ101 WP03) — azure keeps only its
// own URL / deployment / api-version / auth layer (buildChatURL,
// DeploymentRegistry, setAPIKeyHeader). This is also what makes the
// tool_use/tool_result round trip work: the private encoder this used to
// call (buildContent) erased every tool block, dropping tool_calls and
// tool_call_id entirely (spec FR-002). reasoning_effort for o1/o3 models
// (previously mapped here by mapReasoningEffort) is now applied inside
// openaiwire.BuildRequestBody itself (WP08, spec D-14) — moved, not
// duplicated, so this adapter no longer needs its own copy.
func buildRequestBody(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error) {
	model := prof.Model
	if req.Model != "" {
		model = req.Model
	}
	body, err := openaiwire.BuildRequestBody(req, model, prof.Defaults)
	if err != nil {
		return nil, err
	}
	return json.Marshal(body)
}

// ─────────────────── azureChatStream ────────────────────────────────────────
// azureChatStream is the SSE consumer for Azure OpenAI. The wire shape is
// identical to OpenAI so the pump logic is a direct copy. The only
// difference is in how errors are classified.

type azureChatStream struct {
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

func (s *azureChatStream) Events() <-chan llm.StreamEvent { return s.events }

func (s *azureChatStream) Cancel() error {
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

func (s *azureChatStream) Final() (llm.Response, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled && s.finalErr == nil {
		return llm.Response{}, &llm.ErrCancelled{Reason: "stop"}
	}
	return s.final, s.finalErr
}

func (s *azureChatStream) pump() {
	defer func() {
		_ = s.resp.Body.Close()
		s.cancel()
		s.mu.Lock()
		// B2 fix: flush pending tool-call deltas BEFORE closing s.events.
		// flushPendingToolCallsLocked sends on s.events; a tool-call delta
		// not followed by finish_reason=="tool_calls" (finishing "stop",
		// or a mid-stream drop) previously reached this defer with
		// s.events already closed, panicking the whole process.
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
			// no-op
		case errors.Is(err, context.Canceled):
			s.finalErr = &llm.ErrCancelled{Reason: "context"}
		default:
			s.finalErr = &llm.ErrTransient{Message: "azure-openai: stream read: " + err.Error(), Cause: err}
		}
		s.mu.Unlock()
		select {
		case s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}:
		default:
		}
	}
}

func (s *azureChatStream) handleFrame(raw []byte) {
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
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: "azure-openai: malformed SSE frame: " + err.Error()}
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

func (s *azureChatStream) flushPendingToolCallsLocked() {
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
