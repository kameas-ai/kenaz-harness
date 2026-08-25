package bash

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	cedargo "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// DefaultAllowlist is the set of safe-by-default commands the bash
// tool permits when no Cedar engine is wired (legacy fallback path).
// It is intentionally conservative: read-only inspection tools,
// common interpreters, and language-level build tooling. Anything
// destructive (rm, dd, kill, mv) is omitted; users who want it can
// extend the per-installation list via Settings.BashAllowlist.
//
// When a Cedar engine is wired (WP03+), this list is not consulted;
// the Cedar policy bundle governs access instead.
var DefaultAllowlist = []string{
	"ls", "cat", "head", "tail", "grep", "find", "wc", "file", "stat",
	"du", "df", "which", "type", "echo", "pwd", "env", "date", "uname",
	"git", "python", "python3", "node", "go", "cargo", "npm", "npx",
	"make", "gcc", "clang", "ruby", "rustc",
}

// Allows reports whether name (the basename of argv[0]) appears in
// allowlist. Match is exact-name; no globbing, no path traversal.
// Callers MUST pass a basename — the allowlist is checked BEFORE
// exec.LookPath so a planted binary at "../bin/rm" cannot bypass it
// (NFR-005). An empty allowlist denies every command.
//
// This function is used only on the legacy path when no Cedar engine
// is wired. When a Cedar engine is wired, the Cedar gate governs.
func Allows(allowlist []string, name string) bool {
	if name == "" {
		return false
	}
	for _, allowed := range allowlist {
		if allowed == name {
			return true
		}
	}
	return false
}

// defaultBashRunID returns a hex-encoded 12-byte random id. Mirrors
// the run-id shape used elsewhere in the chassis so logs and cache
// keys look uniform.
func defaultBashRunID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("bash-%d", time.Now().UnixNano())
	}
	return "bash-" + hex.EncodeToString(b[:])
}

// Name is the namespaced tool identifier surfaced to the model. The
// "kenaz__" prefix is reserved for built-in (in-binary) tools so the
// dispatcher can route by prefix without a registry lookup.
const Name = "kenaz__bash"

// description is the user-facing tool description sent to the model.
// It mirrors FR-010's text and the assistant relies on the truncated
// flag + policy gate to know when to retry with a narrower command.
const description = "Execute a shell command via `bash -lc` as the user's own account. " +
	"You can read and write ANY path the user's account can reach — `ls ~/Desktop`, `cat ~/.zshrc`, `pwd`, anything. The shell resolves ~ and $HOME from the user's login profile. " +
	"There is NO sandbox restricting which paths you can touch; the only gate is the Cedar permission system, which prompts the user the first time it sees a new program (e.g. first `aws`, first `git`, first `rm`). Once granted, the program runs without re-prompting. " +
	"Pipes (|), redirects (>, <), command chaining (&&, ;, ||), variable expansion ($VAR), globbing (*), and command substitution ($(...)) all work. " +
	"Each invocation spawns a fresh shell — cwd and exported env vars do NOT persist across calls. Default cwd is the harness's agent-workspace directory, but you can `cd` anywhere within the same command line. " +
	"The Cedar gate evaluates the FIRST command in a chain only — don't hide destructive ops behind a benign first command (e.g. `echo hi && rm -rf foo`); the user only saw `echo` in the prompt. " +
	"Returns stdout, stderr, exit code, and a truncated flag."

// inputSchema is the JSON Schema describing kenaz__bash's argument
// shape (FR-010). Inlined as a constant so InputSchema() can return
// the same json.RawMessage every call without re-serialising.
//
// This is the FOREGROUND schema: it deliberately omits
// `run_in_background` and its companion `description`. Call() ignores
// `run_in_background` unless Options.BackgroundSpawn is wired
// (see the guard in Call), and advertising a knob that silently
// degrades to synchronous execution is worse than not advertising it —
// the model believes it has a task_id it can poll and it does not.
// Same doctrine as the subagent_dispatch registration guard in
// core/rpc/builtins_wiring.go (crash-recovery-tool-gating-0XQTC4RK
// FR-007): do not put a capability in the model's catalog until the
// seam behind it is live.
const inputSchema = `{
  "type": "object",
  "properties": {
    "command": {"type": "string"},
    "working_dir": {"type": "string", "description": "Optional cwd for this invocation. Defaults to the harness agent-workspace; you can also pass any absolute path the user's account can reach."},
    "timeout_seconds": {"type": "integer", "default": 30, "maximum": 300}
  },
  "required": ["command"]
}`

