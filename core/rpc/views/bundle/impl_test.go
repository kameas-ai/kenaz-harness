package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
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

// Put satisfies CASLike (UNIT-5 added Put alongside Has). List/Get
// tests using stubCAS never call Install, so this only needs to be
// present and well-behaved, not exercised — it drains the reader and
// records the digest as present, mirroring the real CAS's contract.
func (s stubCAS) Put(_ context.Context, r io.Reader, expected string) (cache.Receipt, error) {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return cache.Receipt{}, err
	}
	if s.have != nil {
		s.have[expected] = true
	}
	return cache.Receipt{Digest: expected}, nil
}

// testCAS returns a real, disk-backed CAS wrapped as CASLike — UNIT-5's
// Install now genuinely fetches and hash-verifies artifact bytes, so
// any test that installs a manifest with artifacts needs a real CAS,
// not a stub that only tracks Has (spec §11.1: "real CAS, real
// filesystem, real engine").
func testCAS(t *testing.T) CASLike {
	t.Helper()
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	return CASFromCache(c)
}

// sha256Hex returns "sha256:<hex>" of b — the real digest, distinct
// from the fixture-only sha64() below (which produces a validly-shaped
// but non-cryptographic placeholder used where a fixture's bytes are
// never actually fetched/hashed by the code under test).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

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
verified = true
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

