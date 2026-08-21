package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/workflows/web"
)

// TODO FR-004: surface tool invocations in the Runs-detail UI. This
// requires threading ToolUseCall records into StepResult so the
// frontend's Run detail component (WorkflowsView) can render each
// tool call and its result inline. Deferred to mission
// 'runs-tab-history' which owns that UI surface.

// runnerRegistry is mutated only by RegisterStepRunner at init time.
// web_fetch and web_scrape are intentionally absent: they require a
// per-engine Fetcher (so the robots.txt cache is scoped to the engine
// lifetime and doesn't bleed between test runs). DefaultRunners()
// creates fresh Fetcher instances each call.
var runnerRegistry = map[StepKind]StepRunner{
	StepKindModelTurn:     modelTurnRunner{},
	StepKindToolCall:      toolCallRunner{},
	StepKindHTTPRequest:   httpRequestRunner{},
	StepKindMCPCall:       mcpCallRunner{},
	StepKindShell:         shellRunner{},
	StepKindReadArtifact:  readArtifactRunner{},
	StepKindWriteArtifact: writeArtifactRunner{},
	StepKindTransform:     transformRunner{},
	StepKindConditional:   conditionalRunner{},
	// WP06 control-flow kinds (no-dep runners).
	StepKindWaitUntil: waitUntilRunner{},
	StepKindAggregate: aggregateRunner{},
}

// DefaultRunners returns the package-default StepRunner registry.
// Callers (notably the rpc impl) inject this into Engine.Runners.
//
// The returned runners have nil deps — they error at Run time when
// an external dependency (LLM, Tools, MCP, Artifacts) is required
// but unavailable. Callers wiring real deps should use
// DefaultRunnersWithDeps instead.
//
// A fresh web.Fetcher is created per call so the robots.txt 24h cache
// is scoped to the engine lifetime and cannot bleed between engines.
// notify runner ships with nil Notifier/Audit (error at run time for
// "os" surface; MCP surfaces degrade to unconfigured).
func DefaultRunners() map[StepKind]StepRunner {
	out := make(map[StepKind]StepRunner, len(runnerRegistry)+3)
	for k, v := range runnerRegistry {
		out[k] = v
	}
	fetcher := web.NewFetcher()
	out[StepKindWebFetch] = webFetchRunner{fetcher: fetcher}
	out[StepKindWebScrape] = webScrapeRunner{fetcher: fetcher}
	out[StepKindNotify] = notifyRunner{} // nil notifier → unconfigured for "os"
	return out
}

// DefaultRunnersWithDeps returns the package-default registry with
// each dep-needing runner pre-bound to the supplied Deps. Runners
// for kinds whose dep is nil retain the no-dep behavior (error at Run
// time); the rest dispatch through the supplied dependency.
func DefaultRunnersWithDeps(deps Deps) map[StepKind]StepRunner {
	out := DefaultRunners()
	if deps.LLM != nil || deps.DefaultLLMProfile != "" || deps.DefaultProfileFunc != nil {
		out[StepKindModelTurn] = modelTurnRunner{
			llm:                deps.LLM,
			defaultProfile:     deps.DefaultLLMProfile,
			defaultProfileFunc: deps.DefaultProfileFunc,
			toolDiscoverer:     deps.ToolDiscoverer,
			toolDispatcher:     deps.ToolDispatcher,
		}
	}
	if deps.Tools != nil {
		out[StepKindToolCall] = toolCallRunner{tools: deps.Tools}
	}
	if deps.MCP != nil {
		out[StepKindMCPCall] = mcpCallRunner{mcp: deps.MCP}
	}
	if deps.Artifacts != nil {
		out[StepKindReadArtifact] = readArtifactRunner{art: deps.Artifacts}
		out[StepKindWriteArtifact] = writeArtifactRunner{art: deps.Artifacts, sessionID: deps.SessionID}
	}
	// web_fetch and web_scrape always get a fresh shared Fetcher; the
	// NetAuthz gate and the network-fetch audit emitter are threaded in
	// from Deps.
	fetcher := web.NewFetcher()
	out[StepKindWebFetch] = webFetchRunner{fetcher: fetcher, authz: deps.NetAuthz, audit: deps.NetworkAudit}
	out[StepKindWebScrape] = webScrapeRunner{
		fetcher:            fetcher,
		llm:                deps.LLM,
		profile:            deps.DefaultLLMProfile,
		audit:              deps.NetworkAudit,
		defaultProfileFunc: deps.DefaultProfileFunc, // FR-006: wire DefaultProfileFunc so llm mode resolves default profile
		authz:              deps.NetAuthz,
	}
	// WP06: notify is always wired with provided Notifier/Audit (may be nil).
	out[StepKindNotify] = notifyRunner{notifier: deps.Notifier, mcp: deps.MCP, audit: deps.Audit}
	return out
}