// inputSchemaBackground is the schema returned when Options.BackgroundSpawn
// is wired — i.e. when `run_in_background:true` genuinely spawns a task
// the model can look up by id.
const inputSchemaBackground = `{
  "type": "object",
  "properties": {
    "command": {"type": "string"},
    "working_dir": {"type": "string", "description": "Optional cwd for this invocation. Defaults to the harness agent-workspace; you can also pass any absolute path the user's account can reach."},
    "timeout_seconds": {"type": "integer", "default": 30, "maximum": 300},
    "run_in_background": {"type": "boolean", "default": false, "description": "When true, spawn the command asynchronously and return immediately with a task_id."},
    "description": {"type": "string", "description": "Human-readable label for the background task (shown in the Tasks panel). Only used when run_in_background=true."}
  },
  "required": ["command"]
}`

const (
	defaultTimeoutSeconds = 30
	maxTimeoutSeconds     = 300
)

// Options configures a Tool. SandboxRoot must be an absolute path
// pointing at the agent workspace (typically <DataDir>/agent-workspace).
// Allowlist defaults to DefaultAllowlist when nil; pass an empty slice
// to deny everything (test-only). Logger is optional; nil is silent.
//
// Store is the optional output cache the kernel's read_bash_output
// executor reads from. nil disables run-id tracking; the tool still
// works, but stale runs cannot be re-read by a downstream node.
// IDGen is the run-id generator; nil falls back to a 12-byte hex id.
//
// CedarEngine / PromptRegistry are nil-tolerant. When both are nil the
// gate falls through to the allowlist-based check that preceded WP03
// so the test harness path (New(Options{})) keeps working unchanged.
// When only CedarEngine is wired (and PromptRegistry nil), Evaluate
// returns Allow/Deny normally; NotApplicable falls through as Allow.
// When both are wired the full gate fires: NotApplicable → interactive
// prompt → decision. DataDir is required when writing AllowAlways
// policy snippets; if empty, AllowAlways is treated as AllowOnce.
//
// PermissionCacheDangerousOps is consulted at gate time; when it
// returns true, AllowAlways policy files are persisted even for commands
// classified as dangerous-tier. nil (or false) demotes AllowAlways on a
// dangerous command to AllowOnce with an audit annotation.
//
// It is a FUNCTION, not a bool, because the Settings dial behind it can
// be toggled while the harness runs and the frontend already shows or
// hides the "Allow always" affordance from the live value. Reading it
// once at construction made the dial a lie: the button appeared and the
// backend demoted the grant anyway (unwired sweep, 2026-08-14).
//
// TaskRegistry is the optional background task registry. When non-nil,
// run_in_background:true spawns the command asynchronously, registers
// it in the registry, and returns immediately with a task_id.
// SessionIDFromCtx is the optional function that extracts the session ID
// from a context (used to populate Task.OwnerSessionID).
type Options struct {
	SandboxRoot                 string
	Allowlist                   []string
	Logger                      *slog.Logger
	Store                       *Store
	IDGen                       func() string
	CedarEngine                 *cedar.Engine
	PromptRegistry              *cedar.Registry
	DataDir                     string
	PermissionCacheDangerousOps func() bool
	// BackgroundSpawn is called when run_in_background:true to register
	// the newly-spawned process in the task registry. nil means
	// background mode silently falls back to synchronous execution.
	//
	// Called with pid:0, BEFORE the process starts (subagent-control-
	// and-background-tasks-01PMZB11 UNIT-3): the task id has to exist
	// before cmd.Start() so BackgroundWriters can be attached to
	// cmd.Stdout/cmd.Stderr ahead of time, which is what makes output
	// capturable at all. The real PID is reported afterwards via
	// BackgroundSetPID.
	BackgroundSpawn BackgroundSpawnFunc
	// BackgroundWriters returns the stdout/stderr io.Writers for a task
	// id BackgroundSpawn already registered, so spawnBackground can
	// attach them to cmd.Stdout/cmd.Stderr before Start(). nil (or a
	// false ok) means output is not captured — the process still runs,
	// but Tasks_Tail / kenaz__monitor see nothing for it.
	BackgroundWriters BackgroundWritersFunc
	// BackgroundSetPID records the OS PID once the process has actually
	// started. nil is safe (informational only — feeds the same-process
	// PID liveness check in core/tasks/recovery.go).
	BackgroundSetPID BackgroundSetPIDFunc
	// BackgroundEnd is called when the background process exits.
	// nil is safe; the task will eventually be orphaned (the registry
	// marks it crashed on next boot).
	BackgroundEnd BackgroundEndFunc
	// SessionIDFromCtx extracts the owning session ID from a context.
	// nil means the task is registered without an owner.
	SessionIDFromCtx func(ctx context.Context) string
}

