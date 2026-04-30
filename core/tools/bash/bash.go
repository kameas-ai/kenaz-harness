package bash

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cedargo "github.com/cedar-policy/cedar-go"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
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
// "kaneaz__" prefix is reserved for built-in (in-binary) tools so the
// dispatcher can route by prefix without a registry lookup.
const Name = "kaneaz__bash"

// description is the user-facing tool description sent to the model.
// It mirrors FR-010's text and the assistant relies on the truncated
// flag + policy gate to know when to retry with a narrower command.
const description = "Execute a shell command in a sandboxed working directory. " +
	"Returns stdout, stderr, exit code, and a truncated flag. Cedar policy gate applies."

// inputSchema is the JSON Schema describing kaneaz__bash's argument
// shape (FR-010). Inlined as a constant so InputSchema() can return
// the same json.RawMessage every call without re-serialising.
const inputSchema = `{
  "type": "object",
  "properties": {
    "command": {"type": "string"},
    "working_dir": {"type": "string", "description": "Optional subdirectory of the workspace"},
    "timeout_seconds": {"type": "integer", "default": 30, "maximum": 300}
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
// PermissionCacheDangerousOps, when true, allows AllowAlways policy
// files to be persisted even for commands classified as dangerous-tier.
// Default false: AllowAlways on dangerous commands is demoted to
// AllowOnce with an audit annotation.
type Options struct {
	SandboxRoot                string
	Allowlist                  []string
	Logger                     *slog.Logger
	Store                      *Store
	IDGen                      func() string
	CedarEngine                *cedar.Engine
	PromptRegistry             *cedar.Registry
	DataDir                    string
	PermissionCacheDangerousOps bool
}

// Tool implements the kaneaz__bash built-in tool. It is safe for
// concurrent use; all state is read-only after construction and the
// per-call work happens in stack-local Run/Parse calls.
type Tool struct {
	sandboxRoot                string
	allowlist                  []string
	logger                     *slog.Logger
	store                      *Store
	idGen                      func() string
	cedarEngine                *cedar.Engine
	promptRegistry             *cedar.Registry
	dataDir                    string
	permissionCacheDangerousOps bool
}

// New constructs a Tool with the given options. SandboxRoot must be
// non-empty and absolute; an empty value is a programming error.
func New(opts Options) *Tool {
	allow := opts.Allowlist
	if allow == nil {
		allow = DefaultAllowlist
	}
	return &Tool{
		sandboxRoot:                opts.SandboxRoot,
		allowlist:                  allow,
		logger:                     opts.Logger,
		store:                      opts.Store,
		idGen:                      opts.IDGen,
		cedarEngine:                opts.CedarEngine,
		promptRegistry:             opts.PromptRegistry,
		dataDir:                    opts.DataDir,
		permissionCacheDangerousOps: opts.PermissionCacheDangerousOps,
	}
}

// Name returns the namespaced tool identifier (always "kaneaz__bash").
func (t *Tool) Name() string { return Name }

// Description returns the user-facing tool description.
func (t *Tool) Description() string { return description }

// InputSchema returns the JSON Schema for the tool's arguments.
// The returned bytes are owned by the caller; mutating them would
// corrupt subsequent calls.
func (t *Tool) InputSchema() json.RawMessage {
	return json.RawMessage(inputSchema)
}

// callArgs mirrors the FR-010 input_schema. timeout_seconds is a
// pointer so we can distinguish "omitted" (use default 30) from
// "explicitly zero" (treated the same as omitted; FR-010 says default
// 30, max 300, but a zero or negative value is equally meaningless).
type callArgs struct {
	Command        string `json:"command"`
	WorkingDir     string `json:"working_dir,omitempty"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
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

// Call dispatches a single tool invocation. Errors that originate
// from invalid input (parse failure, missing command field) are
// returned as Go errors; errors that originate from the sandbox /
// policy gate / exec layer are surfaced as a successful tool result
// with an explanatory stderr and a non-zero exit_code so the model
// learns what went wrong without the toolloop short-circuiting.
//
// Flow:
//  1. Unmarshal args. Empty command → error.
//  2. Parse command into argv. Parse error → error.
//  3. Cedar gate check on argv BEFORE LookPath (NFR-005). Falls back
//     to allowlist check when no Cedar engine is wired.
//  4. Resolve working_dir under sandboxRoot (FR-013, NFR-004).
//  5. exec.LookPath the program; "command not found" → exit 127.
//  6. Run with the bounded context + output cap.
//  7. Marshal RunResult → callResult JSON.
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

	argv, err := Parse(args.Command)
	if err != nil {
		return nil, fmt.Errorf("bash: parse command: %w", err)
	}
	if len(argv) == 0 {
		return nil, errors.New("bash: command parsed to empty argv")
	}

	prog := argv[0]
	progBase := filepath.Base(prog)

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

	resolved, err := exec.LookPath(prog)
	if err != nil {
		t.logf("bash.not_found", "program", progBase)
		return marshalResult(callResult{
			Stderr:    "command not found: " + progBase,
			ExitCode:  127,
			Truncated: false,
		})
	}
	argv[0] = resolved

	t.logf("bash.invoke",
		"program", progBase,
		"argv_len", len(argv),
		"cwd", cwd,
		"timeout_seconds", int(timeout/time.Second),
	)

	res, runErr := Run(ctx, RunOpts{
		Argv:           argv,
		Cwd:            cwd,
		Timeout:        timeout,
		MaxOutputBytes: DefaultMaxOutputBytes,
	})
	exitCode := res.ExitCode
	stderr := string(res.Stderr)
	if runErr != nil {
		// Run already populated the partial buffers and exit -1.
		// Append the error reason so the model can read it.
		if stderr != "" && !strings.HasSuffix(stderr, "\n") {
			stderr += "\n"
		}
		stderr += runErr.Error()
		if exitCode == 0 {
			exitCode = -1
		}
		t.logf("bash.run_error", "err", runErr.Error())
	}
	stdout := string(res.Stdout)
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
			if isDangerous && !t.permissionCacheDangerousOps {
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
// absolute path that lies under sandboxRoot. Empty input returns the
// sandbox root verbatim. Relative input is joined to sandboxRoot.
// Absolute input is taken as-is. In every case the final path is
// canonicalised via filepath.EvalSymlinks BEFORE the prefix check so
// a symlink pointing at /tmp cannot grant access to /tmp.
//
// EvalSymlinks requires the path to exist; non-existent dirs that
// resolve syntactically under the sandbox are accepted on the
// assumption that the user wants to run a command there (e.g. mkdir
// followed by ls). For non-existent paths we fall back to a Clean +
// prefix check on the syntactic form — symlink escape is not
// possible against a path that doesn't exist yet.
func (t *Tool) resolveWorkingDir(workingDir string) (string, error) {
	root, err := canonicalize(t.sandboxRoot)
	if err != nil {
		return "", fmt.Errorf("sandbox root unavailable: %w", err)
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
		// Path doesn't exist (yet). Fall back to syntactic check.
		canonical = filepath.Clean(candidate)
	}
	if !pathHasPrefix(canonical, root) {
		return "", errors.New("working_dir outside sandbox")
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