// RegisterStepRunner installs runner for kind at process start.
func RegisterStepRunner(kind StepKind, runner StepRunner) {
	runnerRegistry[kind] = runner
}

// errDepUnavailable is the sentinel runners return when their
// external dependency was not wired into the Engine.
var errDepUnavailable = errors.New("workflows: runner dependency unavailable")

// ===========================================================================
// model_turn (WP03) — dispatches a streaming completion via LLMStreamer and
// accumulates text into the step output. Falls back to a stub-echo when no
// LLM is wired so the chassis can still boot end-to-end without an LLM
// registry.
//
// When Step.Tools is non-empty and Deps.ToolDiscoverer + ToolDispatcher are
// wired, the runner executes a bounded model→tool→model loop (FR-003).
// Steps without tools: behave byte-identically to the previous behaviour
// (FR-005).
// ===========================================================================

type modelTurnRunner struct {
	llm                LLMStreamer
	defaultProfile     string
	defaultProfileFunc func() string
	toolDiscoverer     ToolDiscoverer
	toolDispatcher     ToolDispatcher
}

func (modelTurnRunner) Validate(st Step) error {
	if st.UserPrompt == "" {
		return fmt.Errorf("model_turn step %q: user_prompt required", st.Name)
	}
	return nil
}

func (r modelTurnRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	if r.llm == nil {
		// Beta fallback: keep the chassis bootable when no LLM
		// registry is wired (e.g. unit tests of the rpc layer).
		return TypedValue{Type: ValueTypeText, Text: "[model_turn stub] " + st.UserPrompt}, nil
	}
	profile := st.Profile
	if profile == "" {
		profile = r.defaultProfile
	}
	if profile == "" && r.defaultProfileFunc != nil {
		profile = r.defaultProfileFunc()
	}
	if profile == "" {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("model_turn step %q: no profile — configure a provider in Settings → Providers, or set step.profile / Deps.DefaultLLMProfile", st.Name)
	}

	// FR-005: when no tools are requested, run a plain single-turn completion —
	// byte-identical to the previous behaviour.
	if st.Tools.IsEmpty() || r.toolDiscoverer == nil || r.toolDispatcher == nil {
		return r.runPlain(ctx, st, profile)
	}

	// FR-001/FR-003: discover tools and run the bounded loop.
	tools, err := r.discoverTools(ctx, st)
	if err != nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("model_turn step %q: discover tools: %w", st.Name, err)
	}
	if len(tools) == 0 {
		// No tools available — degrade gracefully to plain completion.
		return r.runPlain(ctx, st, profile)
	}
	return r.runToolLoop(ctx, st, profile, tools)
}

// runPlain executes a single-turn completion without any tool
// advertisement. This is the regression-pinned path (FR-005).
func (r modelTurnRunner) runPlain(ctx context.Context, st Step, profile string) (TypedValue, error) {
	stream, err := r.llm.Stream(ctx, LLMRequest{
		ProfileID: profile,
		Model:     st.Model,
		Prompt:    st.UserPrompt,
	})
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("model_turn step %q: %w", st.Name, err)
	}
	return r.drainStream(st, stream)
}

