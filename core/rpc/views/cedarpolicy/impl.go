package cedarpolicy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// filenameRe is the compiled safety regex for Cedar snippet filenames.
// Accepted pattern: lowercase letter, then up to 127 lowercase letters /
// digits / underscores, then ".cedar".  Total max length = 1 + 127 + 6 = 134
// characters. The regex rejects uppercase, slashes, dots in the stem, spaces,
// control characters, and the leading ".." path-traversal prefix.
var filenameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}\.cedar$`)

// validateSnippetFilename returns nil when name is safe to use as a
// Cedar policy snippet filename. It is the security-critical gate for
// WritePolicySnippet and RevokePolicySnippet.
func validateSnippetFilename(name string) error {
	if name == "" {
		return errors.New("cedarpolicy: snippet name must not be empty")
	}
	if !filenameRe.MatchString(name) {
		return fmt.Errorf("cedarpolicy: snippet name %q does not match required pattern ^[a-z][a-z0-9_]{0,127}\\.cedar$", name)
	}
	// Belt-and-suspenders: filepath.Base must equal name so even if the regex
	// somehow admitted a slash the join would be safe.
	if filepath.Base(name) != name {
		return fmt.Errorf("cedarpolicy: snippet name %q contains path separators", name)
	}
	return nil
}

// Engine is the small subset of *cedar.Engine the API needs. Defined
// as an interface so tests can drive the API without constructing a
// real Cedar engine.
type Engine interface {
	ListPolicies() []cedar.PolicyFile
	Reload(ctx context.Context) error
	RecentDecisions(limit int) []cedar.Decision
}

// Compile-time witness: *cedar.Engine satisfies Engine.
var _ Engine = (*cedar.Engine)(nil)

// snippetFilenameRE is the validation pattern for WritePolicySnippet
// filenames. Matches cedar WP09's regex requirement.
var snippetFilenameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}\.cedar$`)

// API is the concrete CedarPolicyAPI implementation.
type API struct {
	engine        Engine
	dataDir       string       // may be empty if no DataDir is configured
	emitter       audit.Emitter // may be nil — emission is optional
	editorEnabled bool          // HARNESS_POLICY_EDITOR_UI flag
}

// NewAPI constructs the view-scoped API. engine MAY be nil — in that
// case every method returns an empty result with no error so the
// frontend renders an empty panel rather than an exception screen
// during boot before the engine is wired.
func NewAPI(engine Engine) *API {
	return &API{engine: engine, editorEnabled: true}
}

// NewAPIWithDataDir constructs the view-scoped API with a data directory for
// snippet write/revoke operations. engine MAY be nil.
func NewAPIWithDataDir(engine Engine, dataDir string) *API {
	return &API{engine: engine, dataDir: dataDir, editorEnabled: true}
}

// NewAPIWithOptions constructs the view-scoped API with full options.
// engine and emitter may be nil. editorEnabled controls whether write-path
// methods (SavePolicy, DeletePolicy, InstallTemplate) are available.
func NewAPIWithOptions(engine Engine, dataDir string, emitter audit.Emitter, editorEnabled bool) *API {
	return &API{
		engine:        engine,
		dataDir:       dataDir,
		emitter:       emitter,
		editorEnabled: editorEnabled,
	}
}

// policyDir returns the resolved <DataDir>/policy/ directory path, or an
// error when dataDir is not configured.
func (a *API) policyDir() (string, error) {
	if a == nil || a.dataDir == "" {
		return "", errors.New("cedarpolicy: dataDir not configured; cannot write/revoke snippet")
	}
	return filepath.Join(a.dataDir, "policy"), nil
}

// ListPolicies implements CedarPolicyAPI.
func (a *API) ListPolicies(_ context.Context) ([]PolicyFile, error) {
	if a == nil || a.engine == nil {
		return []PolicyFile{}, nil
	}
	return a.engine.ListPolicies(), nil
}

// ReloadPolicies implements CedarPolicyAPI.
func (a *API) ReloadPolicies(ctx context.Context) error {
	if a == nil || a.engine == nil {
		return errors.New("cedarpolicy: engine not wired")
	}
	return a.engine.Reload(ctx)
}

