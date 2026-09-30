package mlsidecar

import (
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
	t.Run("darwin", func(t *testing.T) {
		got, err := DefaultRootFor("darwin", "/Users/alice", "")
		if err != nil {
			t.Fatalf("DefaultRootFor: %v", err)
		}
		want := "/Users/alice/Library/Application Support/kameas/ml"
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
	t.Run("linux with XDG_DATA_HOME", func(t *testing.T) {
		got, err := DefaultRootFor("linux", "/home/alice", "/home/alice/.data")
		if err != nil {
			t.Fatalf("DefaultRootFor: %v", err)
		}
		want := "/home/alice/.data/kameas/ml"
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
	t.Run("linux fallback to home", func(t *testing.T) {
		got, err := DefaultRootFor("linux", "/home/alice", "")
		if err != nil {
			t.Fatalf("DefaultRootFor: %v", err)
		}
		want := "/home/alice/.local/share/kameas/ml"
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
	t.Run("windows has no freeze target (design §6.3 / OQ-N8)", func(t *testing.T) {
		if _, err := DefaultRootFor("windows", "C:\\Users\\alice", ""); err == nil {
			t.Fatal("expected an error for windows — no kenaz-ml freeze target exists yet")
		}
	})
}
