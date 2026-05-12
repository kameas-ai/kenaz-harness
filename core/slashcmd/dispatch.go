package slashcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
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
			return RunResult{
				Kind:         ResultKindInfo,
				Text:         fmt.Sprintf("would dispatch: %s %s", cmd.Tool, strings.Join(splitArgs, " ")),
				RenderedArgs: splitArgs,
				ToolName:     cmd.Tool,
				Metadata: map[string]any{
					"slash_invocation": "/" + name,
					"tool_dispatched":  cmd.Tool,
					"dry_run":          true,
				},
			}, nil
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
