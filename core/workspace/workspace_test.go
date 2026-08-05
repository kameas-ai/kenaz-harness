package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolve_NoOverride_Default(t *testing.T) {
	dataDir := t.TempDir()
	r := Resolve("", dataDir)
	if r.Source != SourceDefault {
		t.Fatalf("Source = %q, want default", r.Source)
	}
	if r.Dir != filepath.Join(dataDir, "agent-workspace") {
		t.Errorf("Dir = %q, want <dataDir>/agent-workspace", r.Dir)
	}
	if r.ReadOnly || r.FallbackReason != "" {
		t.Errorf("unexpected flags on default resolution: %+v", r)
	}
}

func TestResolve_WritableOverride_Granted(t *testing.T) {
	override := t.TempDir()
	r := Resolve(override, t.TempDir())
	if r.Source != SourceGranted {
		t.Fatalf("Source = %q, want granted (%+v)", r.Source, r)
	}
	if r.Dir != override && r.Dir != mustEval(t, override) {
		t.Errorf("Dir = %q, want the override %q", r.Dir, override)
	}
	if r.ReadOnly {
		t.Error("writable override must not be flagged read-only")
	}
	// The write probe must not leave droppings.
	entries, err := os.ReadDir(override)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("probe left files behind: %v", entries)
	}
}

func TestResolve_MissingOverride_Fallback(t *testing.T) {
	dataDir := t.TempDir()
	r := Resolve(filepath.Join(t.TempDir(), "nope"), dataDir)
	if r.Source != SourceFallback {
		t.Fatalf("Source = %q, want fallback", r.Source)
	}
	if r.Dir != filepath.Join(dataDir, "agent-workspace") {
		t.Errorf("fallback Dir = %q, want the private default", r.Dir)
	}
	if r.FallbackReason == "" {
		t.Error("fallback must carry an operator-facing reason")
	}
}

func TestResolve_OverrideIsFile_Fallback(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := Resolve(f, t.TempDir()); r.Source != SourceFallback {
		t.Errorf("file override must fall back, got %+v", r)
	}
}

// TestResolve_UnwritableStub_Fallback is the SC-3 case: the /workspace
// tmpfiles stub exists on a grant-less boot but is a plain directory the
// service user cannot write — not a workspace.
func TestResolve_UnwritableStub_Fallback(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("chmod-based unwritability not enforceable here")
	}
	stub := filepath.Join(t.TempDir(), "stub")
	if err := os.Mkdir(stub, 0o555); err != nil {
		t.Fatal(err)
	}
	// Same device as parent → not a mountpoint → fallback.
	r := Resolve(stub, t.TempDir())
	if r.Source != SourceFallback {
		t.Fatalf("unwritable stub must fall back, got %+v", r)
	}
}

// TestResolve_UnwritableMountpoint_KeptReadOnly is the SC-4 case: an ro
// dir grant is a real mounted workspace — kept, flagged. Bind mounts need
// root, so the device split is faked through the statDevFn seam.
func TestResolve_UnwritableMountpoint_KeptReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("chmod-based unwritability not enforceable here")
	}
	mount := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(mount, 0o555); err != nil {
		t.Fatal(err)
	}

	orig := statDevFn
	t.Cleanup(func() { statDevFn = orig })
	statDevFn = func(path string) (uint64, bool) {
		if filepath.Clean(path) == filepath.Clean(mount) {
			return 42, true
		}
		return 1, true
	}

	r := Resolve(mount, t.TempDir())
	if r.Source != SourceGranted {
		t.Fatalf("ro mountpoint must be kept, got %+v", r)
	}
	if !r.ReadOnly {
		t.Error("ro mountpoint must be flagged ReadOnly")
	}
}

func TestEnsure_CreatesWithMarker_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	ws, err := Ensure(dataDir)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	marker := filepath.Join(ws, MarkerName)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing after first Ensure: %v", err)
	}
	if err := os.WriteFile(marker, []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws2, err := Ensure(dataDir)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if ws2 != ws {
		t.Errorf("Ensure not stable: %q then %q", ws, ws2)
	}
	b, _ := os.ReadFile(marker)
	if string(b) != "edited" {
		t.Error("second Ensure must leave the existing marker untouched")
	}
}

func TestEnsure_EmptyDataDir_Error(t *testing.T) {
	if _, err := Ensure(""); err == nil {
		t.Fatal("empty dataDir must error")
	}
}

// TestResolve_NeverMarksOverride: the marker discipline (plan D3) — Resolve
// on a granted dir must not create anything persistent there, and Ensure
// only ever targets the DataDir default.
func TestResolve_NeverMarksOverride(t *testing.T) {
	override := t.TempDir()
	_ = Resolve(override, t.TempDir())
	if _, err := os.Stat(filepath.Join(override, MarkerName)); !os.IsNotExist(err) {
		t.Errorf("Resolve must not write the marker into an override dir")
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