// BackgroundSpawnFunc is the function the bash tool calls to register a
// newly-spawned background process in the task registry. The function
// receives the owning sessionID, the command string, description, and the
// OS PID (after the process is confirmed alive). It returns the task ID.
//
// Defined as a function type (not an interface) to avoid the circular
// import between core/tools/bash and core/tasks.
type BackgroundSpawnFunc func(ctx context.Context, sessionID, cmd, description string, pid int) (taskID string, err error)

// BackgroundWritersFunc returns the stdout/stderr io.Writers for a
// task id already registered via BackgroundSpawn. ok is false when the
// id is unknown (e.g. no task registry wired). Defined against the
// stdlib io.Writer, not a core/tasks type, for the same reason
// BackgroundSpawnFunc is a func type: core/tools/bash must not import
// core/tasks.
type BackgroundWritersFunc func(taskID string) (stdout, stderr io.Writer, ok bool)

// BackgroundSetPIDFunc records the OS PID for an already-registered
// background task, once the process has actually started.
type BackgroundSetPIDFunc func(taskID string, pid int)

// BackgroundEndFunc is the function the bash tool calls when the background
// process exits, to mark the task terminal.
type BackgroundEndFunc func(ctx context.Context, taskID string, exitCode int)

// Tool implements the kenaz__bash built-in tool. It is safe for
// concurrent use; all state is read-only after construction and the
// per-call work happens in stack-local Run/Parse calls.
type Tool struct {
	sandboxRoot                 string
	allowlist                   []string
	logger                      *slog.Logger
	store                       *Store
	idGen                       func() string
	cedarEngine                 *cedar.Engine
	promptRegistry              *cedar.Registry
	dataDir                     string
	permissionCacheDangerousOps func() bool
	backgroundSpawn             BackgroundSpawnFunc
	backgroundWriters           BackgroundWritersFunc
	backgroundSetPID            BackgroundSetPIDFunc
	backgroundEnd               BackgroundEndFunc
	sessionIDFromCtx            func(ctx context.Context) string
}

// New constructs a Tool with the given options. SandboxRoot must be
// non-empty and absolute; an empty value is a programming error.
func New(opts Options) *Tool {
	allow := opts.Allowlist
	if allow == nil {
		allow = DefaultAllowlist
	}
	return &Tool{
		sandboxRoot:                 opts.SandboxRoot,
		allowlist:                   allow,
		logger:                      opts.Logger,
		store:                       opts.Store,
		idGen:                       opts.IDGen,
		cedarEngine:                 opts.CedarEngine,
		promptRegistry:              opts.PromptRegistry,
		dataDir:                     opts.DataDir,
		permissionCacheDangerousOps: opts.PermissionCacheDangerousOps,
		backgroundSpawn:             opts.BackgroundSpawn,
		backgroundWriters:           opts.BackgroundWriters,
		backgroundSetPID:            opts.BackgroundSetPID,
		backgroundEnd:               opts.BackgroundEnd,
		sessionIDFromCtx:            opts.SessionIDFromCtx,
	}
}

// dangerousOpsCacheAllowed reports whether an AllowAlways decision on a
// dangerous-tier command may be persisted as a policy snippet. nil lookup
// means "no" — the safe default, and the one New(Options{}) gets.
func (t *Tool) dangerousOpsCacheAllowed() bool {
	if t.permissionCacheDangerousOps == nil {
		return false
	}
	return t.permissionCacheDangerousOps()
}

// Name returns the namespaced tool identifier (always "kenaz__bash").
func (t *Tool) Name() string { return Name }

// Description returns the user-facing tool description.
func (t *Tool) Description() string { return description }