// drainStream reads Events until the channel closes then calls Final.
func (r modelTurnRunner) drainStream(st Step, stream LLMStream) (TypedValue, error) {
	var buf strings.Builder
	for ev := range stream.Events() {
		if ev.Err != "" {
			return TypedValue{Type: ValueTypeError, Text: buf.String()},
				fmt.Errorf("model_turn step %q: stream error: %s", st.Name, ev.Err)
		}
		buf.WriteString(ev.Text)
	}
	final, err := stream.Final()
	if err != nil {
		return TypedValue{Type: ValueTypeError, Text: buf.String()},
			fmt.Errorf("model_turn step %q: final: %w", st.Name, err)
	}
	out := buf.String()
	if out == "" {
		out = final
	}
	return TypedValue{Type: ValueTypeText, Text: out}, nil
}

// discoverTools returns the filtered tool list for this step per Step.Tools.
func (r modelTurnRunner) discoverTools(ctx context.Context, st Step) ([]ToolSpec, error) {
	all, err := r.toolDiscoverer.Discover(ctx)
	if err != nil {
		return nil, err
	}
	if st.Tools.All {
		return all, nil
	}
	// Filter to the named subset.
	allowed := make(map[string]bool, len(st.Tools.Names))
	for _, n := range st.Tools.Names {
		allowed[n] = true
	}
	out := make([]ToolSpec, 0, len(st.Tools.Names))
	for _, t := range all {
		if allowed[t.Name] {
			out = append(out, t)
		}
	}
	return out, nil
}

// runToolLoop runs the bounded model→tool→model loop (FR-003).
// It mirrors the chat/agent-graph iteration counting: each model turn
// that emits at least one tool call counts as one iteration; the loop
// exits when the model stops emitting tool calls or the iteration cap
// is reached.
func (r modelTurnRunner) runToolLoop(
	ctx context.Context, st Step, profile string, tools []ToolSpec,
) (TypedValue, error) {
	maxIter := st.MaxToolIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxToolIterations
	}

	// history accumulates the multi-turn conversation.
	var history []HistoryMessage

	// Seed with the user prompt.
	req := LLMRequest{
		ProfileID: profile,
		Model:     st.Model,
		Prompt:    st.UserPrompt,
		Tools:     tools,
	}

	var lastText string
	for iter := 0; iter < maxIter; iter++ {
		req.History = history

		stream, err := r.llm.Stream(ctx, req)
		if err != nil {
			return TypedValue{Type: ValueTypeError},
				fmt.Errorf("model_turn step %q (iter %d): %w", st.Name, iter, err)
		}

		// Drain text events.
		var textBuf strings.Builder
		for ev := range stream.Events() {
			if ev.Err != "" {
				return TypedValue{Type: ValueTypeError, Text: textBuf.String()},
					fmt.Errorf("model_turn step %q (iter %d): stream error: %s", st.Name, iter, ev.Err)
			}
			textBuf.WriteString(ev.Text)
		}
		if _, ferr := stream.Final(); ferr != nil {
			return TypedValue{Type: ValueTypeError, Text: textBuf.String()},
				fmt.Errorf("model_turn step %q (iter %d): final: %w", st.Name, iter, ferr)
		}
		turnText := textBuf.String()
		if turnText != "" {
			lastText = turnText
		}

		// Check for tool calls via the optional ToolCallStream interface.
		tcs, ok := stream.(ToolCallStream)
		if !ok {
			// Adapter does not implement ToolCallStream — treat as plain
			// completion (no tool calls). Return accumulated text.
			break
		}
		calls := tcs.ToolCalls()
		if len(calls) == 0 {
			// Model is done — no more tool calls.
			break
		}

		// Record this assistant turn in history.
		assistantTurn := HistoryMessage{
			Role:     "assistant",
			Text:     turnText,
			ToolUses: calls,
		}

		// Dispatch each tool call and collect results.
		results := make([]ToolCallResult, 0, len(calls))
		for _, call := range calls {
			content, isError, dispErr := r.toolDispatcher.Dispatch(ctx, call.Name, call.Input)
			if dispErr != nil {
				// Tool dispatch failure: surface as an error result rather
				// than aborting the loop, matching the chat/agent-graph
				// convention (the model sees the error and can recover).
				content = fmt.Sprintf("tool dispatch error: %s", dispErr.Error())
				isError = true
			}
			results = append(results, ToolCallResult{
				ToolUseID: call.ID,
				Content:   content,
				IsError:   isError,
			})
		}

		// Append assistant turn + tool-result turn to history.
		history = append(history, assistantTurn)
		history = append(history, HistoryMessage{
			Role:        "tool",
			ToolResults: results,
		})

		// On the next iteration req.Prompt is empty — history carries context.
		req.Prompt = ""
	}

	return TypedValue{Type: ValueTypeText, Text: lastText}, nil
}

