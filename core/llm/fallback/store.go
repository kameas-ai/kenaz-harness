package fallback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Store defines the persistence surface for user-managed fallback chains.
// The connector and the RPC layer depend on this interface; tests inject a
// fake. FSStore is the production implementation.
type Store interface {
	// List returns all stored chains. The order is unspecified.
	List(ctx context.Context) ([]*Chain, error)
	// Get retrieves a single chain by ID. Returns nil, nil when not found.
	Get(ctx context.Context, id string) (*Chain, error)
	// Save persists c, creating the file when new or overwriting when the
	// ID already exists. Validates before writing; returns an error when
	// validation fails or writing fails.
	Save(ctx context.Context, c *Chain) error
	// Delete removes the chain with the given ID. Returns nil when the id
	// does not exist (idempotent).
	Delete(ctx context.Context, id string) error
}

// FSStore is a Store backed by a directory of YAML files. Each chain is
// stored as <dir>/<chain-id>.yaml. The directory is created lazily.
type FSStore struct {
	dir string
}

// NewFSStore constructs an FSStore rooted at dir. The directory is created
// on first write; reads from a non-existent directory return empty results.
func NewFSStore(dir string) *FSStore {
	return &FSStore{dir: dir}
}

// List reads all *.yaml files in the store directory.
func (s *FSStore) List(_ context.Context) ([]*Chain, error) {
	chains, err := LoadFromDir(s.dir)
	if err != nil {
		// LoadFromDir may return partial results + error; surface both.
		return chains, fmt.Errorf("fallback.FSStore.List: %w", err)
	}
	return chains, nil
}

// Get retrieves the chain with the given ID from <dir>/<id>.yaml.
// Returns (nil, nil) when the file does not exist.
func (s *FSStore) Get(_ context.Context, id string) (*Chain, error) {
	if id == "" {
		return nil, fmt.Errorf("fallback.FSStore.Get: id must not be empty")
	}
	path := filepath.Join(s.dir, id+".yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fallback.FSStore.Get %q: %w", id, err)
	}
	c, err := parseChain(data)
	if err != nil {
		return nil, fmt.Errorf("fallback.FSStore.Get %q parse: %w", id, err)
	}
	return c, nil
}

// Save validates c then writes it to <dir>/<c.ID>.yaml, creating the
// directory if necessary.
func (s *FSStore) Save(_ context.Context, c *Chain) error {
	if c == nil {
		return fmt.Errorf("fallback.FSStore.Save: chain must not be nil")
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("fallback.FSStore.Save: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("fallback.FSStore.Save mkdir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("fallback.FSStore.Save marshal %q: %w", c.ID, err)
	}
	path := filepath.Join(s.dir, c.ID+".yaml")
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return fmt.Errorf("fallback.FSStore.Save write %q: %w", c.ID, err)
	}
	return nil
}

// Delete removes <dir>/<id>.yaml if it exists. Idempotent: returns nil
// when the file is not found.
func (s *FSStore) Delete(_ context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("fallback.FSStore.Delete: id must not be empty")
	}
	path := filepath.Join(s.dir, id+".yaml")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fallback.FSStore.Delete %q: %w", id, err)
	}
	return nil
}
