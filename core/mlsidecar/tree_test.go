package mlsidecar

// tree_test.go — design Amendment A5(3)+(4) proofs: the whole-tree digest
// recipe is byte-identical to Kenaz's, and adoption / spawn rest on the
// provenance + tree rule, so either client adopts the other's install.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildCanonicalOnedir lays out the fixture the canonical vector was
// computed over.
func buildCanonicalOnedir(t *testing.T, root string) {
	t.Helper()
	for p, b := range map[string]string{
		"kameas-ml":                  "launcher bytes",
		"_internal/lib/python.dylib": "interpreter",
		"_internal/base_library.zip": "zip",
		"a b.txt":                    "space name",
	} {
		fp := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("lib/python.dylib", filepath.Join(root, "_internal", "Python")); err != nil {
		t.Fatal(err)
	}
}

// TestTreeDigest_CanonicalVector pins the recipe against a vector computed
// INDEPENDENTLY of this code — by a Python sha256sum-format script, and by
// Kenaz's own internal/ml.TreeDigest run on the same fixture (both yield
// this value; 2026-09-30 interop review). A drift here means the two
// clients can no longer adopt each other's installs.
func TestTreeDigest_CanonicalVector(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kameas-ml")
	buildCanonicalOnedir(t, root)
	const want = "sha256:71c475c52113b8077e24afbefa21d0c4cb0938e75fcefc0a089130f7843622ec"
	got, err := TreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("TreeDigest = %s, want the canonical %s", got, want)
	}
}