// ===========================================================================
// tool_call (WP03) — dispatches the named tool via ToolCaller.
// ===========================================================================

type toolCallRunner struct {
	tools ToolCaller
}

func (toolCallRunner) Validate(st Step) error {
	if st.ToolName == "" {
		return fmt.Errorf("tool_call step %q: tool_name required", st.Name)
	}
	return nil
}

func (r toolCallRunner) Run(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	if r.tools == nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("tool_call step %q: %w (no ToolCaller wired)", st.Name, errDepUnavailable)
	}
	args, err := expandArgs(st.ToolArgs, rc)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("tool_call step %q: expand args: %w", st.Name, err)
	}
	res, err := r.tools.Call(ctx, st.ToolName, args)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("tool_call step %q: %w", st.Name, err)
	}
	if res.IsError {
		return TypedValue{Type: ValueTypeError, Text: res.Content},
			fmt.Errorf("tool_call step %q: tool returned error: %s", st.Name, res.Content)
	}
	return TypedValue{Type: ValueTypeText, Text: res.Content}, nil
}

// ===========================================================================
// http_request (WP04) — stdlib net/http with default 30s timeout and 1MB
// response cap. Output is JSON: {status, headers, body}.
// ===========================================================================

type httpRequestRunner struct{}

const httpResponseCap = 1 << 20 // 1 MiB

func (httpRequestRunner) Validate(st Step) error {
	if st.URL == "" {
		return fmt.Errorf("http_request step %q: url required", st.Name)
	}
	if st.Method == "" {
		return fmt.Errorf("http_request step %q: method required", st.Name)
	}
	return nil
}

func (httpRequestRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	timeout := time.Duration(st.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if st.Body != "" {
		body = strings.NewReader(st.Body)
	}
	req, err := http.NewRequestWithContext(cctx, strings.ToUpper(st.Method), st.URL, body)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("http_request step %q: build request: %w", st.Name, err)
	}
	for k, v := range st.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("http_request step %q: %w", st.Name, err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, httpResponseCap+1)
	respBytes, err := io.ReadAll(limited)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("http_request step %q: read body: %w", st.Name, err)
	}
	if int64(len(respBytes)) > httpResponseCap {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("http_request step %q: response exceeds 1MB cap", st.Name)
	}
	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	payload := map[string]any{
		"status":  resp.StatusCode,
		"headers": headers,
		"body":    string(respBytes),
	}
	encoded, _ := json.Marshal(payload)
	return TypedValue{Type: ValueTypeJSON, JSON: payload, Text: string(encoded)}, nil
}

// ===========================================================================
// mcp_call (WP04) — dispatches via MCPCaller (typically core/mcp/transport
// stdio Pool).
// ===========================================================================

type mcpCallRunner struct {
	mcp MCPCaller
}

func (mcpCallRunner) Validate(st Step) error {
	if st.Server == "" {
		return fmt.Errorf("mcp_call step %q: server required", st.Name)
	}
	if st.ToolName == "" {
		return fmt.Errorf("mcp_call step %q: tool_name required", st.Name)
	}
	return nil
}

