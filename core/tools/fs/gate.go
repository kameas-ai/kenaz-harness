package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Op identifies whether a filesystem gate call is a read or a write.
type Op string

const (
	OpRead  Op = "read"
	OpWrite Op = "write"
)

// PromptResponse is the outcome of a user-facing permission prompt.
type PromptResponse int

const (
	// PromptDeny means the user rejected the request.
	PromptDeny PromptResponse = iota
	// PromptAllowOnce means the user granted a single-use transient grant.
	PromptAllowOnce
	// PromptAllowExact means the user granted a persistent grant for this
	// exact canonical path.
	PromptAllowExact
	// PromptAllowDirectory means the user granted a persistent grant for
	// the parent directory and everything below it.
	PromptAllowDirectory
)

// PromptSurface carries the context the permission modal needs to
// render a meaningful explanation for the user.
type PromptSurface struct {
	// Op is "read" or "write".
	Op Op
	// CanonicalPath is the cleaned, absolute target path.
	CanonicalPath string
	// IsDangerous is true when the path is in the dangerous-tier list.
	IsDangerous bool
	// DangerCopy is the hazard-banner text when IsDangerous is true.
	DangerCopy string
	// IsInsideRecipeDir is true when the path falls inside a
	// recipe-declared allowed directory.
	IsInsideRecipeDir bool
}

// Prompter is the interface the gate calls to surface an interactive
// permission prompt. In production this is wired to the frontend RPC
// surface (WP02). Tests inject a stub.
//
// Dangerous-tier paths MUST NOT return PromptAllowExact or
// PromptAllowDirectory when the caller has not set AllowDangerousPersist
// in the GateOptions — the gate enforces this constraint regardless of
// what Prompt returns.
type Prompter interface {
	Prompt(ctx context.Context, s PromptSurface) (PromptResponse, error)
}

// NoOpPrompter is the default Prompter used when no frontend is
// connected. It always returns PromptDeny so unattended runs are safe.
type NoOpPrompter struct{}

// Prompt implements Prompter.
func (NoOpPrompter) Prompt(_ context.Context, _ PromptSurface) (PromptResponse, error) {
	return PromptDeny, nil
}

// GateOptions configures a Gate instance.
type GateOptions struct {
	// Engine is the cedar policy engine. Required.
	Engine cedar.Gate
	// PolicyDir is the directory where generated .cedar snippets are
	// written on AllowAlways decisions. When empty, snippet persistence
	// is disabled (transient grants still work).
	PolicyDir string
	// Prompter is the interactive permission surface. Defaults to
	// NoOpPrompter when nil.
	Prompter Prompter
	// AllowDangerousPersist enables "Allow always" for dangerous-tier
	// paths. Must be explicitly set true; the default is false so the
	// safe posture requires no configuration.
	AllowDangerousPersist bool
}

// Gate orchestrates the WP04 filesystem permission flow:
//
//  1. Canonicalize the path (rejects .. and control chars).
//  2. Check dangerous tier.
//  3. Evaluate the cedar engine with recipe_dir_match + dangerous_tier.
//  4. On Allow → return allow.
//     On Deny  → return deny.
//     On NotApplicable → invoke Prompter, handle response:
//     - PromptDeny       → deny
//     - PromptAllowOnce  → cache transient grant, return allow
//     - PromptAllowExact → write cedar snippet for this path, return allow
//     - PromptAllowDirectory → write cedar snippet for parent dir, return allow
//
// Gate is safe for concurrent use.
type Gate struct {
	opts GateOptions

	// transientMu protects transientGrants.
	transientMu     sync.RWMutex
	transientGrants map[transientKey]time.Time
}

// transientKey identifies one transient grant.
type transientKey struct {
	op   Op
	path string
}

// transientGrantTTL is how long a PromptAllowOnce grant persists in the
// session without re-prompting. Chosen to cover a typical multi-step
// tool sequence within one conversation turn.
const transientGrantTTL = 5 * time.Minute

