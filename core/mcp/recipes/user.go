// Package recipes — user-recipe loader.
//
// UserStore walks <DataDir>/mcp/recipes/*.yaml (source = "user") and
// <DataDir>/mcp/recipes/_imports/*.yaml (source = "imported"), parses
// each file with `gopkg.in/yaml.v3`, runs Recipe.Validate, and exposes
// the resulting slice through a copy-on-write Recipes() accessor.
//
// Freshness: there is no background watcher. mcp-connector-lifecycle-
// 01PMMC01 WP04 deleted the fsnotify-driven StartWatch/watchLoop/Close
// path after WP03 made every production consumer of this store
// (core/rpc/api.go's mcpUserRecipeSource, core/mcp/connectors's
// CatalogWithUserRecipes) call Load() fresh on every catalog
// build — so an externally-written file is already visible on the very
// next Tools_ListRecipes / import-collision check / recipe-bootstrap
// call, with no cache for a watcher to invalidate. See
// docs/unwired-ledger.md's 2026-08-18 entry for the full justification
// and core/rpc/views/mcp/import.go's docstring for what "picked up"
// means in each mode today.
//
// Boot ergonomics: a missing <DataDir>/mcp/recipes/ directory is NOT
// an error — it returns an empty catalog so fresh installs Just Work.
// A single malformed yaml file is logged + skipped; sibling files
// still load. This keeps the boot path resilient.
//
// Spec mapping: WP05 of mission mcp-server-install-01KQ8TDP. See
// FR-001 (loader), FR-002 (yaml shape mirrors shipped.json).
package recipes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// userRecipesSubdir is the path under DataDir that the loader walks.
// User-authored recipes live at the top level; clipboard imports land
// in _imports/.
const (
	userRecipesSubdir = "mcp/recipes"
	importsSubdir     = "_imports"
)

// UserStore loads user-authored YAML recipes from
// <DataDir>/mcp/recipes/. It is safe for concurrent reads via the
// Recipes() accessor.
//
// Construction: callers supply DataDir and an optional Logger
// (slog.Default() is used when nil).
type UserStore struct {
	// DataDir is the harness data directory. The loader resolves
	// <DataDir>/mcp/recipes/ at every Load/Reload — DataDir is treated
	// as immutable post-construction.
	DataDir string
	// Logger receives parse-skip diagnostics. nil → slog.Default().
	Logger *slog.Logger

	// snapshot points at the most recent successful load. Reads come
	// through the atomic pointer so a concurrent Reload (from Save or
	// Delete) can swap snapshots without blocking readers. Stored value
	// is *[]Recipe (a slice header snapshot — never aliased to the live
	// slice).
	snapshot atomic.Pointer[[]Recipe]
}

// NewUserStore returns a fresh UserStore rooted at dataDir. It does
// not perform I/O; call Load() to populate the snapshot.
//
// dataDir must be non-empty. The store tolerates a missing
// <dataDir>/mcp/recipes/ directory (Load → empty slice, nil error).
func NewUserStore(dataDir string, logger *slog.Logger) *UserStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &UserStore{DataDir: dataDir, Logger: logger}
}

// Recipes returns the most recent successfully-loaded snapshot. The
// returned slice is a fresh copy: callers may sort or mutate it
// without affecting subsequent loads or other readers
// (copy-on-write).
//
// If Load has never been called, Recipes returns an empty slice (not
// nil); this matches the boot-ergonomics contract — fresh installs
// see no user recipes but no errors either.
func (s *UserStore) Recipes() []Recipe {
	if s == nil {
		return nil
	}
	snap := s.snapshot.Load()
	if snap == nil {
		return []Recipe{}
	}
	out := make([]Recipe, len(*snap))
	copy(out, *snap)
	return out
}