// kenazSeed writes an install exactly as Kenaz seeds one: a
// "<semver>+<sha12>" label, a Kenaz-shaped install.json (raw JSON, the
// field names Kenaz's internal/ml InstallRecord writes — verified=false,
// provenance kenaz-bundle-digest, installed_by/verification set) and
// `current` flipped to it.
func kenazSeed(t *testing.T, l Layout, label string) (exePath, exeSHA, tree string) {
	t.Helper()
	onedir := l.OnedirPath(label)
	buildCanonicalOnedir(t, onedir)
	exePath = filepath.Join(onedir, EngineExecutableName(""))
	if EngineExecutableName("") != "kameas-ml" {
		if err := os.WriteFile(exePath, []byte("launcher bytes"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if exeSHA, err = HashFileSHA256(exePath); err != nil {
		t.Fatal(err)
	}
	if tree, err = TreeDigest(onedir); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := l.SetCurrent(label); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "version": "` + label + `",
  "engine_sha256": "` + exeSHA + `",
  "source": "kenaz-bundle:/Applications/Kenaz.app/Contents/Resources/kameas-ml",
  "installed_at": "2026-09-30T12:00:00Z",
  "verified": false,
  "tree_sha256": "` + tree + `",
  "provenance": "kenaz-bundle-digest",
  "installed_by": "kenaz",
  "verification": "bundle_digest"
}`
	if err := os.WriteFile(l.InstallJSONPath(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return exePath, exeSHA, tree
}

// TestEvaluateAdoption_AdoptsAKenazSeededEngine_A5 is the whole point of
// A5(4): the harness adopts an engine Kenaz installed — verified=false,
// kenaz-bundle-digest provenance — because the on-disk tree matches the
// recorded digest. The engine self-reports bare hex and 0.1.0.
func TestEvaluateAdoption_AdoptsAKenazSeededEngine_A5(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, exeSHA, _ := kenazSeed(t, l, "0.2.0+1a2b3c4d5e6f")
	health := HealthPayload{Product: "kenaz-ml", SidecarVersion: "0.1.0", ExePath: exePath,
		EngineSHA256: strings.TrimPrefix(exeSHA, "sha256:"), LifecycleProtocol: 1}
	d, err := EvaluateAdoption(l, health, nil)
	if err != nil || d.Action != AdoptAccept {
		t.Fatalf("Kenaz-seeded engine = %+v, %v; want adopted", d, err)
	}
	if !strings.Contains(d.Detail, ProvenanceKenazBundle) {
		t.Errorf("detail %q does not name the provenance it accepted", d.Detail)
	}
}

// TestEvaluateAdoption_TreeTamper_Refused: the launcher still matches, but
// a byte changed in _internal/ — the payload the launcher digest never
// covered. Refused, by adoption and by spawn.
func TestEvaluateAdoption_TreeTamper_Refused(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, exeSHA, _ := kenazSeed(t, l, "0.2.0+1a2b3c4d5e6f")
	lib := filepath.Join(l.OnedirPath("0.2.0+1a2b3c4d5e6f"), "_internal", "lib", "python.dylib")
	if err := os.WriteFile(lib, []byte("INTERPRETER"), 0o644); err != nil {
		t.Fatal(err)
	}
	health := HealthPayload{ExePath: exePath, EngineSHA256: exeSHA, LifecycleProtocol: 1}
	if d, _ := EvaluateAdoption(l, health, nil); d.Action != AdoptRefuseUnverified {
		t.Fatalf("tampered _internal/ = %q; want refused", d.Action)
	}
	spawner := &fakeSpawner{}
	m := NewManager(l, NewClient(unreachableURL(t), nil), spawner, "harness", "0.85.0")
	st := m.Reconcile(context.Background())
	if spawner.called || st.State != StateUnverified {
		t.Fatalf("spawn over a tampered tree: called=%v status=%+v", spawner.called, st)
	}
}

// TestVerifyInstalled_ProvenanceNotVerifiedBit_A5: Verified=true is not
// sufficient (no provenance / no tree digest -> refused) and not required
// (the Kenaz seed above has verified=false and is adopted).
func TestVerifyInstalled_ProvenanceNotVerifiedBit_A5(t *testing.T) {
	l := NewLayout(t.TempDir())
	_, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))
	rec, _, _ := ReadInstallJSON(l)
	for name, mutate := range map[string]func(*InstallRecord){
		"verified but no provenance":      func(r *InstallRecord) { r.Provenance = "" },
		"verified but unknown provenance": func(r *InstallRecord) { r.Provenance = "someone-else" },
		"verified but no tree digest":     func(r *InstallRecord) { r.TreeSHA256 = "" },
		"record names another version":    func(r *InstallRecord) { r.Version = "0.9.0" },
	} {
		r := rec
		r.Verified, r.EngineSHA256 = true, sha
		mutate(&r)
		if err := WriteInstallJSON(l, r); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyInstalled(l, "1.0.0", nil); err == nil {
			t.Errorf("%s: VerifyInstalled accepted it", name)
		}
	}
}

// TestTreeVerifier_CachesByStat_FirstUseRehashes: a fresh verifier always
// hashes; an unchanged tree is then served from the stat cache; any stat
// change (here a same-size rewrite with a new mtime) forces a re-hash that
// catches the tamper.
func TestTreeVerifier_CachesByStat_FirstUseRehashes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kameas-ml")
	buildCanonicalOnedir(t, root)
	want, _ := TreeDigest(root)
	tv := NewTreeVerifier()
	if err := tv.Verify(root, want); err != nil {
		t.Fatal(err)
	}
	if _, hit := tv.ok[root]; !hit {
		t.Fatal("a successful verification was not cached")
	}
	f := filepath.Join(root, "_internal", "base_library.zip")
	if err := os.WriteFile(f, []byte("ZIP"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(f, later, later)
	if err := tv.Verify(root, want); err == nil {
		t.Fatal("a same-size tamper with a new mtime was served from the cache")
	}
	if _, hit := tv.ok[root]; hit {
		t.Fatal("a failed verification left a cache entry behind")
	}
}

// unreachableURL returns a loopback URL nothing listens on, so the
// Manager's /health probe fails at the transport (the spawn branch).
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}