// NewGate returns a ready-to-use Gate with the supplied options.
func NewGate(opts GateOptions) *Gate {
	if opts.Prompter == nil {
		opts.Prompter = NoOpPrompter{}
	}
	if opts.Engine == nil {
		opts.Engine = cedar.AllowAll{}
	}
	return &Gate{
		opts:            opts,
		transientGrants: make(map[transientKey]time.Time),
	}
}

// Evaluate runs the full WP04 gate flow and returns a cedar Decision.
// The Decision's Outcome is always one of Allow or Deny when Evaluate
// returns nil error; ErrInvalidPath (from Canonicalize) is surfaced as
// an error so callers can distinguish "bad path" from "denied by policy".
//
// On PromptAllowExact or PromptAllowDirectory the gate writes a cedar
// snippet to PolicyDir (if configured); the write is best-effort and
// does not fail the Allow decision.
func (g *Gate) Evaluate(ctx context.Context, op Op, rawPath string) (cedar.Decision, error) {
	canonical, err := Canonicalize(rawPath)
	if err != nil {
		return cedar.Decision{}, err
	}

	// Dangerous-tier classification.
	dangerous, dangerCopy := IsDangerousPath(canonical)

	// Build cedar context attributes.
	recipeDirMatch := IsInsideRecipeDir(canonical)
	attrs := map[cedarlib.String]cedarlib.Value{
		cedarlib.String(cedar.CtxKeyCanonicalPath):  cedarlib.String(canonical),
		cedarlib.String(cedar.CtxKeyRecipeDirMatch): cedarlib.Boolean(recipeDirMatch),
		cedarlib.String(cedar.CtxKeyDangerousTier):  cedarlib.Boolean(dangerous),
	}

	action := cedar.ActionReadFilesystem
	if op == OpWrite {
		action = cedar.ActionWriteFilesystem
	}

	resource := cedar.FilesystemOpUID(canonical)
	d := g.opts.Engine.Evaluate(ctx, cedar.UserUID(), action, resource, attrs)

	switch d.Outcome {
	case cedar.Allow:
		return d, nil
	case cedar.Deny:
		return d, nil
	case cedar.NotApplicable:
		// Check transient cache first.
		if g.hasTransientGrant(op, canonical) {
			d.Outcome = cedar.Allow
			d.Reason = "transient grant (allow-once)"
			return d, nil
		}
		// Invoke the interactive prompt.
		surface := PromptSurface{
			Op:                op,
			CanonicalPath:     canonical,
			IsDangerous:       dangerous,
			DangerCopy:        dangerCopy,
			IsInsideRecipeDir: recipeDirMatch,
		}
		resp, perr := g.opts.Prompter.Prompt(ctx, surface)
		if perr != nil {
			// Prompt error → deny (fail-safe).
			d.Outcome = cedar.Deny
			d.Reason = "prompt error: " + perr.Error()
			return d, nil
		}

		switch resp {
		case PromptDeny:
			d.Outcome = cedar.Deny
			d.Reason = "user denied in prompt"
			return d, nil

		case PromptAllowOnce:
			g.addTransientGrant(op, canonical)
			d.Outcome = cedar.Allow
			d.Reason = "user granted allow-once (transient)"
			return d, nil

		case PromptAllowExact:
			if dangerous && !g.opts.AllowDangerousPersist {
				// Dangerous tier: refuse persistent grant.
				d.Outcome = cedar.Deny
				d.Reason = "dangerous path: allow-always requires AllowDangerousPersist override"
				return d, nil
			}
			body := buildExactSnippet(op, canonical)
			_ = g.writePolicySnippet(snippetName(op, canonical, "path"), body)
			d.Outcome = cedar.Allow
			d.Reason = "user granted allow-always (exact path)"
			return d, nil

		case PromptAllowDirectory:
			if dangerous && !g.opts.AllowDangerousPersist {
				d.Outcome = cedar.Deny
				d.Reason = "dangerous path: allow-always requires AllowDangerousPersist override"
				return d, nil
			}
			dir := filepath.Dir(canonical)
			body := buildDirectorySnippet(op, dir)
			_ = g.writePolicySnippet(snippetName(op, dir, "dir"), body)
			d.Outcome = cedar.Allow
			d.Reason = "user granted allow-always (directory)"
			return d, nil
		}
	}
	return d, nil
}