func (r mcpCallRunner) Run(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	if r.mcp == nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("mcp_call step %q: %w (no MCPCaller wired)", st.Name, errDepUnavailable)
	}
	args, err := expandArgs(st.ToolArgs, rc)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("mcp_call step %q: expand args: %w", st.Name, err)
	}
	out, err := r.mcp.Call(ctx, st.Server, st.ToolName, args)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("mcp_call step %q: %w", st.Name, err)
	}
	return TypedValue{Type: ValueTypeText, Text: out}, nil
}

// ===========================================================================
// shell (WP04 — pre-existing) — runs Cmd + Args via os/exec.
// ===========================================================================

type shellRunner struct{}

func (shellRunner) Validate(st Step) error {
	if st.Cmd == "" {
		return fmt.Errorf("shell step %q: cmd required", st.Name)
	}
	return nil
}

func (shellRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	timeout := time.Duration(st.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, st.Cmd, st.Args...)
	if st.Cwd != "" {
		cmd.Dir = st.Cwd
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil {
		return TypedValue{Type: ValueTypeText, Text: text}, fmt.Errorf("shell step %q: %w", st.Name, err)
	}
	return TypedValue{Type: ValueTypeText, Text: text}, nil
}

// ===========================================================================
// read_artifact (WP05) — loads an artifact's bytes + metadata.
// ===========================================================================

type readArtifactRunner struct {
	art ArtifactsReadWriter
}

func (readArtifactRunner) Validate(st Step) error {
	if st.ArtifactIDRef == "" {
		return fmt.Errorf("read_artifact step %q: artifact_id_ref required", st.Name)
	}
	return nil
}

func (r readArtifactRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	if r.art == nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("read_artifact step %q: %w (no Artifacts wired)", st.Name, errDepUnavailable)
	}
	view, err := r.art.Read(ctx, st.ArtifactIDRef)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("read_artifact step %q: %w", st.Name, err)
	}
	payload := map[string]any{
		"id":       view.ID,
		"name":     view.Title,
		"mime":     view.MimeType,
		"content":  string(view.Content),
	}
	encoded, _ := json.Marshal(payload)
	return TypedValue{Type: ValueTypeJSON, JSON: payload, Text: string(encoded)}, nil
}

// ===========================================================================
// write_artifact (WP05) — saves new artifact bytes; output is the
// minted artifact id.
// ===========================================================================

type writeArtifactRunner struct {
	art       ArtifactsReadWriter
	sessionID string
}

func (writeArtifactRunner) Validate(st Step) error {
	if st.Title == "" {
		return fmt.Errorf("write_artifact step %q: title required", st.Name)
	}
	if st.Content == "" && st.ContentRef == "" {
		return fmt.Errorf("write_artifact step %q: content or content_ref required", st.Name)
	}
	return nil
}

func (r writeArtifactRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	if r.art == nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("write_artifact step %q: %w (no Artifacts wired)", st.Name, errDepUnavailable)
	}
	if r.sessionID == "" {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("write_artifact step %q: no SessionID configured on engine deps", st.Name)
	}
	mime := st.MimeType
	if mime == "" {
		mime = "text/plain"
	}
	id, err := r.art.Write(ctx, ArtifactWrite{
		SessionID: r.sessionID,
		Title:     st.Title,
		MimeType:  mime,
		Content:   []byte(st.Content),
	})
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("write_artifact step %q: %w", st.Name, err)
	}
	payload := map[string]any{"id": id}
	encoded, _ := json.Marshal(payload)
	return TypedValue{
		Type:       ValueTypeArtifactID,
		ArtifactID: id,
		Text:       string(encoded),
		JSON:       payload,
	}, nil
}

// ===========================================================================
// transform (WP05) — `${...}` ref expansion of the supplied template
// string. Output is the rendered text.
// ===========================================================================

