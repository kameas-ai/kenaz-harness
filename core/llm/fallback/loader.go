package fallback

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed bundled/*.yaml
var bundledFS embed.FS

// LoadBundled returns the chains embedded in the binary bundle.
// These are the out-of-the-box defaults shipped with the harness;
// operators can override them by saving chains with the same ID
// to the FSStore, which takes precedence at resolution time.
func LoadBundled() ([]*Chain, error) {
	entries, err := fs.ReadDir(bundledFS, "bundled")
	if err != nil {
		return nil, fmt.Errorf("fallback: read bundled dir: %w", err)
	}
	var chains []*Chain
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := bundledFS.ReadFile("bundled/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("fallback: read bundled %s: %w", e.Name(), err)
		}
		c, err := parseChain(data)
		if err != nil {
			return nil, fmt.Errorf("fallback: parse bundled %s: %w", e.Name(), err)
		}
		chains = append(chains, c)
	}
	return chains, nil
}

// LoadFromDir reads all *.yaml files in dir and returns the parsed chains.
// Files that fail to parse are skipped with a logged error; partial results
// may be returned alongside a non-nil error if some files succeed.
func LoadFromDir(dir string) ([]*Chain, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil // empty dir is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("fallback: read dir %s: %w", dir, err)
	}
	var (
		chains []*Chain
		errs   []string
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		c, err := parseChain(data)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		chains = append(chains, c)
	}
	if len(errs) > 0 {
		return chains, fmt.Errorf("fallback: LoadFromDir errors: %s", strings.Join(errs, "; "))
	}
	return chains, nil
}

// parseChain decodes YAML bytes into a Chain and validates it.
func parseChain(data []byte) (*Chain, error) {
	var c Chain
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}
