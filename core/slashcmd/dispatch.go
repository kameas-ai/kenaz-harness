package slashcmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// RunResult is the output of a user command execution. The Kind field
// mirrors ResultKind* constants so the RPC layer can forward it directly
// to the existing ExecuteResult wire shape.
//
// Cross-mission contract: the __skill builtin tool in the
// model-invoked-skills-catalog mission calls Dispatch.Run with the same
// signature. Run must produce no UI side effects; it emits events that
// callers route to the appropriate surface.
type RunResult struct {
	// Kind is one of ResultKindInfo / Error / Warning / System.
	Kind string
	// Text is the rendered output for kind:text and kind:prompt commands,
	// or a human-readable summary for kind:tool commands.
	Text string
	// RenderedArgs is the []string passed to the tool for kind:tool commands.
	// Populated only for kind:tool; nil otherwise.
	RenderedArgs []string
	// ToolName is the tool name for kind:tool commands.
	ToolName string
	// Metadata carries arbitrary key/value pairs for the frontend (same
	// shape as Result.Metadata).
	Metadata map[string]any
}

// ErrNotModelInvokable is returned by RunModelInvoked when the resolved
// command's model_invokable frontmatter flag is false (the default). The
// command exists and a human can still run it via the RPC path (Run) —
// this error only means the model is not permitted to invoke it.
var ErrNotModelInvokable = errors.New("slashcmd: command is not model-invokable")

// ToolDispatcher is the narrow interface the dispatch layer uses to
// invoke a tool by name. The toolloop package satisfies this interface;
// tests stub it.
//
// Cross-mission note: this interface is intentionally narrow so the
// __skill builtin can provide its own dispatcher.
type ToolDispatcher interface {
	// DispatchTool invokes the named tool with the given args and
	// returns the tool output as a string. args is a []string — never
	// a shell-concatenated string.
	DispatchTool(ctx context.Context, toolName string, args []string) (string, error)
}

// SessionContext carries the per-session resolved variables needed for
// template substitution and audit logging.
type SessionContext struct {
	SessionID string
	ProjectID string
	CWD       string
	Selection string
	Date      string            // ISO-8601 YYYY-MM-DD; zero → today
	UserArgs  map[string]string // populated internally for audit; callers pass args to Run
}

// Dispatch is the central dispatch point for user command execution.
// It is reused by both the human-invoked RPC path and the model-invoked
// __skill builtin tool. The dispatch layer is intentionally UI-agnostic:
// it returns a RunResult that callers route to the appropriate surface.
type Dispatch struct {
	store    *Store
	tools    ToolDispatcher // may be nil (kind:tool commands return an error)
	auditor  audit.Emitter  // may be nil (no-op)
}

// NewDispatch constructs a Dispatch. tools may be nil if tool dispatch
// is not needed (e.g. in tests that only test kind:text / kind:prompt).
func NewDispatch(store *Store, tools ToolDispatcher) *Dispatch {
	return &Dispatch{store: store, tools: tools}
}

// WithAuditEmitter injects an audit.Emitter into the Dispatch.
// Nil is a no-op. The emitter is called synchronously from Run; the
// credstore pattern of async drain is not needed here because the audit
// emit is itself non-blocking at the emitter level.
func (d *Dispatch) WithAuditEmitter(em audit.Emitter) *Dispatch {
	d.auditor = em
	return d
}