type transformRunner struct{}

func (transformRunner) Validate(st Step) error {
	if st.Template == "" {
		return fmt.Errorf("transform step %q: template required", st.Name)
	}
	return nil
}

func (transformRunner) Run(_ context.Context, st Step, rc *RunContext) (TypedValue, error) {
	out, err := expandRefs(st.Template, rc)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("transform step %q: %w", st.Name, err)
	}
	return TypedValue{Type: ValueTypeText, Text: out}, nil
}

// ===========================================================================
// conditional (WP05) — predicate evaluator + branch selector. Output
// is the chosen branch step name. The Engine consults the conditional
// hook on RunContext to skip past the not-chosen branch step.
// ===========================================================================

type conditionalRunner struct{}

func (conditionalRunner) Validate(st Step) error {
	if st.If == "" {
		return fmt.Errorf("conditional step %q: if required", st.Name)
	}
	if st.ThenStep == "" && st.ElseStep == "" {
		return fmt.Errorf("conditional step %q: then_step or else_step required", st.Name)
	}
	return nil
}

func (conditionalRunner) Run(_ context.Context, st Step, rc *RunContext) (TypedValue, error) {
	expanded, err := expandRefs(st.If, rc)
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("conditional step %q: expand if: %w", st.Name, err)
	}
	taken := evalPredicate(expanded)
	branch := st.ElseStep
	if taken {
		branch = st.ThenStep
	}
	skipBranch := st.ThenStep
	if taken {
		skipBranch = st.ElseStep
	}
	rc.SetBranchSkip(skipBranch)
	return TypedValue{Type: ValueTypeText, Text: branch}, nil
}

// evalPredicate is the beta predicate evaluator. The grammar accepted
// is intentionally narrow:
//
//   - "a == b"   — string equality after trim
//   - "a != b"   — inequality
//   - "a"        — truthy when non-empty and not "false"/"0"/"no"
func evalPredicate(s string) bool {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "=="); i >= 0 {
		return strings.TrimSpace(s[:i]) == strings.TrimSpace(s[i+2:])
	}
	if i := strings.Index(s, "!="); i >= 0 {
		return strings.TrimSpace(s[:i]) != strings.TrimSpace(s[i+2:])
	}
	switch strings.ToLower(s) {
	case "", "false", "0", "no":
		return false
	}
	return true
}

// ===========================================================================
// web_fetch (WP05) — fetches a URL honoring robots.txt + rate-limits.
// Cedar gate: emits "workflow.network.fetch" before any network I/O.
// Audit: logs hostname + status + bytes (never full URL or body).
// ===========================================================================

type webFetchRunner struct {
	fetcher *web.Fetcher
	authz   NetworkAuthorizer
	// audit, when non-nil, receives KindWorkflowNetworkFetch once per
	// successful fetch (audit-that-tells-the-truth-01PMZA10 UNIT-5).
	audit contextaudit.Emitter
}

func (webFetchRunner) Validate(st Step) error {
	if st.URL == "" {
		return fmt.Errorf("web_fetch step %q: url required", st.Name)
	}
	return nil
}

func (r webFetchRunner) Run(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	// Cedar gate: emit before any network I/O.
	if r.authz != nil {
		if err := r.authz.Authorize(ctx, "workflow.network.fetch", st.Name); err != nil {
			return TypedValue{Type: ValueTypeError},
				fmt.Errorf("web_fetch step %q: policy denied: %w", st.Name, err)
		}
	}

	// Privacy: log hostname only.
	host := hostOf(st.URL)

	timeout := time.Duration(st.TimeoutMS) * time.Millisecond
	opts := web.FetchOptions{
		Timeout:   timeout,
		UserAgent: st.UserAgent,
		MinInterval: func() time.Duration {
			if st.MinIntervalMS > 0 {
				return time.Duration(st.MinIntervalMS) * time.Millisecond
			}
			return 0
		}(),
	}

	result, err := r.fetcher.Fetch(ctx, st.URL, opts)
	if err != nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_fetch step %q (host=%s): %w", st.Name, host, err)
	}

	emitNetworkFetch(ctx, r.audit, rc, st, "web_fetch", host, result.Status, len(result.Body))

	payload := map[string]any{
		"kind":    result.Kind,
		"body":    result.Body,
		"status":  result.Status,
		"headers": result.Headers,
	}
	if result.Parsed != nil {
		payload["parsed"] = result.Parsed
	}
	encoded, _ := json.Marshal(payload)
	return TypedValue{Type: ValueTypeJSON, JSON: payload, Text: string(encoded)}, nil
}

