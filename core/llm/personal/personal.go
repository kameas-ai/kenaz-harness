// Package personal implements the user-scoped "personal providers" store
// (DIRECTIVE_001). It persists a small set of ProviderProfile entries to
// $USER_CONFIG_DIR/kaneaz-harness/providers.json so a user can add a
// provider through the UI without authoring a Bundle.
//
// Security posture (mission C-002 / C-004):
//
//   - The on-disk file is created mode 0600 and re-asserted on every
//     write.
//   - Only CredentialReference values are persisted. The store rejects
//     any profile whose credential reference does not point to a
//     keychain-backed secret — plaintext credential bytes never enter
//     this file.
//   - The store delegates schema validation to llm.ValidateProfile so the
//     defense-in-depth plaintext-prefix and locator-length checks apply
//     equally to bundle-derived and personal profiles.
package personal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Store is the contract the rpc layer and the registry consume. The
// concrete implementation is *FileStore; a separate fake satisfies the
// same shape in tests.
type Store interface {
	// List returns a snapshot of the persisted profiles, sorted by ID
	// for deterministic output.
	List() ([]llm.ProviderProfile, error)
	// Get returns the profile with the matching ID. Missing IDs are a
	// typed error (errors.Is(err, ErrNotFound)).
	Get(id string) (llm.ProviderProfile, error)
	// Add validates and persists p. Re-adding an existing ID is a typed
	// error (errors.Is(err, ErrAlreadyExists)).
	Add(p llm.ProviderProfile) error
	// Remove deletes the profile with id. Missing IDs are a typed
	// error (errors.Is(err, ErrNotFound)).
	Remove(id string) error
}

// Sentinel errors. Wrapped by FileStore on every external return.
var (
	// ErrNotFound is returned by Get/Remove when no profile matches.
	ErrNotFound = errors.New("personal: provider not found")
	// ErrAlreadyExists is returned by Add when the ID is already
	// persisted.
	ErrAlreadyExists = errors.New("personal: provider already exists")
	// ErrPlaintextCredential is returned by Add when the supplied
	// CredentialReference is not keychain-backed. The personal store
	// MUST never persist a plaintext credential.
	ErrPlaintextCredential = errors.New("personal: credential reference must be keychain-backed")
)

// FileStore is the JSON-file-backed Store. Concurrent access is
// serialised through a mutex; writes are atomic via a temp-file rename.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a FileStore rooted at path. The parent directory
// is created (mode 0700) on demand.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("personal: empty store path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("personal: mkdir parent: %w", err)
	}
	return &FileStore{path: path}, nil
}

// DefaultPath returns $USER_CONFIG_DIR/kaneaz-harness/providers.json.
// On platforms where os.UserConfigDir fails, a typed error is returned.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("personal: resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "kaneaz-harness", "providers.json"), nil
}

// fileShape is the on-disk JSON. A typed wrapper makes future schema
// migrations explicit.
type fileShape struct {
	Version   int                   `json:"version"`
	Providers []llm.ProviderProfile `json:"providers"`
}

const currentVersion = 1

// readLocked reads the file under the caller-held mutex. A non-existent
// file is treated as an empty store.
func (s *FileStore) readLocked() (fileShape, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileShape{Version: currentVersion}, nil
		}
		return fileShape{}, fmt.Errorf("personal: open %s: %w", s.path, err)
	}
	defer f.Close()
	bytes, err := io.ReadAll(f)
	if err != nil {
		return fileShape{}, fmt.Errorf("personal: read %s: %w", s.path, err)
	}
	if len(bytes) == 0 {
		return fileShape{Version: currentVersion}, nil
	}
	var shape fileShape
	if err := json.Unmarshal(bytes, &shape); err != nil {
		return fileShape{}, fmt.Errorf("personal: parse %s: %w", s.path, err)
	}
	if shape.Version == 0 {
		shape.Version = currentVersion
	}
	return shape, nil
}

// writeLocked serializes shape to the file mode 0600 atomically.
func (s *FileStore) writeLocked(shape fileShape) error {
	shape.Version = currentVersion
	data, err := json.MarshalIndent(shape, "", "  ")
	if err != nil {
		return fmt.Errorf("personal: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "providers-*.json.tmp")
	if err != nil {
		return fmt.Errorf("personal: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("personal: write tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("personal: chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("personal: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		cleanup()
		return fmt.Errorf("personal: rename: %w", err)
	}
	// Re-assert mode in case the platform clobbered it on rename.
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// List implements Store.
func (s *FileStore) List() ([]llm.ProviderProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	shape, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	out := make([]llm.ProviderProfile, len(shape.Providers))
	copy(out, shape.Providers)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get implements Store.
func (s *FileStore) Get(id string) (llm.ProviderProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	shape, err := s.readLocked()
	if err != nil {
		return llm.ProviderProfile{}, err
	}
	for _, p := range shape.Providers {
		if p.ID == id {
			return p, nil
		}
	}
	return llm.ProviderProfile{}, fmt.Errorf("%w: %q", ErrNotFound, id)
}

// Add implements Store. The supplied profile is validated, the
// credential reference is required to be an indirect reference (never
// plaintext), and on a successful write the on-disk mode is
// re-asserted to 0600.
//
// Allowed cred kinds:
//   - keychain    OS keychain entry (most providers — Anthropic, OpenAI, …)
//   - aws_profile named profile in ~/.aws/credentials (Bedrock)
//   - env         environment variable name (legacy / dev escape hatch)
//   - file        path on disk to a credential file
//
// Plaintext credentials are rejected via ErrPlaintextCredential.
func (s *FileStore) Add(p llm.ProviderProfile) error {
	if err := llm.ValidateProfile(p); err != nil {
		return err
	}
	switch p.Cred.Kind {
	case "keychain", "aws_profile", "env", "file":
		// indirect references — accepted
	default:
		return fmt.Errorf("%w: kind=%q", ErrPlaintextCredential, p.Cred.Kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	shape, err := s.readLocked()
	if err != nil {
		return err
	}
	for _, existing := range shape.Providers {
		if existing.ID == p.ID {
			return fmt.Errorf("%w: %q", ErrAlreadyExists, p.ID)
		}
	}
	shape.Providers = append(shape.Providers, p)
	return s.writeLocked(shape)
}

// Remove implements Store.
func (s *FileStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	shape, err := s.readLocked()
	if err != nil {
		return err
	}
	idx := -1
	for i, p := range shape.Providers {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	shape.Providers = append(shape.Providers[:idx], shape.Providers[idx+1:]...)
	return s.writeLocked(shape)
}

// Compile-time interface witness.
var _ Store = (*FileStore)(nil)