// Load walks <DataDir>/mcp/recipes/ and <DataDir>/mcp/recipes/_imports/
// once, parses + validates every yaml file, and replaces the internal
// snapshot.
//
// Returns the freshly-loaded slice (a copy — same copy-on-write
// guarantee as Recipes). A missing <DataDir>/mcp/recipes/ directory is
// not an error: Load returns ([]Recipe{}, nil) and clears the
// snapshot. A single malformed yaml is logged + skipped; sibling
// files still load.
//
// An empty DataDir is a programming error and returns a non-nil
// error. Anything else surfaces as a logged-skip per file.
func (s *UserStore) Load() ([]Recipe, error) {
	if s == nil {
		return nil, errors.New("recipes: UserStore.Load: nil receiver")
	}
	if s.DataDir == "" {
		return nil, errors.New("recipes: UserStore.Load: DataDir is empty")
	}

	root := filepath.Join(s.DataDir, userRecipesSubdir)
	imports := filepath.Join(root, importsSubdir)

	loaded := make([]Recipe, 0, 16)

	// Top-level files: source "user".
	loaded = s.appendDir(loaded, root, false /*recurseImports=*/, SourceUser)
	// Imports subdir: source "imported".
	loaded = s.appendDir(loaded, imports, false, SourceImported)

	snap := append([]Recipe(nil), loaded...)
	s.snapshot.Store(&snap)
	return s.Recipes(), nil
}

// Reload re-runs Load and is called by Save/Delete to refresh the
// snapshot after a write. Errors from Load (only DataDir-empty, in
// practice) are logged at error level — the previous snapshot stays
// in place so readers don't see an empty catalog on a transient
// failure.
func (s *UserStore) Reload() {
	if _, err := s.Load(); err != nil {
		s.logger().Error("recipes: user-store reload failed", slog.String("err", err.Error()))
	}
}

// appendDir reads dir for *.yaml / *.yml files (non-recursive) and
// appends each successfully-parsed recipe to dst with Source=source.
// Errors from individual files are logged + skipped; the function
// never returns an error (boot ergonomics: a single bad file must not
// break the whole load).
//
// Missing directory → noop. The recurseImports parameter is reserved
// for a future change; today the caller invokes appendDir twice
// (once for the top-level, once for _imports) so the function itself
// is non-recursive.
func (s *UserStore) appendDir(dst []Recipe, dir string, _ bool, source string) []Recipe {
	entries, err := os.ReadDir(dir) // #nosec G304 — dataDir is harness-owned
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger().Warn("recipes: user-store: read dir failed",
				slog.String("dir", dir),
				slog.String("err", err.Error()))
		}
		return dst
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isYAMLName(name) {
			continue
		}
		path := filepath.Join(dir, name)
		r, err := s.parseFile(path)
		if err != nil {
			s.logger().Warn("recipes: user-store: skip malformed yaml",
				slog.String("path", path),
				slog.String("err", err.Error()))
			continue
		}
		r.Source = source
		dst = append(dst, r)
	}
	return dst
}

// parseFile reads + parses one yaml file into a Recipe and runs
// Recipe.Validate. The Source field is left empty — the caller stamps
// it after a successful return.
//
// Implementation note: the Recipe struct only carries `json:` tags
// (the embedded shipped.json uses encoding/json), so we route YAML
// → generic map → JSON → Recipe rather than maintaining a parallel
// set of `yaml:` tags on every field. This keeps the schema exactly
// aligned with shipped.json (FR-002) at the cost of one extra
// marshal+unmarshal per file. Recipe count is small; cost is
// negligible.
func (s *UserStore) parseFile(path string) (Recipe, error) {
	data, err := os.ReadFile(path) // #nosec G304 — file path is built from harness-owned dataDir
	if err != nil {
		return Recipe{}, fmt.Errorf("read: %w", err)
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Recipe{}, fmt.Errorf("parse yaml: %w", err)
	}
	// yaml.v3 decodes mappings as map[string]any when keys are
	// strings. JSON requires map[string]any, so coerce
	// map[interface{}]interface{} (which yaml.v3 produces for
	// non-string keys, though the recipe schema has none) into the
	// JSON-friendly shape before re-marshalling.
	raw = coerceYAMLMaps(raw)
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return Recipe{}, fmt.Errorf("re-marshal yaml as json: %w", err)
	}
	var r Recipe
	if err := json.Unmarshal(jsonBytes, &r); err != nil {
		return Recipe{}, fmt.Errorf("decode recipe: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Recipe{}, fmt.Errorf("validate: %w", err)
	}
	return r, nil
}

