package mlsidecar

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLayout_EnsureDirs_CreatesExpectedTree(t *testing.T) {
	root := t.TempDir()
	l := NewLayout(filepath.Join(root, "kameas", "ml"))
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{l.Root, l.VersionsDir(), l.CheckpointsDir(), l.LeaseDir()} {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Errorf("expected directory %s to exist, err=%v", d, err)
		}
	}
}

func TestLayout_SetCurrent_And_CurrentVersionDir_RoundTrip(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := os.MkdirAll(l.VersionDir("1.0.0"), 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := l.SetCurrent("1.0.0"); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	got, err := l.CurrentVersionDir()
	if err != nil {
		t.Fatalf("CurrentVersionDir: %v", err)
	}
	want, _ := filepath.EvalSymlinks(l.VersionDir("1.0.0"))
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != want {
		t.Errorf("CurrentVersionDir = %s, want %s", gotResolved, want)
	}

	// Flip to a second version — SetCurrent must be a real swap, not an
	// error-on-exists.
	if err := os.MkdirAll(l.VersionDir("2.0.0"), 0o755); err != nil {
		t.Fatalf("mkdir v2: %v", err)
	}
	if err := l.SetCurrent("2.0.0"); err != nil {
		t.Fatalf("SetCurrent v2: %v", err)
	}
	got2, err := l.CurrentVersionDir()
	if err != nil {
		t.Fatalf("CurrentVersionDir after flip: %v", err)
	}
	if filepath.Base(got2) != "2.0.0" {
		t.Errorf("CurrentVersionDir after flip = %s, want a path ending in 2.0.0", got2)
	}
}

func TestLayout_CurrentVersionDir_NoSymlink_Errors(t *testing.T) {
	l := NewLayout(t.TempDir())
	if _, err := l.CurrentVersionDir(); err == nil {
		t.Fatal("expected an error when `current` does not exist")
	}
}

func TestLayout_CurrentVersionDir_RejectsEscapingTarget(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, l.CurrentLink()); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := l.CurrentVersionDir(); err == nil {
		t.Fatal("expected an error when `current` points outside versions/")
	}
}

func TestInstallJSON_RoundTrip(t *testing.T) {
	l := NewLayout(t.TempDir())
	if _, ok, err := ReadInstallJSON(l); err != nil || ok {
		t.Fatalf("expected no record on a fresh root: ok=%v err=%v", ok, err)
	}
	rec := InstallRecord{
		Version:      "1.0.0",
		EngineSHA256: "sha256:abc",
		Source:       "local_path:/tmp/channel",
		InstalledAt:  time.Now().UTC().Truncate(time.Second),
		Verified:     true,
	}
	if err := WriteInstallJSON(l, rec); err != nil {
		t.Fatalf("WriteInstallJSON: %v", err)
	}
	got, ok, err := ReadInstallJSON(l)
	if err != nil || !ok {
		t.Fatalf("ReadInstallJSON: ok=%v err=%v", ok, err)
	}
	if got.Version != rec.Version || got.EngineSHA256 != rec.EngineSHA256 || got.Verified != rec.Verified {
		t.Errorf("got %+v, want %+v", got, rec)
	}
}

func TestDefaultRootFor(t *testing.T) {
	// Design Amendment A5(2): ~/.kenaz/ml/<env>, the same on every OS and
	// the same root Kenaz resolves.
	for _, env := range []string{"prod", "dev", "test"} {
		got, err := DefaultRootFor("/Users/alice", env)
		if err != nil {
			t.Fatalf("DefaultRootFor(%s): %v", env, err)
		}
		if want := filepath.Join("/Users/alice", ".kenaz", "ml", env); got != want {
			t.Errorf("DefaultRootFor(%s) = %s, want %s", env, got, want)
		}
	}
	if _, err := DefaultRootFor("", "prod"); err == nil {
		t.Error("expected an error with no home dir")
	}
	if _, err := DefaultRootFor("/Users/alice", "stage"); err == nil {
		t.Error("stage is not an engine env; it must map to prod before reaching DefaultRootFor")
	}
}

func TestEngineEnvAndPorts_A5(t *testing.T) {
	cases := map[string]struct {
		env  string
		port int
	}{
		"":      {"prod", 7774},
		"prod":  {"prod", 7774},
		"stage": {"prod", 7774},
		"dev":   {"dev", 7775},
		"local": {"dev", 7775},
		"test":  {"test", 7776},
		"TEST ": {"test", 7776},
		"bogus": {"prod", 7774},
	}
	for raw, want := range cases {
		env := EngineEnvFor(raw)
		if env != want.env || EnginePort(env) != want.port {
			t.Errorf("KENAZ_HARNESS_ENV=%q -> env %q port %d, want %q %d", raw, env, EnginePort(env), want.env, want.port)
		}
		if got := BaseURLForEnv(env); got != fmt.Sprintf("http://127.0.0.1:%d", want.port) {
			t.Errorf("BaseURLForEnv(%q) = %s", env, got)
		}
	}
	t.Setenv("KENAZ_HARNESS_ENV", "dev")
	if got := DefaultEngineBaseURL(); got != "http://127.0.0.1:7775" {
		t.Errorf("DefaultEngineBaseURL under dev = %s", got)
	}
}