// InputSchema returns the JSON Schema for the tool's arguments.
// The returned bytes are owned by the caller; mutating them would
// corrupt subsequent calls.
//
// The `run_in_background` knob is advertised only when the background
// seam is wired. Without it, Call() runs the command synchronously no
// matter what the model asks for, so advertising the knob would hand
// the model a task_id contract the harness cannot honour.
func (t *Tool) InputSchema() json.RawMessage {
	if t.backgroundSpawn != nil {
		return json.RawMessage(inputSchemaBackground)
	}
	return json.RawMessage(inputSchema)
}

// callArgs mirrors the FR-010 input_schema. timeout_seconds is a
// pointer so we can distinguish "omitted" (use default 30) from
// "explicitly zero" (treated the same as omitted; FR-010 says default
// 30, max 300, but a zero or negative value is equally meaningless).
type callArgs struct {
	Command         string `json:"command"`
	WorkingDir      string `json:"working_dir,omitempty"`
	TimeoutSeconds  *int   `json:"timeout_seconds,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
	Description     string `json:"description,omitempty"`
}

// callResult mirrors the tool's documented JSON return shape
// (FR-011). Marshalled and returned by Call.
//
// RunID is the optional cache key the read_bash_output node uses to
// re-read this run's transcript. Populated only when the tool was
// constructed with a non-nil Store.
type callResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
	RunID     string `json:"run_id,omitempty"`
}

// backgroundResult is the JSON return shape when run_in_background=true.
// The model uses task_id with __monitor to observe output.
type backgroundResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"` // always "running"
}