// hasTransientGrant reports whether a non-expired transient grant
// exists for (op, canonical).
func (g *Gate) hasTransientGrant(op Op, canonical string) bool {
	g.transientMu.RLock()
	exp, ok := g.transientGrants[transientKey{op, canonical}]
	g.transientMu.RUnlock()
	return ok && time.Now().Before(exp)
}

// addTransientGrant stores a time-limited transient grant for
// (op, canonical).
func (g *Gate) addTransientGrant(op Op, canonical string) {
	g.transientMu.Lock()
	g.transientGrants[transientKey{op, canonical}] = time.Now().Add(transientGrantTTL)
	g.transientMu.Unlock()
}

// buildExactSnippet returns the Cedar policy body for an
// "allow this exact path" persistent grant.
//
//	permit (
//	    principal == User::"local",
//	    action == Action::"<action>",
//	    resource is FilesystemOp
//	) when { resource.path == "<canonical>" };
//
// Note: FilesystemOp resources don't have a `.path` attribute in Cedar
// by default, so we match on the entity id via context.canonical_path
// which is always populated by the engine's populateFamilyContext.
func buildExactSnippet(op Op, canonical string) string {
	action := "read_filesystem"
	if op == OpWrite {
		action = "write_filesystem"
	}
	escaped := strings.ReplaceAll(canonical, `"`, `\"`)
	return fmt.Sprintf(
		"permit (\n"+
			"    principal == User::\"local\",\n"+
			"    action == Action::\"%s\",\n"+
			"    resource is FilesystemOp\n"+
			") when {\n"+
			"    context.canonical_path == \"%s\"\n"+
			"};\n",
		action, escaped,
	)
}

// buildDirectorySnippet returns the Cedar policy body for an
// "allow this directory and everything below" persistent grant.
//
//	permit (...) when { context.canonical_path like "<dir>/*" };
func buildDirectorySnippet(op Op, dir string) string {
	action := "read_filesystem"
	if op == OpWrite {
		action = "write_filesystem"
	}
	escaped := strings.ReplaceAll(dir, `"`, `\"`)
	// Ensure the dir pattern ends with / before the wildcard.
	prefix := strings.TrimSuffix(escaped, "/") + "/"
	return fmt.Sprintf(
		"permit (\n"+
			"    principal == User::\"local\",\n"+
			"    action == Action::\"%s\",\n"+
			"    resource is FilesystemOp\n"+
			") when {\n"+
			"    context.canonical_path like \"%s*\"\n"+
			"};\n",
		action, prefix,
	)
}

// sanitizedPathSegment replaces all non-alphanumeric, non-hyphen,
// non-underscore chars with underscores so the result is safe for a
// filename. Leading/trailing underscores are stripped.
var nonFileChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizePathForFilename(p string) string {
	s := nonFileChars.ReplaceAllString(p, "_")
	return strings.Trim(s, "_")
}

// snippetName returns a deterministic filename for a cedar snippet.
// Format: fs_allow_<op>_<sanitized>_<scope>.cedar
func snippetName(op Op, path, scope string) string {
	return fmt.Sprintf("fs_allow_%s_%s_%s.cedar", op, sanitizePathForFilename(path), scope)
}

// writePolicySnippet writes body to PolicyDir/<name>. Best-effort:
// errors are returned to the caller (which logs or ignores them) but
// never block the Allow decision.
func (g *Gate) writePolicySnippet(name, body string) error {
	if g.opts.PolicyDir == "" {
		return nil
	}
	if err := os.MkdirAll(g.opts.PolicyDir, 0o700); err != nil {
		return fmt.Errorf("fs/gate: mkdir PolicyDir: %w", err)
	}
	dst := filepath.Join(g.opts.PolicyDir, name)
	return os.WriteFile(dst, []byte(body), 0o600)
}
