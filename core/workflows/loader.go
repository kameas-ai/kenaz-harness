package workflows

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileSizeCap is the WP01 NFR-003 ceiling on a single workflow file.
const FileSizeCap = 256 * 1024

// LoadYAML parses a single YAML payload into a Workflow and runs the
// load-time validator.
func LoadYAML(data []byte) (Workflow, error) {
	if len(data) > FileSizeCap {
		return Workflow{}, fmt.Errorf("%w: %d bytes", ErrFileTooLarge, len(data))
	}
	var w Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return Workflow{}, fmt.Errorf("workflows: yaml unmarshal: %w", err)
	}
	if err := Validate(w); err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// MarshalYAML returns the canonical YAML serialization of w.
func MarshalYAML(w Workflow) ([]byte, error) {
	return yaml.Marshal(w)
}

//go:embed builtin/*.yaml
var builtinFS embed.FS

// LoadBuiltins returns every workflow embedded under
// core/workflows/builtin/. Files that fail validation are skipped
// with their error returned alongside the successful results — the
// caller decides whether to log or fail-fast.
func LoadBuiltins() ([]Workflow, []error) {
	return loadFromFS(builtinFS, "builtin")
}

func loadFromFS(fsys fs.FS, root string) ([]Workflow, []error) {
	var out []Workflow
	var errs []error
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		// Empty embed (no files matched) is not an error — return
		// nil slices.
		return nil, nil
	}
	// Stable order so callers see deterministic listings.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, root+"/"+e.Name())
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", e.Name(), err))
			continue
		}
		w, err := LoadYAML(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("load %s: %w", e.Name(), err))
			continue
		}
		out = append(out, w)
	}
	return out, errs
}
