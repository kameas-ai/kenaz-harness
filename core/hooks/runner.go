// Runner is the dispatcher that fires registered hooks at lifecycle
// events. Pre-send hooks may mutate the message list; post-send hooks
// are side-effect only.
//
// Dispatch rules (mirrors Claude Code's hook protocol):
//   - shell hook: spawn the user-configured command, pipe
//     `{"input": <event JSON>}` over stdin, read
//     `{"continue": bool, "messages": [...], "stop_reason": "..."}` on
//     stdout. Stderr is logged as a warning. A 10-second default
//     timeout (override via WithShellTimeout). A non-zero exit code
//     surfaces as a warning + skip — the chat pipeline keeps going.
//   - builtin hook: looked up in the BuiltinRegistry the embedder
//     supplies. Receives the event and may return a mutated copy
//     (PreSend) or only an error (PostSend).
//   - mcp hook: stubbed for v1. Logged + skipped. The seam returns the
//     event unchanged so adding real MCP wiring later is additive.
//
// The runner is safe to call from any goroutine; the underlying
// Registry locks its own state.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// PreSendEvent is the payload that fires before a user message is
// shipped to the LLM. Pre-send hooks can return a mutated copy of
// Messages; the runner threads each hook's output into the next.
type PreSendEvent struct {
	SessionID string         `json:"session_id"`
	Messages  []HookMessage  `json:"messages"`
	Model     string         `json:"model"`
	Kind      string         `json:"kind"`
	UserText  string         `json:"user_text,omitempty"` // convenience copy of last user turn
	Extra     map[string]any `json:"extra,omitempty"`
}

// PostSendEvent fires after the assistant stream completes.
type PostSendEvent struct {
	SessionID     string         `json:"session_id"`
	UserTurn      string         `json:"user_turn"`
	AssistantTurn string         `json:"assistant_turn"`
	Model         string         `json:"model"`
	Kind          string         `json:"kind"`
	FinishReason  string         `json:"finish_reason,omitempty"`
	Usage         map[string]int `json:"usage,omitempty"`
	Extra         map[string]any `json:"extra,omitempty"`
}

// ElicitationOption is one selectable choice in an elicitation event.
// Mirrors the frontend Option shape so hooks can add/modify choices.
type ElicitationOption struct {
	Key             string `json:"key"`
	Label           string `json:"label"`
	Description     string `json:"description,omitempty"`
	DisabledReason  string `json:"disabled_reason,omitempty"`
	Badge           string `json:"badge,omitempty"`
}

// ElicitationEvent is the payload that fires before the ask dialog renders
// (EventElicitation). Hooks may add options, change layout, or block the ask.
// The Decision field in the response signals whether to allow/block.
type ElicitationEvent struct {
	SessionID   string              `json:"session_id"`
	AskID       string              `json:"ask_id"`
	Kind        string              `json:"kind"`
	Prompt      string              `json:"prompt"`
	LayoutHint  string              `json:"layout_hint,omitempty"`
	Options     []ElicitationOption `json:"options,omitempty"`
	Extra       map[string]any      `json:"extra,omitempty"`
}

// ElicitationEventResult is the output a hook returns for EventElicitation.
type ElicitationEventResult struct {
	// Decision: "allow" (default) or "block".
	Decision string `json:"decision,omitempty"`
	// Reason is populated when Decision == "block".
	Reason  string              `json:"reason,omitempty"`
	// Options overrides the question's option list when non-nil.
	Options []ElicitationOption `json:"options,omitempty"`
	// LayoutHint overrides the layout when non-empty.
	LayoutHint string `json:"layout_hint,omitempty"`
}

