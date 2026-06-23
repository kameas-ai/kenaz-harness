package personal

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func newTempStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewFileStore(filepath.Join(dir, "kenaz-harness", "providers.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}

func keychainProfile(id string) llm.ProviderProfile {
	return llm.ProviderProfile{
		ID:    id,
		Kind:  "anthropic",
		Model: "claude-sonnet",
		Cred: llm.CredentialReference{
			Kind:    "keychain",
			Locator: "kenaz-harness/" + id,
		},
	}
}

func TestFileStore_AddListRoundTrip(t *testing.T) {
	s := newTempStore(t)
	if err := s.Add(keychainProfile("anthropic-default")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(keychainProfile("openai-personal")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(got))
	}
	// Sorted alphabetically by ID.
	if got[0].ID != "anthropic-default" || got[1].ID != "openai-personal" {
		t.Fatalf("unexpected ordering: %+v", got)
	}
}

func TestFileStore_GetMissing(t *testing.T) {
	s := newTempStore(t)
	_, err := s.Get("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFileStore_DuplicateAddRejected(t *testing.T) {
	s := newTempStore(t)
	if err := s.Add(keychainProfile("dup")); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := s.Add(keychainProfile("dup"))
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestFileStore_RemoveRoundTrip(t *testing.T) {
	s := newTempStore(t)
	if err := s.Add(keychainProfile("a")); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := s.Add(keychainProfile("b")); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if err := s.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("unexpected after remove: %+v", got)
	}
	// Removing a missing id is ErrNotFound.
	if err := s.Remove("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFileStore_AcceptsIndirectCredKinds(t *testing.T) {
	for _, kind := range []string{"keychain", "aws_profile", "env", "file"} {
		t.Run(kind, func(t *testing.T) {
			s := newTempStore(t)
			p := llm.ProviderProfile{
				ID:    "p-" + kind,
				Kind:  "anthropic",
				Model: "claude-sonnet",
				Cred:  llm.CredentialReference{Kind: kind, Locator: "loc-" + kind},
			}
			if err := s.Add(p); err != nil {
				t.Fatalf("Add: %v", err)
			}
		})
	}
}

func TestFileStore_RejectsUnsupportedCredKind(t *testing.T) {
	s := newTempStore(t)
	// "kms" passes the schema validator but the personal store does
	// not yet support it — should be rejected as a non-indirect ref.
	p := llm.ProviderProfile{
		ID:    "p",
		Kind:  "anthropic",
		Model: "claude-sonnet",
		Cred:  llm.CredentialReference{Kind: "kms", Locator: "alias/foo"},
	}
	err := s.Add(p)
	if !errors.Is(err, ErrPlaintextCredential) {
		t.Fatalf("expected ErrPlaintextCredential, got %v", err)
	}
}

func TestFileStore_RejectsInvalidProfile(t *testing.T) {
	s := newTempStore(t)
	err := s.Add(llm.ProviderProfile{ID: "", Kind: "anthropic"})
	if err == nil {
		t.Fatal("expected validation error from empty ID")
	}
}

func TestFileStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kenaz-harness", "providers.json")
	s1, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s1.Add(keychainProfile("persisted")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s2, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore (reopen): %v", err)
	}
	got, err := s2.Get("persisted")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "persisted" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestFileStore_FileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode semantics differ on windows")
	}
	s := newTempStore(t)
	if err := s.Add(keychainProfile("p")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", mode)
	}
}

func TestFileStore_EmptyFileTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kenaz-harness", "providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}

func TestFileStore_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kenaz-harness", "providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_, err = s.List()
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestDefaultPath(t *testing.T) {
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("kenaz-harness", "providers.json")) {
		t.Fatalf("unexpected path %q", got)
	}
}