// coerceYAMLMaps walks the yaml.v3 decode tree and converts any
// map[any]any (which appears when a YAML key is non-string) into
// map[string]any so encoding/json will accept it. The recipe schema
// only ever uses string keys, so non-string keys land as their
// fmt.Sprint representation — sufficient for downstream JSON
// round-trip without panicking.
func coerceYAMLMaps(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = coerceYAMLMaps(val)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = coerceYAMLMaps(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = coerceYAMLMaps(t[i])
		}
		return t
	default:
		return v
	}
}

// logger returns the configured slog logger, defaulting to
// slog.Default when none was supplied at construction.
func (s *UserStore) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// isYAMLName reports whether name has a recognised yaml extension.
// The recipe loader accepts both .yaml and .yml; .json is excluded
// here because the only json file under <DataDir>/mcp/recipes/ today
// is the original-clipboard-paste preserved alongside translated
// recipes (see WP08).
func isYAMLName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

// Save writes a user recipe to <DataDir>/mcp/recipes/<id>.yaml,
// creating the directory if missing. The Recipe.Source field is
// forced to SourceUser before writing. The file is written atomically
// (write temp file + rename) to prevent torn reads. After writing,
// the in-memory snapshot is reloaded so callers see the updated
// catalog immediately.
//
// Callers MUST validate the recipe before calling Save; Save re-runs
// Recipe.Validate as a safety net and returns an error on failure.
func (s *UserStore) Save(r Recipe) error {
	if s == nil {
		return errors.New("recipes: UserStore.Save: nil receiver")
	}
	if s.DataDir == "" {
		return errors.New("recipes: UserStore.Save: DataDir is empty")
	}
	r.Source = SourceUser
	if err := r.Validate(); err != nil {
		return fmt.Errorf("recipes: Save: validate: %w", err)
	}

	root := filepath.Join(s.DataDir, userRecipesSubdir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("recipes: Save: mkdir: %w", err)
	}

	yamlBytes, err := encodeUserRecipeYAML(r)
	if err != nil {
		return fmt.Errorf("recipes: Save: encode: %w", err)
	}

	dst := filepath.Join(root, r.ID+".yaml")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, yamlBytes, 0o600); err != nil {
		return fmt.Errorf("recipes: Save: write temp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("recipes: Save: rename: %w", err)
	}

	// Refresh snapshot so callers see the new recipe immediately.
	s.Reload()
	return nil
}

// Delete removes the user recipe with the given id from disk (removes
// <DataDir>/mcp/recipes/<id>.yaml) and reloads the snapshot. Returns
// ErrRecipeNotFound when no file exists with that id. Only user-owned
// files (top-level *.yaml in <DataDir>/mcp/recipes/) are deletable;
// _imports/ and shipped recipes are out of scope for this method.
func (s *UserStore) Delete(id string) error {
	if s == nil {
		return errors.New("recipes: UserStore.Delete: nil receiver")
	}
	if s.DataDir == "" {
		return errors.New("recipes: UserStore.Delete: DataDir is empty")
	}
	if err := ValidateRecipeID(id); err != nil {
		return fmt.Errorf("recipes: Delete: %w", err)
	}

	path := filepath.Join(s.DataDir, userRecipesSubdir, id+".yaml")
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %q", ErrRecipeNotFound, id)
		}
		return fmt.Errorf("recipes: Delete: remove: %w", err)
	}

	// Refresh snapshot to evict the deleted recipe.
	s.Reload()
	return nil
}

// encodeUserRecipeYAML marshals a user recipe to YAML with a small
// header comment. Reuses the json-round-trip approach from
// encodeRecipeYAML in import.go so the YAML schema stays 1:1 with
// shipped.json.
func encodeUserRecipeYAML(r Recipe) ([]byte, error) {
	jb, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(jb, &m); err != nil {
		return nil, err
	}
	// Remove runtime-only fields stamped by the loader.
	delete(m, "source")
	dropEmptyFields(m)
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	header := "# User recipe — managed by the harness Add MCP Server flow.\n# Edit freely — the harness picks up changes the next time it reads this catalog.\n"
	return append([]byte(header), out...), nil
}
