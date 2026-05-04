package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// runnerRegistry is mutated only by RegisterStepRunner at init time.
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
}

// DefaultRunners returns the package-default StepRunner registry.
// Callers (notably the rpc impl) inject this into Engine.Runners.
//
// The returned runners have nil deps — they error at Run time when
// an external dependency (LLM, Tools, MCP, Artifacts) is required
// but unavailable. Callers wiring real deps should use
// DefaultRunnersWithDeps instead.
func DefaultRunners() map[StepKind]StepRunner {
	out := make(map[StepKind]StepRunner, len(runnerRegistry))
	for k, v := range runnerRegistry {
		out[k] = v
	}
	return out
}

// DefaultRunnersWithDeps returns the package-default registry with
// each dep-needing runner pre-bound to the supplied Deps. Runners
// for kinds whose dep is nil retain the no-dep behavior (error at Run
// time); the rest dispatch through the supplied dependency.
func DefaultRunnersWithDeps(deps Deps) map[StepKind]StepRunner {
	out := DefaultRunners()
	if deps.LLM != nil || deps.DefaultLLMProfile != "" {
		out[StepKindModelTurn] = modelTurnRunner{llm: deps.LLM, defaultProfile: deps.DefaultLLMProfile}
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
// ===========================================================================

type modelTurnRunner struct {
	llm            LLMStreamer
	defaultProfile string
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
	if profile == "" {
		return TypedValue{Type: ValueTypeError},
			fmt.Errorf("model_turn step %q: no profile (set step.profile or Deps.DefaultLLMProfile)", st.Name)
	}
	stream, err := r.llm.Stream(ctx, LLMRequest{
		ProfileID: profile,
		Model:     st.Model,
		Prompt:    st.UserPrompt,
	})
	if err != nil {
		return TypedValue{Type: ValueTypeError}, fmt.Errorf("model_turn step %q: %w", st.Name, err)
	}
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