// ===========================================================================
// web_scrape (WP05) — fetches a URL then extracts structured data via
// CSS-selector rules (mode="css", default) or LLM prompt (mode="llm").
// Cedar gate: emits "workflow.network.fetch" before any network I/O.
// ===========================================================================

type webScrapeRunner struct {
	fetcher            *web.Fetcher
	llm                LLMStreamer
	profile            string
	// defaultProfileFunc, when non-nil, is called to resolve the active
	// LLM profile when neither the step nor the static profile supply one.
	// FR-006: gap fix so web_scrape llm mode works without an explicit
	// step.profile when a default provider is configured.
	defaultProfileFunc func() string
	authz              NetworkAuthorizer
	// audit, when non-nil, receives KindWorkflowNetworkFetch once per
	// successful fetch (audit-that-tells-the-truth-01PMZA10 UNIT-5).
	audit contextaudit.Emitter
}

func (webScrapeRunner) Validate(st Step) error {
	if st.URL == "" {
		return fmt.Errorf("web_scrape step %q: url required", st.Name)
	}
	switch st.Mode {
	case "", "css", "llm":
	default:
		return fmt.Errorf("web_scrape step %q: mode must be css or llm", st.Name)
	}
	if (st.Mode == "llm" || st.ExtractWithModel != "") && st.ExtractPrompt == "" {
		return fmt.Errorf("web_scrape step %q: llm mode requires extract_prompt", st.Name)
	}
	return nil
}

func (r webScrapeRunner) Run(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	// Cedar gate: emit before any network I/O.
	if r.authz != nil {
		if err := r.authz.Authorize(ctx, "workflow.network.fetch", st.Name); err != nil {
			return TypedValue{Type: ValueTypeError},
				fmt.Errorf("web_scrape step %q: policy denied: %w", st.Name, err)
		}
	}

	host := hostOf(st.URL)

	timeout := time.Duration(st.TimeoutMS) * time.Millisecond
	fetchOpts := web.FetchOptions{
		Timeout:   timeout,
		UserAgent: st.UserAgent,
		MinInterval: func() time.Duration {
			if st.MinIntervalMS > 0 {
				return time.Duration(st.MinIntervalMS) * time.Millisecond
			}
			return 0
		}(),
	}

	result, err := r.fetcher.Fetch(ctx, st.URL, fetchOpts)
	if err != nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q (host=%s): %w", st.Name, host, err)
	}

	emitNetworkFetch(ctx, r.audit, rc, st, "web_scrape", host, result.Status, len(result.Body))

	mode := st.Mode
	if mode == "" {
		mode = "css"
	}

	switch mode {
	case "css":
		return r.runCSS(st, result.Body)
	case "llm":
		return r.runLLM(ctx, st, result.Body)
	default:
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q: unknown mode %q", st.Name, mode)
	}
}

func (r webScrapeRunner) runCSS(st Step, body string) (TypedValue, error) {
	extractors, err := parseExtractors(st.Extractors)
	if err != nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q: parse extractors: %w", st.Name, err)
	}
	out, err := web.ScrapeCSS(body, extractors)
	if err != nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q: CSS extraction: %w", st.Name, err)
	}
	encoded, _ := json.Marshal(out)
	return TypedValue{Type: ValueTypeJSON, JSON: out, Text: string(encoded)}, nil
}