// Run resolves the named command, substitutes template variables, and
// dispatches by kind. It is safe for concurrent use.
//
// Dispatch contract for the skills-catalog mission:
//
//	func (d *Dispatch) Run(ctx, name, args, sessionCtx) (RunResult, error)
//
// args is a map of user-provided argument values keyed by input.Name.
// sessionCtx carries the resolved built-in variables. The returned
// RunResult carries no Wails-specific types so it is safe to call from
// the __skill tool handler without importing the rpc package.
func (d *Dispatch) Run(
	ctx context.Context,
	name string,
	args map[string]string,
	sc SessionContext,
) (res RunResult, rerr error) {
	// Audit emission deferred so it fires on every return path — success
	// or failure — and always has the final result and error available.
	// The cmd variable is captured by pointer so the closure sees the
	// resolved command even when loading succeeds after the defer is set.
	var cmd UserCommand
	defer func() {
		if d.auditor != nil && cmd.Name != "" {
			// Augment sc with user args for arg-name extraction.
			auditSC := sc
			auditSC.UserArgs = args
			EmitRun(ctx, d.auditor, cmd, res, rerr, auditSC)
		}
	}()

	// Feature flag gate: built-in commands bypass this path entirely (they
	// go through Registry.Execute, not Dispatch.Run). If the flag is off,
	// reject user command dispatch while leaving built-ins functional.
	if !UserSlashcmdEnabled() {
		return RunResult{
			Kind: ResultKindError,
			Text: ErrFeatureDisabled.Error(),
		}, ErrFeatureDisabled
	}

	var err error
	cmd, err = d.store.LoadUserOne(ctx, name, sc.ProjectID)
	if err != nil {
		if errors.Is(err, ErrCommandNotFound) {
			return RunResult{
				Kind: ResultKindError,
				Text: fmt.Sprintf("user command %q not found", name),
			}, err
		}
		return RunResult{Kind: ResultKindError, Text: "failed to load command"}, err
	}

	date := sc.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	vars := TemplateVars{
		UserArgs:  args,
		Selection: sc.Selection,
		CWD:       sc.CWD,
		Date:      date,
		SessionID: sc.SessionID,
		ProjectID: sc.ProjectID,
	}

	switch cmd.Kind {
	case KindText:
		return RunResult{
			Kind: ResultKindInfo,
			Text: cmd.Body,
		}, nil

	case KindPrompt:
		rendered, renderErr := Render(cmd.Body, vars)
		if renderErr != nil {
			return RunResult{
				Kind: ResultKindError,
				Text: fmt.Sprintf("template error: %v", renderErr),
			}, renderErr
		}
		return RunResult{
			Kind: ResultKindInfo,
			Text: rendered,
			Metadata: map[string]any{
				"slash_invocation": "/" + name,
				"prompt_rendered":  true,
			},
		}, nil

	case KindTool:
		rendered, renderErr := Render(cmd.ToolArgsTemplate, vars)
		if renderErr != nil {
			return RunResult{
				Kind: ResultKindError,
				Text: fmt.Sprintf("template error: %v", renderErr),
			}, renderErr
		}
		splitArgs := SplitArgs(rendered)
		if d.tools == nil {
			// automation-actually-runs-01PMZ404 UNIT-3: this branch used to
			// return ResultKindInfo with a nil error and a "would dispatch"
			// bubble that looked harmless — the field's own doc comment
			// above (`tools ToolDispatcher // may be nil (kind:tool
			// commands return an error)`) already promised this behaviour;
			// the code just never did it. Nothing dispatched; say so.
			err := fmt.Errorf("tool dispatch is not configured: %q was not run", cmd.Tool)
			return RunResult{
				Kind:         ResultKindError,
				Text:         err.Error(),
				RenderedArgs: splitArgs,
				ToolName:     cmd.Tool,
				Metadata: map[string]any{
					"slash_invocation": "/" + name,
					"tool_dispatched":  cmd.Tool,
					"dry_run":          true,
				},
			}, err
		}
		output, dispErr := d.tools.DispatchTool(ctx, cmd.Tool, splitArgs)
		if dispErr != nil {
			return RunResult{
				Kind: ResultKindError,
				Text: fmt.Sprintf("tool %q failed: %v", cmd.Tool, dispErr),
			}, dispErr
		}
		return RunResult{
			Kind:         ResultKindInfo,
			Text:         output,
			RenderedArgs: splitArgs,
			ToolName:     cmd.Tool,
			Metadata: map[string]any{
				"slash_invocation": "/" + name,
				"tool_dispatched":  cmd.Tool,
			},
		}, nil

	default:
		return RunResult{
			Kind: ResultKindError,
			Text: fmt.Sprintf("unknown command kind %q", cmd.Kind),
		}, fmt.Errorf("slashcmd: unknown kind %q", cmd.Kind)
	}
}

// RunModelInvoked is the model-invoked entry point into dispatch. It is
// called exclusively by the kenaz__skill builtin tool
// (core/tools/skill), the model's only path into user-defined slash
// commands. Human-invoked dispatch — the RPC path at
// core/rpc/views/slashcmd/impl.go UserRun — calls Run directly and is
// unaffected by this method: model_invokable restricts what the model
// may run, not what the user may run.
//
// trust-surfaces-that-fire-01PMZ202 WP20 (formerly WP14). Before this
// method existed, LoadUserOne already scanned model_invokable onto the
// struct (it has no WHERE-clause predicate — it is a discovery-only
// flag), but nothing at dispatch read it: the model could name any
// command, including one the user explicitly left off the model-invokable
// catalog. skill.go's own doc comment claimed the opposite.
//
// RunModelInvoked loads the command once to check ModelInvokable before
// executing. A command with model_invokable=false (the default) is
// refused with ErrNotModelInvokable and never reaches Run — no text,
// prompt or tool body is ever produced for a model-invoked call the user
// did not authorize. The refusal is itself audited (mirroring Run's own
// defer) so a rejected attempt by the model is visible in the audit log,
// not merely swallowed by an error return.
func (d *Dispatch) RunModelInvoked(
	ctx context.Context,
	name string,
	args map[string]string,
	sc SessionContext,
) (RunResult, error) {
	// Same feature-flag gate Run applies, checked up front so a disabled
	// feature surfaces identically regardless of caller.
	if !UserSlashcmdEnabled() {
		return RunResult{
			Kind: ResultKindError,
			Text: ErrFeatureDisabled.Error(),
		}, ErrFeatureDisabled
	}

	cmd, err := d.store.LoadUserOne(ctx, name, sc.ProjectID)
	if err != nil {
		if errors.Is(err, ErrCommandNotFound) {
			return RunResult{
				Kind: ResultKindError,
				Text: fmt.Sprintf("user command %q not found", name),
			}, err
		}
		return RunResult{Kind: ResultKindError, Text: "failed to load command"}, err
	}

	if !cmd.ModelInvokable {
		res := RunResult{
			Kind: ResultKindError,
			Text: fmt.Sprintf("command %q is not model-invokable", name),
		}
		rerr := fmt.Errorf("%w: %q", ErrNotModelInvokable, name)
		if d.auditor != nil {
			auditSC := sc
			auditSC.UserArgs = args
			EmitRun(ctx, d.auditor, cmd, res, rerr, auditSC)
		}
		return res, rerr
	}

	// Eligible — delegate to the shared dispatch path. Run reloads the
	// command; the extra read matches the cost TraceRun already pays for
	// the same reason (span/guard metadata needed before the switch).
	return d.Run(ctx, name, args, sc)
}