// Call dispatches a single tool invocation. Errors that originate
// from invalid input (missing command field) are returned as Go
// errors; errors that originate from the sandbox / policy gate / exec
// layer are surfaced as a successful tool result with an explanatory
// stderr and a non-zero exit_code so the model learns what went wrong
// without the toolloop short-circuiting.
//
// Flow:
//  1. Unmarshal args. Empty command → error.
//  2. Derive first-segment argv via FirstSegmentArgv for Cedar pattern.
//  3. Cedar gate check on first-segment argv (NFR-005). Falls back
//     to allowlist check when no Cedar engine is wired.
//  4. Resolve working_dir under sandboxRoot (FR-013, NFR-004).
//  5. Run via bash -lc with the bounded context + output cap.
//     The shell handles PATH lookup and all metacharacters.
//  6. Marshal RunResult → callResult JSON.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	var args callArgs
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return nil, fmt.Errorf("bash: unmarshal args: %w", err)
		}
	}
	if strings.TrimSpace(args.Command) == "" {
		return nil, errors.New("bash: command is required")
	}

	// Derive argv for Cedar pattern derivation from the first segment.
	// FirstSegmentArgv never returns an error; if parsing fails it
	// falls back to a whitespace split. This is best-effort — the shell
	// is the authoritative parser at runtime.
	argv := FirstSegmentArgv(args.Command)
	if len(argv) == 0 {
		return nil, errors.New("bash: command parsed to empty argv")
	}

	progBase := filepath.Base(argv[0])

	// Cedar gate (WP03). When no engine is wired, fall back to the
	// legacy allowlist so test harnesses and unbooted chassis paths
	// keep working unchanged.
	if t.cedarEngine != nil {
		if allowed, result := t.cedarGate(ctx, argv, args.WorkingDir); !allowed {
			return result, nil
		}
	} else {
		// Legacy allowlist fallback path.
		if !Allows(t.allowlist, progBase) {
			t.logf("bash.denied", "program", progBase)
			return marshalResult(callResult{
				Stderr:    "command not allowed: " + progBase,
				ExitCode:  -1,
				Truncated: false,
			})
		}
	}

	cwd, err := t.resolveWorkingDir(args.WorkingDir)
	if err != nil {
		t.logf("bash.cwd_rejected", "working_dir", args.WorkingDir, "err", err.Error())
		return marshalResult(callResult{
			Stderr:    err.Error(),
			ExitCode:  -1,
			Truncated: false,
		})
	}

	timeout := resolveTimeout(args.TimeoutSeconds)

	t.logf("bash.invoke",
		"program", progBase,
		"cwd", cwd,
		"timeout_seconds", int(timeout/time.Second),
	)

	// ── @secret: reference substitution (WP08) ──────────────────────────
	// Substitute references in the command string. The resolver is wired
	// via context by the chat runner (via refs.WithResolver). When nil
	// (test paths, no resolver wired) the command passes through unchanged.
	commandLine := args.Command
	resolver := refs.ResolverFromContext(ctx)
	if resolver != nil && refs.HasReference(commandLine) {
		// AgentKind: "trusted" — bash runs in-process under harness
		// control (not a third-party MCP server); see the MCP
		// stdio CallTool comment for the "untrusted" counterpart.
		rctx := cedar.ResolveContext{ToolName: Name, AgentKind: "trusted"}
		sub, _, subErr := resolver.Substitute(ctx, commandLine, rctx)
		if subErr != nil {
			return marshalResult(callResult{
				Stderr:   "secret resolution failed: " + subErr.Error(),
				ExitCode: -1,
			})
		}
		commandLine = sub
		// ZeroBuffer is not applicable here since commandLine is a Go string;
		// the internal buffer was already zeroed by refs.Substitute.
	}

	// ── Background mode (run_in_background:true) ────────────────────────
	// The Cedar gate has already run synchronously above — background mode
	// does NOT bypass the policy gate. We spawn the process, confirm it
	// is alive within 100 ms, register it in the task registry, and return
	// immediately with {task_id, status:"running"}.
	if args.RunInBackground && t.backgroundSpawn != nil {
		// commandLine (resolved, post-substitution) is what actually runs.
		// args.Command (unresolved, pre-substitution) is what gets
		// persisted to the task registry / SQLite and written to logs —
		// matching the synchronous path below, which stores args.Command
		// into t.store, never the resolved commandLine.
		return t.spawnBackground(ctx, commandLine, args.Command, cwd, timeout, args.Description)
	}

	res, runErr := Run(ctx, RunOpts{
		CommandLine: commandLine,
		// args.Command (unresolved, pre-substitution) is what may end up
		// inside a %q error label — matching the background path, which
		// persists/logs args.Command (via logCommand), never the resolved
		// commandLine. See exec.go's LogCommandLine doc comment (the
		// seventh @secret: egress finding, release/v0.72.0).
		LogCommandLine: args.Command,
		Cwd:            cwd,
		Timeout:        timeout,
		MaxOutputBytes: DefaultMaxOutputBytes,
	})
	exitCode := res.ExitCode
	rawStderr := string(res.Stderr)
	if runErr != nil {
		// Run already populated the partial buffers and exit -1.
		// Append the error reason so the model can read it.
		if rawStderr != "" && !strings.HasSuffix(rawStderr, "\n") {
			rawStderr += "\n"
		}
		rawStderr += runErr.Error()
		if exitCode == 0 {
			exitCode = -1
		}
		t.logf("bash.run_error", "err", runErr.Error())
	}
	// Sanitize stdout and stderr to redact any resolved plaintext (WP08).
	sanitizer := refs.SanitizerFromContext(ctx)
	stdoutBytes := res.Stdout
	stderrBytes := []byte(rawStderr)
	if sanitizer != nil {
		stdoutBytes = sanitizer.Sanitize(stdoutBytes)
		stderrBytes = sanitizer.Sanitize(stderrBytes)
	}
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)
	runID := ""
	if t.store != nil {
		runID = t.allocRunID()
		t.store.Put(runID, Record{
			Command:  args.Command,
			Stdout:   stdout,
			Stderr:   stderr,
			ExitCode: exitCode,
		})
	}
	return marshalResult(callResult{
		Stdout:    stdout,
		Stderr:    stderr,
		ExitCode:  exitCode,
		Truncated: res.Truncated,
		RunID:     runID,
	})
}

// allocRunID returns a unique run-id for the cache. Falls back to a
// time-based id if crypto/rand is unavailable; in practice this is
// the same defensive posture as core/rpc/views/agentgraph's run-id
// generator and the chance of a collision in a single-user desktop
// app is negligible.
func (t *Tool) allocRunID() string {
	if t.idGen != nil {
		if id := t.idGen(); id != "" {
			return id
		}
	}
	return defaultBashRunID()
}