func (r webScrapeRunner) runLLM(ctx context.Context, st Step, body string) (TypedValue, error) {
	if r.llm == nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q: %w (no LLMStreamer wired for llm mode)", st.Name, errDepUnavailable)
	}
	profile := st.Profile
	if profile == "" {
		profile = r.profile
	}
	// FR-006: fall through to DefaultProfileFunc when static profile is absent.
	if profile == "" && r.defaultProfileFunc != nil {
		profile = r.defaultProfileFunc()
	}
	if profile == "" {
		// ExtractWithModel carries the model/profile slug for llm mode.
		profile = st.ExtractWithModel
	}
	if profile == "" {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q: llm mode requires profile or extract_with_model", st.Name)
	}
	text, err := web.ScrapeLLM(ctx, webLLMBridge{r.llm}, body, web.ScrapeLLMOptions{
		Profile: profile,
		Model:   st.Model,
		Prompt:  st.ExtractPrompt,
	})
	if err != nil {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("web_scrape step %q: llm extraction: %w", st.Name, err)
	}
	return TypedValue{Type: ValueTypeText, Text: text}, nil
}

// webLLMBridge adapts the workflows LLMStreamer to the web package's
// mirror interface so we don't import the workflows package from web/.
type webLLMBridge struct{ inner LLMStreamer }

func (b webLLMBridge) Stream(ctx context.Context, req web.LLMRequest) (web.LLMStream, error) {
	s, err := b.inner.Stream(ctx, LLMRequest{
		ProfileID: req.ProfileID,
		Model:     req.Model,
		Prompt:    req.Prompt,
	})
	if err != nil {
		return nil, err
	}
	return webStreamBridge{s}, nil
}

type webStreamBridge struct{ inner LLMStream }

func (b webStreamBridge) Events() <-chan web.LLMStreamEvent {
	ch := make(chan web.LLMStreamEvent, 64)
	go func() {
		defer close(ch)
		for ev := range b.inner.Events() {
			ch <- web.LLMStreamEvent{Text: ev.Text, Err: ev.Err}
		}
	}()
	return ch
}

func (b webStreamBridge) Final() (string, error) { return b.inner.Final() }

// parseExtractors converts the Step.Extractors []any into
// []web.Extractor. Each element must be a map[string]any.
func parseExtractors(raw []any) ([]web.Extractor, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]web.Extractor, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			// YAML loader may decode as map[string]interface{} or
			// map[interface{}]interface{} depending on mode; try the
			// latter too.
			if mi, ok2 := item.(map[interface{}]interface{}); ok2 {
				m = make(map[string]any, len(mi))
				for k, v := range mi {
					m[fmt.Sprint(k)] = v
				}
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("extractors[%d]: expected object", i)
		}
		ex := web.Extractor{
			Name:     stringField(m, "name"),
			Selector: stringField(m, "selector"),
			Attr:     stringField(m, "attr"),
		}
		if b, ok := m["multiple"].(bool); ok {
			ex.Multiple = b
		}
		out = append(out, ex)
	}
	return out, nil
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// hostOf extracts just the hostname from a raw URL string. Returns
// the raw string on parse failure (safe for logging).
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Hostname()
}

// ===========================================================================
// Shared helpers used across runners.
// ===========================================================================

// expandArgs deep-walks a map and resolves ${...} refs in any string
// values. Non-string values pass through.
func expandArgs(in map[string]any, rc *RunContext) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		expanded, err := expandValue(v, rc)
		if err != nil {
			return nil, err
		}
		out[k] = expanded
	}
	return out, nil
}

func expandValue(v any, rc *RunContext) (any, error) {
	switch x := v.(type) {
	case string:
		return expandRefs(x, rc)
	case map[string]any:
		return expandArgs(x, rc)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			val, err := expandValue(item, rc)
			if err != nil {
				return nil, err
			}
			out[i] = val
		}
		return out, nil
	default:
		return v, nil
	}
}

