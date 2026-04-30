package cedarpolicy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
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
	engine  Engine
	dataDir string // may be empty if no DataDir is configured
}

// NewAPI constructs the view-scoped API. engine MAY be nil — in that
// case every method returns an empty result with no error so the
// frontend renders an empty panel rather than an exception screen
// during boot before the engine is wired.
func NewAPI(engine Engine) *API {
	return &API{engine: engine}
}

// NewAPIWithDataDir constructs the view-scoped API with a data directory for
// snippet write/revoke operations. engine MAY be nil.
func NewAPIWithDataDir(engine Engine, dataDir string) *API {
	return &API{engine: engine, dataDir: dataDir}
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