// cedarGate evaluates the Cedar policy for argv and returns (true, nil)
// when the command may proceed, or (false, result) when it should be
// blocked — result carries the JSON-marshalled callResult for the
// model. The gate implements the WP03 flow:
//
//  1. Derive pattern + dangerous tier.
//  2. Build Cedar context (pattern + argv_count + dangerous_tier +
//     working_dir) — NO raw argv, per the audit-redaction lint.
//  3. Evaluate: Allow → proceed; Deny → typed error result.
//  4. NotApplicable → invoke PromptRegistry.RequestInteractive.
//     Resolution: AllowOnce → proceed; AllowAlways non-dangerous →
//     write .cedar snippet + engine.Reload + proceed; AllowAlways
//     dangerous + no override → demote to AllowOnce + proceed; Deny →
//     block.
//
// If PromptRegistry is nil and outcome is NotApplicable, the command
// is allowed (default-allow stance for unbooted chassis paths).
func (t *Tool) cedarGate(ctx context.Context, argv []string, workingDir string) (allow bool, result json.RawMessage) {
	pattern := DerivePattern(argv)
	isDangerous, _ := IsDangerous(argv)

	// Build Cedar context. MUST NOT include raw argv — redaction lint
	// rejects argv keys in audit pipeline (constraint §3).
	ctxAttrs := map[cedargo.String]cedargo.Value{
		cedargo.String(cedar.CtxKeyPattern):       cedargo.String(pattern),
		cedargo.String(cedar.CtxKeyWorkingDir):    cedargo.String(workingDir),
		cedargo.String(cedar.CtxKeyDangerousTier): cedargo.Boolean(isDangerous),
		cedargo.String("argv_count"):              cedargo.Long(int64(len(argv))),
	}

	resource := cedar.BashCommandUID(pattern)
	dec := t.cedarEngine.Evaluate(ctx, cedar.UserUID(), cedar.ActionRunBashCommand, resource, ctxAttrs)

	switch dec.Outcome {
	case cedar.Allow:
		t.logf("bash.gate.allow", "pattern", pattern, "policy", dec.MatchedPolicy)
		return true, nil

	case cedar.Deny:
		t.logf("bash.gate.deny", "pattern", pattern, "reason", dec.Reason)
		res, _ := marshalResult(callResult{
			Stderr:   "cedar policy denied: " + dec.Reason,
			ExitCode: -1,
		})
		return false, res

	default: // NotApplicable
		if t.promptRegistry == nil {
			// No registry — default-allow stance.
			t.logf("bash.gate.not_applicable.allow_unbooted", "pattern", pattern)
			return true, nil
		}
		surface := cedar.PromptSurface{
			Bash: &cedar.BashPromptSurface{
				Pattern:    pattern,
				Argv:       argv,
				WorkingDir: workingDir,
				Dangerous:  isDangerous,
			},
		}
		resolution, err := t.promptRegistry.RequestInteractive(ctx, surface)
		if err != nil {
			// Context cancelled or invalid surface — deny.
			t.logf("bash.gate.prompt_err", "err", err.Error())
			res, _ := marshalResult(callResult{
				Stderr:   "permission prompt error: " + err.Error(),
				ExitCode: -1,
			})
			return false, res
		}

		switch resolution.Decision {
		case cedar.DecisionDeny:
			t.logf("bash.gate.prompt_deny", "pattern", pattern, "reason", resolution.Reason)
			res, _ := marshalResult(callResult{
				Stderr:   "permission denied by user: " + resolution.Reason,
				ExitCode: -1,
			})
			return false, res

		case cedar.DecisionAllowOnce:
			t.logf("bash.gate.allow_once", "pattern", pattern)
			return true, nil

		case cedar.DecisionAllowAlways:
			if isDangerous && !t.dangerousOpsCacheAllowed() {
				// Demote to AllowOnce; emit audit annotation.
				// The entry is already resolved by RequestInteractive so
				// we cannot call Resolve again. Log the demotion scope
				// and proceed — the transient grant from AllowOnce would
				// require the user to have picked AllowOnce; since they
				// picked AllowAlways and we demote, the next invocation
				// re-prompts. This is intentional (§4.3 FR-015).
				t.logf("bash.gate.allow_always.dangerous_demoted",
					"pattern", pattern,
					"scope", "once_dangerous_demoted",
				)
				return true, nil
			}
			// Non-dangerous AllowAlways (or dangerous + override): write
			// a .cedar snippet so the next gate query resolves via Allow
			// without a prompt.
			t.writePolicySnippet(pattern)
			return true, nil

		default:
			// Unknown decision — allow conservatively.
			return true, nil
		}
	}
}