// RecentDecisions implements CedarPolicyAPI.
func (a *API) RecentDecisions(_ context.Context, limit int) ([]Decision, error) {
	if a == nil || a.engine == nil {
		return []Decision{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return a.engine.RecentDecisions(limit), nil
}

// WritePolicySnippet implements CedarPolicyAPI.
//
// Safety contract:
//  1. Validate filename BEFORE any I/O — never create then revoke.
//  2. Write to a <name>.tmp sibling, then os.Rename so a crash never
//     leaves a partial file in the live policy directory.
//  3. Engine.Reload is best-effort — failure is logged as a warning;
//     the write is already durable when we reach the reload.
func (a *API) WritePolicySnippet(ctx context.Context, name string, body string) error {
	if err := validateSnippetFilename(name); err != nil {
		return err
	}
	dir, err := a.policyDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cedarpolicy: mkdir %q: %w", dir, err)
	}
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("cedarpolicy: write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("cedarpolicy: rename %q → %q: %w", tmp, dst, err)
	}
	a.reloadBestEffort(ctx, "WritePolicySnippet", name)
	return nil
}

// RevokePolicySnippet implements CedarPolicyAPI.
func (a *API) RevokePolicySnippet(ctx context.Context, name string) error {
	if err := validateSnippetFilename(name); err != nil {
		return err
	}
	dir, err := a.policyDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, name)
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cedarpolicy: remove %q: %w", dst, err)
	}
	a.reloadBestEffort(ctx, "RevokePolicySnippet", name)
	return nil
}

// reloadBestEffort triggers Engine.Reload when an engine is wired.
// Failure is logged as a warning and does not propagate — the file
// I/O already succeeded; the engine will pick up the change on the
// next process start or manual reload.
func (a *API) reloadBestEffort(ctx context.Context, caller, name string) {
	if a == nil || a.engine == nil {
		return
	}
	if err := a.engine.Reload(ctx); err != nil {
		slog.Warn("cedarpolicy: engine reload failed (snippet is written; will apply on next reload)",
			"caller", caller,
			"snippet", name,
			"err", err,
		)
	}
}

// ── Editor methods (cedar-policy-editor-ui-01KQ8TD6 WP01) ────────────────

// GetPolicy reads the source of a single policy file.
// For protected embedded defaults it reads from cedar.PoliciesFS.
// For on-disk files it reads from <DataDir>/policy/<name>.
func (a *API) GetPolicy(_ context.Context, name string) (PolicyFileDetail, error) {
	if err := cedar.ValidPolicyName(name); err != nil {
		return PolicyFileDetail{}, err
	}
	// Embedded default — read from the packaged FS.
	if cedar.IsProtectedPolicyName(name) {
		data, err := cedar.PoliciesFS.ReadFile("policies/" + name)
		if err != nil {
			return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: read embedded policy %q: %w", name, err)
		}
		return PolicyFileDetail{
			PolicyFile: cedar.PolicyFile{
				Name:     name,
				Bytes:    len(data),
				Embedded: true,
				ParseOK:  true,
			},
			Source:   string(data),
			ReadOnly: true,
		}, nil
	}
	// On-disk user policy.
	dir, err := a.policyDir()
	if err != nil {
		return PolicyFileDetail{}, err
	}
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: read policy %q: %w", name, err)
	}
	return PolicyFileDetail{
		PolicyFile: cedar.PolicyFile{
			Name:    name,
			Path:    path,
			Bytes:   len(data),
			ParseOK: true, // We'll let the engine report actual parse status.
		},
		Source:   string(data),
		ReadOnly: false,
	}, nil
}

// SavePolicy validates source, and if parse succeeds, writes it atomically
// to <DataDir>/policy/<name>. Returns ParseResult with parse diagnostics.
// Never creates or modifies a file when parse fails.
func (a *API) SavePolicy(ctx context.Context, name string, source string) (ParseResult, error) {
	if !a.editorEnabled {
		return ParseResult{}, ErrPolicyEditorDisabled
	}
	if err := cedar.ValidPolicyName(name); err != nil {
		return ParseResult{}, err
	}
	if cedar.IsProtectedPolicyName(name) {
		return ParseResult{}, fmt.Errorf("cedarpolicy: %q is a protected embedded policy and cannot be overwritten", name)
	}
	dir, err := a.policyDir()
	if err != nil {
		return ParseResult{}, err
	}
	// Parse-validate BEFORE any I/O — never write then rollback.
	result := parseSource(name, source)
	if !result.OK {
		return result, nil
	}
	// Atomic write: temp file + rename so a crash never leaves a half-file.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ParseResult{}, fmt.Errorf("cedarpolicy: mkdir %q: %w", dir, err)
	}
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(source), 0o600); err != nil {
		return ParseResult{}, fmt.Errorf("cedarpolicy: write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return ParseResult{}, fmt.Errorf("cedarpolicy: rename %q → %q: %w", tmp, dst, err)
	}
	a.reloadBestEffort(ctx, "SavePolicy", name)
	_ = audit.Emit(ctx, a.emitter, audit.KindPolicyFileSaved, audit.PolicyFileSavedPayload{
		Name:    name,
		Bytes:   len(source),
		ParseOK: true,
	}, time.Now())
	return result, nil
}

