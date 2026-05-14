package wirecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// RegistryEntry is one row in coverage_registry.yaml.
//
// Schema mirrors docs/coverage-registry-schema.md.
type RegistryEntry struct {
	// Struct is the Go type name being covered: GenerationRequest,
	// Response, or StreamEvent.
	Struct string `yaml:"struct"`
	// Field is the exported Go field name within Struct.
	Field string `yaml:"field"`
	// Adapter is the provider kind (anthropic, openai, openrouter,
	// bedrock) or "*" for fields explicitly rejected by every adapter.
	Adapter string `yaml:"adapter"`
	// Tests lists the fully-qualified test function names that cover
	// this (struct, field, adapter) triple. At least one entry is
	// required when Unsupported is empty.
	Tests []string `yaml:"tests,omitempty"`
	// Unsupported carries a non-empty human-readable reason when the
	// adapter intentionally does not support this field. When set,
	// Tests may be empty. The completeness check accepts this as
	// satisfying coverage.
	Unsupported string `yaml:"unsupported,omitempty"`
}

// Registry holds the decoded entries from coverage_registry.yaml.
type Registry struct {
	Version int             `yaml:"version"`
	Entries []RegistryEntry `yaml:"entries"`
}

// LoadRegistryFromPath decodes the coverage registry at the given
// absolute or working-directory-relative file path.
func LoadRegistryFromPath(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("wirecheck: load registry %q: %w", path, err)
	}
	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("wirecheck: parse registry %q: %w", path, err)
	}
	return &reg, nil
}

// LoadRegistry decodes the coverage registry at the given file path.
// The path may be relative (resolved against the caller's source file
// directory) or absolute.
func LoadRegistry(path string) (*Registry, error) {
	abs := path
	if !filepath.IsAbs(path) {
		_, file, _, ok := runtime.Caller(1)
		if !ok {
			return nil, fmt.Errorf("wirecheck: cannot determine caller location for relative path %q", path)
		}
		abs = filepath.Join(filepath.Dir(file), path)
	}
	return LoadRegistryFromPath(abs)
}

// EntryKey is the composite lookup key for one coverage entry.
type EntryKey struct {
	Struct  string
	Field   string
	Adapter string
}

// Index returns a map from EntryKey to all matching entries (there may
// be multiple rows for the same key — e.g. the same field tested by
// several functions).
func (r *Registry) Index() map[EntryKey][]RegistryEntry {
	m := make(map[EntryKey][]RegistryEntry, len(r.Entries))
	for _, e := range r.Entries {
		k := EntryKey{Struct: e.Struct, Field: e.Field, Adapter: e.Adapter}
		m[k] = append(m[k], e)
	}
	return m
}

// HasCoverage reports whether the registry has at least one satisfying
// entry for the given (struct, field, adapter) triple — either a real
// test or an explicit Unsupported reason.
func (r *Registry) HasCoverage(structName, field, adapter string) bool {
	for _, e := range r.Entries {
		if e.Struct != structName || e.Field != field {
			continue
		}
		if e.Adapter != adapter && e.Adapter != "*" {
			continue
		}
		if e.Unsupported != "" || len(e.Tests) > 0 {
			return true
		}
	}
	return false
}

// TestNames returns the deduplicated list of every test function name
// referenced across all entries.
func (r *Registry) TestNames() []string {
	seen := make(map[string]struct{})
	var names []string
	for _, e := range r.Entries {
		for _, fn := range e.Tests {
			if _, ok := seen[fn]; !ok {
				seen[fn] = struct{}{}
				names = append(names, fn)
			}
		}
	}
	return names
}
