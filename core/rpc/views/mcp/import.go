// Package mcp — RPC wrapper around recipes.ImportClaudeDesktop.
//
// ImportClaudeDesktopConfig is the Wails-bound surface the frontend
// "Paste config" tab calls. It accepts the raw clipboard payload and a
// `dryRun` flag:
//
//   - dryRun=true → translate only; surface the TranslationReport so
//     the modal can render per-entry ✓ / ✗ rows. NO disk writes.
//   - dryRun=false → translate, then for each entry the caller marked
//     `keep=true` (Status=kept or collision_warning) write the
//     translated YAML and the original-JSON snippet under
//     <DataDir>/mcp/recipes/_imports/<id>.{yaml,json}. Returns the same
//     report (with updated WrotePath fields).
//
// The collision check feeds off MergedCatalog.Get so the warning rows
// reflect the live catalog (shipped + registry + user). dryRun=true is
// a pure-read operation; dryRun=false is the only path that touches
// disk. UserStore.Reload (or the fsnotify watcher) picks up the new
// _imports/<id>.yaml on the next event tick, completing the round-trip.
package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// ImportRequest is the wire shape the frontend sends. RawJSON is the
// clipboard payload verbatim. KeepIDs is the list of entry ids the
// user explicitly marked "Save" — only those entries get persisted in
// the dryRun=false path. nil/empty KeepIDs in a dryRun=false call
// means "save every kept-or-warning entry" (the modal's "Save all"
// button).
type ImportRequest struct {
	RawJSON string   `json:"raw_json"`
	DryRun  bool     `json:"dry_run"`
	KeepIDs []string `json:"keep_ids,omitempty"`
}

// ImportResponse mirrors recipes.TranslationReport plus per-entry
// disk-write outcomes for non-dryRun calls. The frontend renders
// WrotePath in the success toast.
type ImportResponse struct {
	Report     recipes.TranslationReport `json:"report"`
	WrotePaths []ImportWrotePath         `json:"wrote_paths,omitempty"`
}

// ImportWrotePath is the post-write artefact pair for one entry.
type ImportWrotePath struct {
	ID       string `json:"id"`
	YAMLPath string `json:"yaml_path"`
	JSONPath string `json:"json_path"`
}

// MergedCatalogReader is the read seam ImportClaudeDesktopConfig uses
// for collision detection. *recipes.MergedCatalog satisfies this
// interface; tests pass a stub.
type MergedCatalogReader interface {
	Recipes() []recipes.Recipe
}

// DataDirProvider hands the absolute path to <DataDir>. The boot
// wiring in core/rpc/api.go returns c.DataDir().
type DataDirProvider func() string

// ImportConfig wires the import flow's collaborators. Both fields
// MUST be non-nil at construction; passing nil yields an API that
// returns ErrImportNotConfigured on every call (so the frontend gets
// a clean "feature disabled" error rather than a panic).
type ImportConfig struct {
	Catalog MergedCatalogReader
	DataDir DataDirProvider
}

// ImportAPI is the import-specific MCPAPI extension. NewImportAPI
// returns one wired against the supplied config.
type ImportAPI struct {
	cfg ImportConfig
}

// NewImportAPI constructs the import surface.
func NewImportAPI(cfg ImportConfig) *ImportAPI {
	return &ImportAPI{cfg: cfg}
}

// ImportClaudeDesktopConfig is the user-facing RPC. Returns
// ImportResponse on success; per-entry malformed/unsupported rows are
// reflected in the report — only wholesale-payload problems
// (oversize, no mcpServers key, JSON parse failure) come back as
// errors.
func (a *ImportAPI) ImportClaudeDesktopConfig(_ context.Context, req ImportRequest) (ImportResponse, error) {
	if a == nil {
		return ImportResponse{}, ErrImportNotConfigured
	}
	if a.cfg.Catalog == nil || a.cfg.DataDir == nil {
		return ImportResponse{}, ErrImportNotConfigured
	}
	dataDir := a.cfg.DataDir()
	if dataDir == "" {
		return ImportResponse{}, ErrImportNotConfigured
	}

	existing := buildExistingIDs(a.cfg.Catalog)

	report, err := recipes.ImportClaudeDesktop([]byte(req.RawJSON), existing)
	if err != nil {
		return ImportResponse{}, err
	}

	resp := ImportResponse{Report: report}
	if req.DryRun {
		return resp, nil
	}

	keepSet := buildKeepSet(req.KeepIDs)
	for i := range report.Entries {
		e := report.Entries[i]
		if !isKeepable(e) {
			continue
		}
		if keepSet != nil && !keepSet[e.ID] {
			continue
		}
		yamlPath, jsonPath, werr := recipes.WriteImportArtifacts(dataDir, e)
		if werr != nil {
			return ImportResponse{}, fmt.Errorf("import: write %q: %w", e.ID, werr)
		}
		resp.WrotePaths = append(resp.WrotePaths, ImportWrotePath{
			ID:       e.ID,
			YAMLPath: yamlPath,
			JSONPath: jsonPath,
		})
	}
	return resp, nil
}

// buildExistingIDs collects every recipe id from the merged catalog so
// the translator can flag collisions. Nil-tolerant: a nil catalog
// returns nil (== "no collisions known").
func buildExistingIDs(cat MergedCatalogReader) map[string]bool {
	if cat == nil {
		return nil
	}
	rs := cat.Recipes()
	if len(rs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(rs))
	for _, r := range rs {
		out[r.ID] = true
	}
	return out
}

// buildKeepSet returns nil when the caller passed no explicit
// keep-list (semantics: "save every kept-or-warning entry"); otherwise
// it returns the id-keyed set so the writer can per-entry filter.
func buildKeepSet(ids []string) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// isKeepable reports whether an entry is eligible for the disk-write
// path. Only kept and collision_warning entries are saveable;
// unsupported and malformed entries never reach the writer.
func isKeepable(e recipes.ImportEntry) bool {
	return e.Status == recipes.ImportStatusKept || e.Status == recipes.ImportStatusCollisionWarning
}

// ErrImportNotConfigured is returned by ImportClaudeDesktopConfig
// when the boot wiring left either Catalog or DataDir nil. Callers
// interpret this as "the import feature is disabled in this build /
// config".
var ErrImportNotConfigured = errors.New("mcp: import not configured (catalog or data-dir missing)")
