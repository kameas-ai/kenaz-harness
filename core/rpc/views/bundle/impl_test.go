package bundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type stubReader struct {
	data []byte
	err  error
}

func (s stubReader) ReadLockfile() ([]byte, error) { return s.data, s.err }

// memReadWriter implements both Reader and Writer against an in-memory
// blob; used by Install/Remove tests where round-tripping the lockfile
// matters but disk I/O does not.
type memReadWriter struct {
	mu   sync.Mutex
	data []byte
}

func (m *memReadWriter) ReadLockfile() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.data) == 0 {
		return []byte("schema_version = 1\n"), nil
	}
	out := make([]byte, len(m.data))
	copy(out, m.data)
	return out, nil
}

func (m *memReadWriter) WriteLockfile(b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make([]byte, len(b))
	copy(m.data, b)
	return nil
}

type stubCAS struct{ have map[string]bool }

func (s stubCAS) Has(d string) bool { return s.have[d] }

func TestList_EmptyLockfile(t *testing.T) {
	api := NewAPI(WithReader(stubReader{data: []byte("schema_version = 1\n")}))
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty lockfile should yield 0 bundles, got %d", len(got))
	}
}

func TestList_ParsesBundlesAndSorts(t *testing.T) {
	tom := []byte(`schema_version = 1

[[bundle]]
name = "zeta"
version = "1.0.0"
source = "https://example.com/zeta"
content_hash = "sha256:` + sha64("zeta") + `"

[[bundle]]
name = "alpha"
version = "0.2.0"
source = "https://example.com/alpha"
content_hash = "sha256:` + sha64("alpha") + `"
signature_ref = "sigstore://abc"
`)
	api := NewAPI(WithReader(stubReader{data: tom}))
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 bundles, got %d", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Errorf("expected sorted [alpha, zeta], got [%s, %s]", got[0].Name, got[1].Name)
	}
	if got[0].Tier != "signed" {
		t.Errorf("alpha tier = %q, want signed", got[0].Tier)
	}
	if got[1].Tier != "channel" {
		t.Errorf("zeta tier = %q, want channel", got[1].Tier)
	}
	if got[0].Source == "" {
		t.Errorf("source channel should be exposed in list payloads")
	}
}

func TestList_UncachedTierAnnotation(t *testing.T) {
	hash := "sha256:" + sha64("z")
	tom := []byte(`schema_version = 1

[[bundle]]
name = "z"
version = "1.0.0"
source = "https://example.com/z"
content_hash = "` + hash + `"
`)
	cas := stubCAS{have: map[string]bool{}} // no cache hits
	api := NewAPI(WithReader(stubReader{data: tom}), WithCAS(cas))
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Tier != "channel (uncached)" {
		t.Errorf("missing-cache should annotate tier; got %q", got[0].Tier)
	}
}

func TestGet_IncludesArtifacts(t *testing.T) {
	tom := []byte(`schema_version = 1

[[bundle]]
name = "alpha"
version = "0.2.0"
source = "https://example.com/alpha"
content_hash = "sha256:` + sha64("alpha") + `"

  [[bundle.artifact]]
  name = "policy.toml"
  kind = "policy"
  content_hash = "sha256:` + sha64("a-policy") + `"

  [[bundle.artifact]]
  name = "mcp.json"
  kind = "mcp"
  content_hash = "sha256:` + sha64("a-mcp") + `"
`)
	api := NewAPI(WithReader(stubReader{data: tom}))
	got, err := api.Get(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alpha" {
		t.Errorf("Get returned %q, want alpha", got.Name)
	}
	if got.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2", got.ArtifactCount)
	}
	if len(got.Artifacts) != 2 {
		t.Errorf("Artifacts len = %d, want 2", len(got.Artifacts))
	}
}

func TestGet_NotFound(t *testing.T) {
	tom := []byte("schema_version = 1\n")
	api := NewAPI(WithReader(stubReader{data: tom}))
	_, err := api.Get(context.Background(), "ghost")
	if err == nil {
		t.Fatalf("Get of unknown bundle should error")
	}
}

func TestList_NoReader_ReturnsEmpty(t *testing.T) {
	api := NewAPI()
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil reader path should yield empty slice, got %d", len(got))
	}
}

func TestInstall_LocalPathRegistersBundleInLockfile(t *testing.T) {
	tmp := t.TempDir()
	bundleDir := filepath.Join(tmp, "alpha")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifestYAML := `schema_version: 1
name: alpha
version: 0.2.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "sha256:` + sha64("a-policy") + `"
`
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw))

	got, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got.Name != "alpha" || got.Version != "0.2.0" {
		t.Errorf("Install returned %+v", got)
	}
	if got.ArtifactCount != 1 {
		t.Errorf("ArtifactCount = %d want 1", got.ArtifactCount)
	}
	if !strings.Contains(string(rw.data), `name = "alpha"`) {
		t.Errorf("lockfile not updated; got=%q", rw.data)
	}

	// List should now surface the installed bundle.
	list, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "alpha" {
		t.Errorf("expected 1 alpha bundle, got %+v", list)
	}
}

func TestInstall_RejectsNonLocalPath(t *testing.T) {
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw))
	_, err := api.Install(context.Background(), InstallRequest{Kind: "git", Path: "/tmp/anything"})
	if err == nil {
		t.Fatalf("non-localpath kind should be rejected in v0.3.0")
	}
}

func TestInstall_RejectsRelativePath(t *testing.T) {
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw))
	_, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: "relative/path"})
	if err == nil {
		t.Fatalf("relative path should be rejected")
	}
}

func TestInstall_NoWriter_Errors(t *testing.T) {
	api := NewAPI(WithReader(stubReader{data: []byte("schema_version = 1\n")}))
	_, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: "/tmp/x"})
	if err == nil {
		t.Fatalf("install without writer should error")
	}
}

func TestRemove_DropsNamedBundle(t *testing.T) {
	rw := &memReadWriter{data: []byte(`schema_version = 1

[[bundle]]
name = "alpha"
version = "0.2.0"
source = "local_path:/tmp/alpha"
content_hash = "sha256:` + sha64("alpha") + `"

[[bundle]]
name = "zeta"
version = "1.0.0"
source = "local_path:/tmp/zeta"
content_hash = "sha256:` + sha64("zeta") + `"
`)}
	api := NewAPI(WithReader(rw), WithWriter(rw))
	if err := api.Remove(context.Background(), "alpha"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	list, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "zeta" {
		t.Errorf("expected only zeta, got %+v", list)
	}
}

func TestRemove_UnknownIdIsNoOp(t *testing.T) {
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw))
	if err := api.Remove(context.Background(), "ghost"); err != nil {
		t.Fatalf("removing unknown id should be no-op, got: %v", err)
	}
}

func TestRemove_NoWriter_Errors(t *testing.T) {
	api := NewAPI(WithReader(stubReader{data: []byte("schema_version = 1\n")}))
	if err := api.Remove(context.Background(), "alpha"); err == nil {
		t.Fatalf("remove without writer should error")
	}
}

// sha64 returns a 64-char hex digest derived from a deterministic string.
// Lockfile parser validates digest length but not provenance, so a stable
// per-input string suffices for fixtures.
func sha64(seed string) string {
	const hexAlphabet = "0123456789abcdef"
	out := make([]byte, 64)
	for i := 0; i < 64; i++ {
		out[i] = hexAlphabet[(int(seed[i%len(seed)])+i)%16]
	}
	return string(out)
}