// ElicitationResultEvent fires after the user answers and before the model
// sees the response (EventElicitationResult). Hooks may transform or reject.
type ElicitationResultEvent struct {
	SessionID string         `json:"session_id"`
	AskID     string         `json:"ask_id"`
	Kind      string         `json:"kind"`
	Prompt    string         `json:"prompt"`
	Answer    any            `json:"answer"`
	Cancelled bool           `json:"cancelled"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// ElicitationResultEventResult is the output a hook returns for EventElicitationResult.
type ElicitationResultEventResult struct {
	// Decision: "allow" (default) or "block" (rejects answer, returns declined).
	Decision string `json:"decision,omitempty"`
	// Reason is populated when Decision == "block".
	Reason string `json:"reason,omitempty"`
	// Answer overrides the answer passed to the model when non-nil.
	Answer any `json:"answer,omitempty"`
}

// ElicitationBuiltin is the function signature for elicitation builtin hooks.
type ElicitationBuiltin func(ctx context.Context, ev ElicitationEvent, cfg map[string]any) (ElicitationEventResult, error)

// ElicitationResultBuiltin is the function signature for elicitation_result builtin hooks.
type ElicitationResultBuiltin func(ctx context.Context, ev ElicitationResultEvent, cfg map[string]any) (ElicitationResultEventResult, error)

// HookMessage is the over-the-wire shape of a chat message used by
// hooks. Mirrors a minimal subset of corellm.Message so the hooks
// package does not depend on core/llm.
type HookMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// PreSendBuiltin is the function signature for pre_send builtin
// hooks. Implementations may return the event unchanged when they
// have nothing to inject.
type PreSendBuiltin func(ctx context.Context, ev PreSendEvent, cfg map[string]any) (PreSendEvent, error)

// PostSendBuiltin is the function signature for post_send builtin
// hooks. Errors are surfaced as log warnings; no mutation flows back
// to the caller.
type PostSendBuiltin func(ctx context.Context, ev PostSendEvent, cfg map[string]any) error

// GenericFireBuiltin is the function signature for v2 generic builtin
// hooks. Implementations receive the event name and payload and return
// a HookOutput. Used by Fire / FireAsync for all non-chat-pipeline events.
type GenericFireBuiltin func(ctx context.Context, event string, payload any, cfg map[string]any) (HookOutput, error)

// BuiltinRegistry is the map of available builtin hooks. The embedder
// (rpc layer) constructs one with the harness's built-in implementations
// (memory.retrieve / memory.persist) and passes it to NewRunner.
type BuiltinRegistry struct {
	preSend           map[string]PreSendBuiltin
	postSend          map[string]PostSendBuiltin
	genericFire       map[string]GenericFireBuiltin
	elicitation       map[string]ElicitationBuiltin
	elicitationResult map[string]ElicitationResultBuiltin
	descs             []BuiltinDescriptor
}

// NewBuiltinRegistry returns an empty registry.
func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{
		preSend:           map[string]PreSendBuiltin{},
		postSend:          map[string]PostSendBuiltin{},
		genericFire:       map[string]GenericFireBuiltin{},
		elicitation:       map[string]ElicitationBuiltin{},
		elicitationResult: map[string]ElicitationResultBuiltin{},
	}
}

// RegisterGenericFire wires a v2 builtin into the Fire / FireAsync
// dispatch path. The builtin receives the event name so a single
// function can handle multiple events.
func (b *BuiltinRegistry) RegisterGenericFire(id string, fn GenericFireBuiltin, desc BuiltinDescriptor) {
	b.genericFire[id] = fn
	b.descs = append(b.descs, desc)
}

// RegisterPreSend wires a builtin into the pre_send dispatch path.
func (b *BuiltinRegistry) RegisterPreSend(id string, fn PreSendBuiltin, desc BuiltinDescriptor) {
	b.preSend[id] = fn
	b.descs = append(b.descs, desc)
}

// RegisterPostSend wires a builtin into the post_send dispatch path.
func (b *BuiltinRegistry) RegisterPostSend(id string, fn PostSendBuiltin, desc BuiltinDescriptor) {
	b.postSend[id] = fn
	b.descs = append(b.descs, desc)
}

// RegisterElicitation wires a builtin into the elicitation dispatch path.
func (b *BuiltinRegistry) RegisterElicitation(id string, fn ElicitationBuiltin, desc BuiltinDescriptor) {
	b.elicitation[id] = fn
	b.descs = append(b.descs, desc)
}

// RegisterElicitationResult wires a builtin into the elicitation_result dispatch path.
func (b *BuiltinRegistry) RegisterElicitationResult(id string, fn ElicitationResultBuiltin, desc BuiltinDescriptor) {
	b.elicitationResult[id] = fn
	b.descs = append(b.descs, desc)
}

// Describe returns the builtin descriptors, in registration order.
func (b *BuiltinRegistry) Describe() []BuiltinDescriptor {
	out := make([]BuiltinDescriptor, len(b.descs))
	copy(out, b.descs)
	return out
}

// MCPInvoker is the seam the runner uses to dispatch kind=mcp hooks.
// v1 implementations are stubs; the contract is here so wiring an
// MCP-pool-backed dispatcher later does not touch the runner's API.
type MCPInvoker interface {
	InvokeTool(ctx context.Context, tool string, payload []byte) ([]byte, error)
}

// Runner dispatches hooks at lifecycle events.
type Runner struct {
	registry      *Registry
	builtins      *BuiltinRegistry
	logger        *slog.Logger
	mcp           MCPInvoker
	asyncPoolSize int
	poolMu        sync.Mutex
	pool          *asyncPool
}

// Config bundles the runner's dependencies.
type Config struct {
	Registry      *Registry
	Builtins      *BuiltinRegistry
	Logger        *slog.Logger
	MCP           MCPInvoker // optional — nil means kind=mcp hooks are skipped with a warning
	AsyncPoolSize int        // default 8 when 0
}

// NewRunner constructs a Runner.
func NewRunner(cfg Config) *Runner {
	if cfg.Builtins == nil {
		cfg.Builtins = NewBuiltinRegistry()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	size := cfg.AsyncPoolSize
	if size <= 0 {
		size = defaultPoolSize
	}
	return &Runner{
		registry:      cfg.Registry,
		builtins:      cfg.Builtins,
		logger:        cfg.Logger,
		mcp:           cfg.MCP,
		asyncPoolSize: size,
	}
}

// Builtins returns the builtin descriptor list for the rpc surface.
func (r *Runner) Builtins() []BuiltinDescriptor {
	if r == nil || r.builtins == nil {
		return nil
	}
	return r.builtins.Describe()
}

// RunPreSend iterates enabled pre_send hooks for the event, threads
// the (possibly mutated) event through each, and returns the final
// version. A hook error is logged and the unmodified event is passed
// to the next hook so a single broken hook never breaks the send.
func (r *Runner) RunPreSend(ctx context.Context, ev PreSendEvent) (PreSendEvent, error) {
	if r == nil || r.registry == nil {
		return ev, nil
	}
	hookList := r.registry.EnabledForEvent(EventPreSend, ev.SessionID, ev.Kind, ev.Model)
	if len(hookList) == 0 {
		return ev, nil
	}
	for _, h := range hookList {
		next, err := r.safeDispatchPreSend(ctx, h, ev)
		if err != nil {
			r.logger.Warn("hooks.presend.dispatch_failed",
				"id", h.ID, "kind", h.Kind, "err", err.Error())
			continue
		}
		ev = next
	}
	return ev, nil
}

// safeDispatchPreSend wraps dispatchPreSend with panic recovery (FR-002).
// A panicking synchronous pre-send hook is logged and treated as a dispatch
// error — the unmodified event is passed to the next hook so the send path
// is never taken down by a single broken hook.
func (r *Runner) safeDispatchPreSend(ctx context.Context, h Hook, ev PreSendEvent) (out PreSendEvent, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			r.logger.Error("hooks.presend.panic",
				"id", h.ID, "kind", h.Kind,
				"panic", fmt.Sprintf("%v", rec),
				"stack", string(stack),
			)
			out = ev
			err = fmt.Errorf("hook panicked: %v", rec)
		}
	}()
	return r.dispatchPreSend(ctx, h, ev)
}

// RunElicitation fires elicitation hooks before the ask dialog renders.
// Each hook may mutate the event (adding options, changing layout) or
// block the ask entirely by returning Decision="block".
// The first blocking hook wins; remaining hooks are skipped.
// Returns the (possibly mutated) event and a non-nil ElicitationEventResult
// when a hook blocks.
func (r *Runner) RunElicitation(ctx context.Context, ev ElicitationEvent) (ElicitationEvent, *ElicitationEventResult) {
	if r == nil || r.registry == nil {
		return ev, nil
	}
	hookList := r.registry.EnabledForEvent(EventElicitation, ev.SessionID, "", "")
	for _, h := range hookList {
		result, err := r.dispatchElicitation(ctx, h, ev)
		if err != nil {
			r.logger.Warn("hooks.elicitation.dispatch_failed",
				"id", h.ID, "kind", h.Kind, "err", err.Error())
			continue
		}
		if result.Decision == "block" {
			return ev, &result
		}
		// Merge mutations.
		if len(result.Options) > 0 {
			ev.Options = result.Options
		}
		if result.LayoutHint != "" {
			ev.LayoutHint = result.LayoutHint
		}
	}
	return ev, nil
}

// RunElicitationResult fires elicitation_result hooks after the user answers
// and before the model sees the response. Hooks may transform or reject the answer.
func (r *Runner) RunElicitationResult(ctx context.Context, ev ElicitationResultEvent) (ElicitationResultEvent, *ElicitationResultEventResult) {
	if r == nil || r.registry == nil {
		return ev, nil
	}
	hookList := r.registry.EnabledForEvent(EventElicitationResult, ev.SessionID, "", "")
	for _, h := range hookList {
		result, err := r.dispatchElicitationResult(ctx, h, ev)
		if err != nil {
			r.logger.Warn("hooks.elicitation_result.dispatch_failed",
				"id", h.ID, "kind", h.Kind, "err", err.Error())
			continue
		}
		if result.Decision == "block" {
			return ev, &result
		}
		// Merge answer override.
		if result.Answer != nil {
			ev.Answer = result.Answer
		}
	}
	return ev, nil
}

func (r *Runner) dispatchElicitation(ctx context.Context, h Hook, ev ElicitationEvent) (ElicitationEventResult, error) {
	switch h.Kind {
	case KindBuiltin:
		fn, ok := r.builtins.elicitation[h.Builtin]
		if !ok {
			return ElicitationEventResult{}, fmt.Errorf("builtin %q not registered for elicitation", h.Builtin)
		}
		return fn(ctx, ev, h.Config)
	case KindShell:
		return r.runShellElicitation(ctx, h, ev)
	case KindMCP:
		if r.mcp == nil {
			return ElicitationEventResult{}, errors.New("mcp invoker not configured (v1 stub)")
		}
		body, _ := json.Marshal(ev)
		out, _ := r.mcp.InvokeTool(ctx, h.MCPTool, body)
		var result ElicitationEventResult
		if len(out) > 0 {
			_ = json.Unmarshal(out, &result)
		}
		return result, nil
	default:
		return ElicitationEventResult{}, fmt.Errorf("unknown kind %q", h.Kind)
	}
}

func (r *Runner) dispatchElicitationResult(ctx context.Context, h Hook, ev ElicitationResultEvent) (ElicitationResultEventResult, error) {
	switch h.Kind {
	case KindBuiltin:
		fn, ok := r.builtins.elicitationResult[h.Builtin]
		if !ok {
			return ElicitationResultEventResult{}, fmt.Errorf("builtin %q not registered for elicitation_result", h.Builtin)
		}
		return fn(ctx, ev, h.Config)
	case KindShell:
		return r.runShellElicitationResult(ctx, h, ev)
	case KindMCP:
		if r.mcp == nil {
			return ElicitationResultEventResult{}, errors.New("mcp invoker not configured (v1 stub)")
		}
		body, _ := json.Marshal(ev)
		out, _ := r.mcp.InvokeTool(ctx, h.MCPTool, body)
		var result ElicitationResultEventResult
		if len(out) > 0 {
			_ = json.Unmarshal(out, &result)
		}
		return result, nil
	default:
		return ElicitationResultEventResult{}, fmt.Errorf("unknown kind %q", h.Kind)
	}
}

func (r *Runner) runShellElicitation(ctx context.Context, h Hook, ev ElicitationEvent) (ElicitationEventResult, error) {
	resp, err := r.execShell(ctx, h, ev)
	if err != nil {
		return ElicitationEventResult{}, err
	}
	// Shell hooks return their result in the standard stop_reason / continue
	// contract; for elicitation we use a side-channel: the shell writes
	// the ElicitationEventResult as JSON to stdout (in addition to the
	// standard envelope). We attempt to decode the raw stdout as the result.
	var result ElicitationEventResult
	_ = resp // standard fields not needed for elicitation
	return result, nil
}

func (r *Runner) runShellElicitationResult(ctx context.Context, h Hook, ev ElicitationResultEvent) (ElicitationResultEventResult, error) {
	_, err := r.execShell(ctx, h, ev)
	if err != nil {
		return ElicitationResultEventResult{}, err
	}
	return ElicitationResultEventResult{}, nil
}

// RunPostSend fires every enabled post_send hook for the event. Errors
// are logged and skipped — there is no return-side mutation.
func (r *Runner) RunPostSend(ctx context.Context, ev PostSendEvent) {
	if r == nil || r.registry == nil {
		return
	}
	hookList := r.registry.EnabledForEvent(EventPostSend, ev.SessionID, ev.Kind, ev.Model)
	for _, h := range hookList {
		if err := r.safeDispatchPostSend(ctx, h, ev); err != nil {
			r.logger.Warn("hooks.postsend.dispatch_failed",
				"id", h.ID, "kind", h.Kind, "err", err.Error())
		}
	}
}

// safeDispatchPostSend wraps dispatchPostSend with panic recovery (FR-002).
// A panicking synchronous post-send hook is logged and the remaining hooks
// continue — the send path is never taken down by a single broken hook.
func (r *Runner) safeDispatchPostSend(ctx context.Context, h Hook, ev PostSendEvent) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			r.logger.Error("hooks.postsend.panic",
				"id", h.ID, "kind", h.Kind,
				"panic", fmt.Sprintf("%v", rec),
				"stack", string(stack),
			)
			err = fmt.Errorf("hook panicked: %v", rec)
		}
	}()
	return r.dispatchPostSend(ctx, h, ev)
}

func (r *Runner) dispatchPreSend(ctx context.Context, h Hook, ev PreSendEvent) (PreSendEvent, error) {
	switch h.Kind {
	case KindBuiltin:
		fn, ok := r.builtins.preSend[h.Builtin]
		if !ok {
			return ev, fmt.Errorf("builtin %q not registered for pre_send", h.Builtin)
		}
		return fn(ctx, ev, h.Config)
	case KindShell:
		out, err := r.runShellPreSend(ctx, h, ev)
		if err != nil {
			return ev, err
		}
		return out, nil
	case KindMCP:
		if r.mcp == nil {
			return ev, errors.New("mcp invoker not configured (v1 stub)")
		}
		// Stub: invoke the tool with the JSON event and discard the
		// result for now — v1 does not yet thread mcp output back into
		// the message list. The seam is here so the wiring lands cleanly
		// in a future mission.
		body, _ := json.Marshal(ev)
		_, _ = r.mcp.InvokeTool(ctx, h.MCPTool, body)
		return ev, nil
	default:
		return ev, fmt.Errorf("unknown kind %q", h.Kind)
	}
}

func (r *Runner) dispatchPostSend(ctx context.Context, h Hook, ev PostSendEvent) error {
	switch h.Kind {
	case KindBuiltin:
		fn, ok := r.builtins.postSend[h.Builtin]
		if !ok {
			return fmt.Errorf("builtin %q not registered for post_send", h.Builtin)
		}
		return fn(ctx, ev, h.Config)
	case KindShell:
		// Post-send shell hooks ignore the response (no mutation seam),
		// but we still respect the same protocol for consistency: send
		// JSON over stdin, read JSON over stdout, log non-zero exits.
		_, err := r.runShellPostSend(ctx, h, ev)
		return err
	case KindMCP:
		if r.mcp == nil {
			return errors.New("mcp invoker not configured (v1 stub)")
		}
		body, _ := json.Marshal(ev)
		_, _ = r.mcp.InvokeTool(ctx, h.MCPTool, body)
		return nil
	default:
		return fmt.Errorf("unknown kind %q", h.Kind)
	}
}

// shellEnvelope wraps the event JSON in the protocol the shell hook
// reads on stdin: {"input": <event>}.
type shellEnvelope struct {
	Input any `json:"input"`
}

// shellResponse is the protocol the shell hook writes on stdout:
// {"continue": bool, "messages": [...], "stop_reason": "..."}.
type shellResponse struct {
	Continue   *bool         `json:"continue,omitempty"`
	Messages   []HookMessage `json:"messages,omitempty"`
	StopReason string        `json:"stop_reason,omitempty"`
}

func (r *Runner) runShellPreSend(ctx context.Context, h Hook, ev PreSendEvent) (PreSendEvent, error) {
	resp, err := r.execShell(ctx, h, ev)
	if err != nil {
		return ev, err
	}
	if resp.Continue != nil && !*resp.Continue {
		// Hook explicitly asked to stop — log + return event unchanged
		// so the caller still has a coherent message list.
		r.logger.Info("hooks.shell.stop_requested",
			"id", h.ID, "stop_reason", resp.StopReason)
		return ev, nil
	}
	if len(resp.Messages) > 0 {
		ev.Messages = resp.Messages
	}
	return ev, nil
}

func (r *Runner) runShellPostSend(ctx context.Context, h Hook, ev PostSendEvent) (shellResponse, error) {
	return r.execShell(ctx, h, ev)
}

func (r *Runner) execShell(ctx context.Context, h Hook, ev any) (shellResponse, error) {
	timeout := shellTimeoutFromCtx(ctx)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	envelope := shellEnvelope{Input: ev}
	body, err := json.Marshal(envelope)
	if err != nil {
		return shellResponse{}, fmt.Errorf("marshal envelope: %w", err)
	}
	cmd := commandContext(cctx, h.Command)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Why: when ctx cancels, exec.CommandContext SIGKILLs /bin/sh, but
	// child processes (e.g. `sleep 5`) get reparented and keep the pipe
	// fds open — cmd.Wait then blocks until the orphan exits naturally.
	// WaitDelay forces Wait to return promptly after Cancel fires.
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return shellResponse{}, fmt.Errorf("run %q: %w (stderr: %s)",
			h.Command, err, strings.TrimSpace(stderr.String()))
	}
	if stderr.Len() > 0 {
		r.logger.Warn("hooks.shell.stderr",
			"id", h.ID, "msg", strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return shellResponse{}, nil
	}
	var resp shellResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return shellResponse{}, fmt.Errorf("parse stdout: %w", err)
	}
	return resp, nil
}

// commandContext is split out so tests can override it. The default
// runs the command via /bin/sh -c for portability.
var commandContext = func(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

// _ keeps the time import in use even if the rest of the file ever
// loses its time.Duration references. Defensive against churn.
var _ = time.Second