// writePolicySnippet writes a per-pattern Cedar policy file at
// <DataDir>/policy/bash_allow_<sanitized-pattern>.cedar. The file body
// is a single permit rule that allows the derived pattern as a
// BashCommand resource. After writing, best-effort engine.Reload is
// called so the new policy takes effect in the current process.
//
// When DataDir is empty or the write fails, the function logs a
// warning and returns — the in-flight command still runs (the user
// approved it) but future invocations re-prompt.
func (t *Tool) writePolicySnippet(pattern string) {
	if t.dataDir == "" {
		t.logf("bash.gate.snippet_skip", "reason", "no DataDir configured")
		return
	}
	sanitized := sanitizePatternForFilename(pattern)
	dir := filepath.Join(t.dataDir, cedar.PolicyDir)
	if err := mkdirAll(dir); err != nil {
		t.logf("bash.gate.snippet_mkdir_err", "err", err.Error())
		return
	}
	filename := filepath.Join(dir, "bash_allow_"+sanitized+".cedar")
	body := "permit(\n" +
		"  principal,\n" +
		"  action == Action::\"run_bash_command\",\n" +
		"  resource == BashCommand::\"" + pattern + "\"\n" +
		");\n"
	if err := writeFile(filename, []byte(body)); err != nil {
		t.logf("bash.gate.snippet_write_err", "file", filename, "err", err.Error())
		return
	}
	t.logf("bash.gate.snippet_written", "file", filename, "pattern", pattern)
	// Best-effort reload: failure is non-fatal; the in-process engine
	// already granted this run; future runs re-read the file on next
	// Reload.
	if err := t.cedarEngine.Reload(context.Background()); err != nil {
		t.logf("bash.gate.reload_warn", "err", err.Error())
	}
}

// sanitizePatternForFilename replaces characters unsafe in filenames
// with underscores. Spaces become underscores. The result is
// ASCII-clean so the policy directory is safe to list.
func sanitizePatternForFilename(pattern string) string {
	var sb strings.Builder
	for _, r := range pattern {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// mkdirAll is an os.MkdirAll wrapper used as a seam in tests.
var mkdirAll = func(path string) error {
	return os.MkdirAll(path, 0o755)
}

// writeFile is an os.WriteFile wrapper used as a seam in tests.
var writeFile = func(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

// resolveWorkingDir maps the caller-supplied working_dir to an
// absolute path. Empty input returns the harness's default
// agent-workspace root. Relative input is joined to that root.
// Absolute input is taken as-is — the bash tool runs as the user's
// account so any path the account can reach is fair game; the Cedar
// permission gate is the security boundary, not the cwd.
//
// In every case the final path is canonicalised via
// filepath.EvalSymlinks; if the path doesn't exist yet (e.g. the
// model wants to mkdir + ls) we fall back to filepath.Clean on the
// syntactic form.
func (t *Tool) resolveWorkingDir(workingDir string) (string, error) {
	root, err := canonicalize(t.sandboxRoot)
	if err != nil {
		// Default-root unavailable but the model passed an absolute
		// path — let the spawn happen anyway; the kernel will reject
		// if the dir genuinely doesn't exist. For the no-input case
		// fall back to "/" so the spawn doesn't crash on a nil cwd.
		root = "/"
	}
	var candidate string
	switch {
	case workingDir == "":
		return root, nil
	case filepath.IsAbs(workingDir):
		candidate = filepath.Clean(workingDir)
	default:
		candidate = filepath.Join(root, workingDir)
	}
	canonical, err := canonicalize(candidate)
	if err != nil {
		// Path doesn't exist (yet). Use the syntactic form.
		canonical = filepath.Clean(candidate)
	}
	return canonical, nil
}

// canonicalize resolves symlinks and returns an absolute, cleaned
// path. Returns the underlying error when EvalSymlinks fails (path
// missing, traversal failure, permissions). Callers may interpret
// "missing" as a soft error.
func canonicalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

// pathHasPrefix reports whether candidate equals root or sits under
// root with a separator boundary, so "/sandbox/foo" matches root
// "/sandbox" but "/sandboxoid/foo" does not.
func pathHasPrefix(candidate, root string) bool {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	if candidate == root {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(candidate, prefix)
}

func resolveTimeout(raw *int) time.Duration {
	secs := defaultTimeoutSeconds
	if raw != nil && *raw > 0 {
		secs = *raw
	}
	if secs > maxTimeoutSeconds {
		secs = maxTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

func marshalResult(r callResult) (json.RawMessage, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("bash: marshal result: %w", err)
	}
	return json.RawMessage(b), nil
}

func (t *Tool) logf(event string, kv ...any) {
	if t == nil || t.logger == nil {
		return
	}
	t.logger.Info(event, kv...)
}