// DeletePolicy removes <DataDir>/policy/<name>.
// Returns an error if name is a protected embedded default.
func (a *API) DeletePolicy(ctx context.Context, name string) error {
	if !a.editorEnabled {
		return ErrPolicyEditorDisabled
	}
	if err := cedar.ValidPolicyName(name); err != nil {
		return err
	}
	if cedar.IsProtectedPolicyName(name) {
		return fmt.Errorf("cedarpolicy: %q is a protected embedded policy and cannot be deleted", name)
	}
	dir, err := a.policyDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, name)
	if err := os.Remove(dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cedarpolicy: policy %q does not exist on disk", name)
		}
		return fmt.Errorf("cedarpolicy: remove %q: %w", dst, err)
	}
	a.reloadBestEffort(ctx, "DeletePolicy", name)
	_ = audit.Emit(ctx, a.emitter, audit.KindPolicyFileDeleted, audit.PolicyFileDeletedPayload{
		Name: name,
	}, time.Now())
	return nil
}

// ValidatePolicy parses source and returns diagnostics. Never touches disk.
// Used for the editor's debounced live-validation indicator.
func (a *API) ValidatePolicy(_ context.Context, source string) (ParseResult, error) {
	return parseSource("validate", source), nil
}

// InstallTemplate copies the shipped template <templateName> from
// cedar.PoliciesFS to <DataDir>/policy/<destName>.
// Returns ErrPolicyExists if destName already exists on disk.
func (a *API) InstallTemplate(ctx context.Context, templateName string, destName string) (PolicyFileDetail, error) {
	if !a.editorEnabled {
		return PolicyFileDetail{}, ErrPolicyEditorDisabled
	}
	if err := cedar.ValidPolicyName(templateName); err != nil {
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: invalid template name: %w", err)
	}
	if err := cedar.ValidPolicyName(destName); err != nil {
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: invalid destination name: %w", err)
	}
	if cedar.IsProtectedPolicyName(destName) {
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: %q is a protected name and cannot be used as a destination", destName)
	}
	// Read the template from the embedded FS.
	data, err := cedar.PoliciesFS.ReadFile("policies/" + templateName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: template %q not found in embedded FS", templateName)
		}
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: read template %q: %w", templateName, err)
	}
	dir, errDir := a.policyDir()
	if errDir != nil {
		return PolicyFileDetail{}, errDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: mkdir %q: %w", dir, err)
	}
	dst := filepath.Join(dir, destName)
	// Refuse if dest already exists.
	if _, err := os.Stat(dst); err == nil {
		return PolicyFileDetail{}, ErrPolicyExists
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: write template tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return PolicyFileDetail{}, fmt.Errorf("cedarpolicy: rename %q → %q: %w", tmp, dst, err)
	}
	a.reloadBestEffort(ctx, "InstallTemplate", destName)
	_ = audit.Emit(ctx, a.emitter, audit.KindPolicyTemplateInstalled, audit.PolicyTemplateInstalledPayload{
		Template: templateName,
		Dest:     destName,
		Bytes:    len(data),
	}, time.Now())
	return PolicyFileDetail{
		PolicyFile: cedar.PolicyFile{
			Name:    destName,
			Path:    dst,
			Bytes:   len(data),
			ParseOK: true,
		},
		Source:   string(data),
		ReadOnly: false,
	}, nil
}

// ListPlanModeActions returns the canonical Cedar action strings denied
// during plan_mode. Used by the Cedar editor UI's plan-mode reference
// panel so policy authors can see which actions the posture gate blocks.
// (plan-mode-posture-01KZNP3F WP08)
func (a *API) ListPlanModeActions(_ context.Context) ([]string, error) {
	out := make([]string, len(cedar.PlanModeDeniedActions))
	copy(out, cedar.PlanModeDeniedActions)
	return out, nil
}

// parseSource attempts to parse Cedar source using cedar-go.
// Returns ParseResult with OK=true on success, or OK=false with
// best-effort line/column diagnostics on failure.
func parseSource(name, source string) ParseResult {
	_, err := cedarlib.NewPolicySetFromBytes(name, []byte(source))
	if err == nil {
		return ParseResult{OK: true}
	}
	// cedar-go error messages are opaque; parse them best-effort.
	pe := extractParseError(err.Error())
	return ParseResult{OK: false, Errors: []ParseError{pe}}
}

// extractParseError attempts to extract line/column from a cedar-go
// error string. Cedar-go formats parse errors as:
//
//	"<name>:1:5: unexpected token..."  (line:col style)
//
// or similar. We scan for the first ":<digits>:<digits>:" prefix.
// If extraction fails, Line/Column are 0 ("unknown").
func extractParseError(msg string) ParseError {
	// Try to find a :<line>:<col>: pattern.
	parts := strings.SplitN(msg, ":", 4)
	if len(parts) >= 3 {
		line, errL := strconv.Atoi(strings.TrimSpace(parts[1]))
		col, errC := strconv.Atoi(strings.TrimSpace(parts[2]))
		if errL == nil && errC == nil && line > 0 {
			tail := ""
			if len(parts) >= 4 {
				tail = strings.TrimSpace(parts[3])
			}
			return ParseError{Line: line, Column: col, Message: tail}
		}
	}
	return ParseError{Line: 0, Column: 0, Message: msg}
}
