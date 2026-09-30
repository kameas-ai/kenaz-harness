package mlsidecar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnsWholeRoot(t *testing.T) {
	cases := map[string]bool{
		"/d/harness/prod/ml":      true, // isolated <dataDir>/ml fallback
		"/home/u/.kenaz/ml/prod":  true, // ratified shared root (A5)
		"/home/u/.kenaz/ml/dev":   true,
		"/home/u/.kenaz/ml/test":  true,
		"/home/u/.kenaz/ml/stage": false, // not an engine env
		"/home/u/.kenaz/ml":       true,  // base "ml"
		"/home/u":                 false,
		"/tmp/whatever":           false,
		"/home/u/other/prod":      false, // env-named dir not under ml/
	}
	for root, want := range cases {
		if got := ownsWholeRoot(root); got != want {
			t.Errorf("ownsWholeRoot(%q) = %v, want %v", root, got, want)
		}
	}
}

func TestManager_Uninstall_RatifiedSharedRootRemovedWhole(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kenaz", "ml", "prod")
	l := NewLayout(root)
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	mustWrite(t, filepath.Join(root, "models", "w.bin"), "w")
	m := NewManager(l, NewClient(unreachableBaseURL, nil), nil, "harness", "0.85.0")
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("shared root must be gone entirely (weights + config): %v", err)
	}
}

// TestManager_Uninstall_RefusesWhileKenazHoldsALiveLease: the root is
// shared; removing it would pull the engine out from under the other app.
func TestManager_Uninstall_RefusesWhileKenazHoldsALiveLease(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kenaz", "ml", "prod")
	l := NewLayout(root)
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	if err := AcquireOrRenewLease(l, "harness", os.Getpid(), "0.85.0"); err != nil {
		t.Fatal(err)
	}
	if err := AcquireOrRenewLease(l, "kenaz", os.Getpid(), "2.0.0"); err != nil { // live pid, fresh mtime
		t.Fatal(err)
	}
	stub := newStubSidecar()
	defer stub.Close()
	mustWrite(t, l.TokenFile(), "tok")
	m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.85.0")

	err := m.Uninstall(context.Background())
	if !errors.Is(err, ErrEngineInUse) {
		t.Fatalf("err = %v, want ErrEngineInUse", err)
	}
	if _, ok := m.Installed(); !ok {
		t.Fatal("the engine install must be left in place while another app uses it")
	}
	if _, serr := os.Stat(l.VersionDir("1.0.0")); serr != nil {
		t.Fatalf("engine files were removed: %v", serr)
	}
	if stub.shutdownCallCount() != 0 {
		t.Fatal("a shared engine must not be asked to stop while another client holds a lease")
	}
	if _, serr := os.Stat(l.LeaseFile("harness")); !os.IsNotExist(serr) {
		t.Fatal("our own lease must be released even when the uninstall is refused")
	}
	if _, serr := os.Stat(l.LeaseFile("kenaz")); serr != nil {
		t.Fatal("the other client's lease must never be touched")
	}
}

func TestManager_Uninstall_ProceedsWhenTheOtherLeaseIsStale(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kenaz", "ml", "prod")
	l := NewLayout(root)
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	if err := AcquireOrRenewLease(l, "kenaz", os.Getpid(), "2.0.0"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute) // far past the 90s rule: a crashed/quit client
	if err := os.Chtimes(l.LeaseFile("kenaz"), old, old); err != nil {
		t.Fatal(err)
	}
	m := NewManager(l, NewClient(unreachableBaseURL, nil), nil, "harness", "0.85.0")
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("a stale foreign lease must not block uninstall: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root should be gone: %v", err)
	}
}