// TestList_SignatureRefWithoutVerified_IsNotSigned is UNIT-4's G-2: a
// lockfile row can carry a non-empty signature_ref (a locator
// reference) with no recorded verification result — every row a
// v0.64.0-and-earlier release ever wrote has exactly this shape,
// because the "verified" key did not exist yet (AC-008). Rendering it
// as "signed" from ref presence alone is the FR-006 defect UNIT-4
// closes.
//
// Mutation: restore `if lb.SignatureRef != "" { tier = "signed" }` in
// lockedToBundle — this test must go red.
func TestList_SignatureRefWithoutVerified_IsNotSigned(t *testing.T) {
	tom := []byte(`schema_version = 1

[[bundle]]
name = "legacy"
version = "1.0.0"
source = "local_path:/tmp/legacy"
content_hash = "sha256:` + sha64("legacy") + `"
signature_ref = "kenaz.yaml.sig"
`)
	api := NewAPI(WithReader(stubReader{data: tom}))
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(got))
	}
	if got[0].Tier == "signed" {
		t.Errorf("tier=%q for a row with signature_ref but no verified=true — should not be \"signed\"", got[0].Tier)
	}
	if got[0].Signature == "" {
		t.Errorf("the signature ref itself should still be exposed for display, even though it doesn't grant the tier")
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
	if err := os.MkdirAll(filepath.Join(bundleDir, "policy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// UNIT-5: Install now actually fetches artifact bytes, so the
	// fixture must write real bytes whose real sha256 matches the
	// manifest's content_hash — a declared-but-absent artifact now
	// refuses the install (that refusal IS the fix; see
	// TestInstall_LocalPathFetchesArtifactBytesIntoCAS for the
	// dedicated CAS-focused coverage).
	artifactBytes := []byte("alpha policy contents")
	digest := sha256Hex(artifactBytes)
	if err := os.WriteFile(filepath.Join(bundleDir, "policy", "policy.toml"), artifactBytes, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifestYAML := `schema_version: 1
name: alpha
version: 0.2.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "` + digest + `"
`
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(testCAS(t)))

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

// TestInstall_LocalPathFetchesArtifactBytesIntoCAS is AC-005: the whole
// point of UNIT-5. Before this unit, Install's own doc comment admitted
// "Artifact bytes are NOT fetched into the CAS" — a bundle's row
// pointed at a directory, and removing that directory silently
// orphaned the "installed" bundle. This test asserts the CAS directly
// and the post-delete read, not just the returned struct (spec §11.2:
// "the whole of §1.2 is a path that returns a plausible struct having
// copied nothing").
func TestInstall_LocalPathFetchesArtifactBytesIntoCAS(t *testing.T) {
	tmp := t.TempDir()
	bundleDir := filepath.Join(tmp, "alpha")
	if err := os.MkdirAll(filepath.Join(bundleDir, "policy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	artifactBytes := []byte("real policy bytes for AC-005")
	digest := sha256Hex(artifactBytes)
	if err := os.WriteFile(filepath.Join(bundleDir, "policy", "policy.toml"), artifactBytes, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifestYAML := `schema_version: 1
name: alpha
version: 0.2.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "` + digest + `"
`
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rw := &memReadWriter{}
	casDir := t.TempDir()
	realCAS, err := cache.New(casDir)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(CASFromCache(realCAS)))

	got, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if strings.Contains(got.Tier, "(uncached)") {
		t.Errorf("freshly-installed bundle should not carry (uncached); got tier=%q", got.Tier)
	}
	if !realCAS.Has(digest) {
		t.Fatalf("artifact digest %s not present in CAS after install — bytes were never actually fetched", digest)
	}

	// AC-005: deleting the source and calling Get must still return the
	// artifact — proof the bytes live in the CAS, not just referenced
	// by a pointer back to the source directory.
	if err := os.RemoveAll(bundleDir); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	got2, err := api.Get(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Get after source deletion: %v", err)
	}
	if len(got2.Artifacts) != 1 || got2.Artifacts[0].ContentHash != digest {
		t.Fatalf("Get after source deletion = %+v, want 1 artifact with digest %s", got2.Artifacts, digest)
	}
	if strings.Contains(got2.Tier, "(uncached)") {
		t.Errorf("post-delete List/Get should still report cached (bytes live in the CAS, not the source dir); got tier=%q", got2.Tier)
	}
}

// TestInstall_CorruptArtifactRefusesInstall is AC-005's other half: a
// manifest whose declared content_hash does not match the artifact's
// real bytes must refuse the install, leave no lockfile row, and leave
// no CAS entry for the mismatched digest.
func TestInstall_CorruptArtifactRefusesInstall(t *testing.T) {
	tmp := t.TempDir()
	bundleDir := filepath.Join(tmp, "alpha")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "policy.toml"), []byte("actual on-disk bytes"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	// wrongDigest is a validly-shaped sha256 string that does NOT match
	// the bytes actually on disk above — simulating corruption/tampering
	// between manifest authoring and install.
	wrongDigest := "sha256:" + sha64("not-the-real-content")
	manifestYAML := `schema_version: 1
name: alpha
version: 0.2.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy.toml
    content_hash: "` + wrongDigest + `"
`
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rw := &memReadWriter{}
	casDir := t.TempDir()
	realCAS, err := cache.New(casDir)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(CASFromCache(realCAS)))

	_, err = api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if err == nil {
		t.Fatalf("install with a mismatched content_hash should refuse")
	}
	if len(rw.data) != 0 {
		t.Errorf("refused install must not write a lockfile row; got %q", rw.data)
	}
	if realCAS.Has(wrongDigest) {
		t.Errorf("refused install must not leave a CAS entry for the mismatched digest")
	}
}

// TestInstall_NoCAS_ArtifactBearingBundle_Refuses documents that an
// artifact-bearing manifest genuinely requires a configured CAS —
// there is nowhere else for UNIT-5's fetched bytes to go. A bundle
// with zero declared artifacts still installs without one (unaffected
// by this requirement).
func TestInstall_NoCAS_ArtifactBearingBundle_Refuses(t *testing.T) {
	tmp := t.TempDir()
	bundleDir := filepath.Join(tmp, "alpha")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "policy.toml"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifestYAML := `schema_version: 1
name: alpha
version: 0.2.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy.toml
    content_hash: "` + sha256Hex([]byte("bytes")) + `"
`
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw)) // no WithCAS

	_, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if err == nil {
		t.Fatalf("install of an artifact-bearing bundle with no CAS configured should refuse, not silently skip the fetch")
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
