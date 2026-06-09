package core_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/event"
)

// TestNew_RejectsEmptyDataDir pins the basic precondition: Core
// cannot be constructed without a DataDir, because every lazily-
// initialized subsystem derives paths from it.
func TestNew_RejectsEmptyDataDir(t *testing.T) {
	if _, err := core.New(core.Options{}); err == nil {
		t.Fatalf("New with empty DataDir returned nil error")
	}
}

// TestNew_AcceptsDataDir confirms the happy path returns a non-nil
// Core when DataDir is set.
func TestNew_AcceptsDataDir(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatalf("New returned nil Core")
	}
	if c.DataDir() == "" {
		t.Fatalf("DataDir() = empty after explicit set")
	}
}

// TestBundleCache_LazilyConstructsUnderDataDir asserts the SEAM 6
// contract: BundleCache constructs the CAS rooted at <DataDir>/cache,
// the directory is created on disk, and concurrent callers see the
// same CAS instance.
func TestBundleCache_LazilyConstructsUnderDataDir(t *testing.T) {
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cas, err := c.BundleCache()
	if err != nil {
		t.Fatalf("BundleCache: %v", err)
	}
	if cas == nil {
		t.Fatalf("BundleCache returned nil")
	}
	wantRoot := filepath.Join(dataDir, "cache")
	if cas.Root() != wantRoot {
		t.Fatalf("CAS Root=%q want %q", cas.Root(), wantRoot)
	}

	// Second call must return the same instance — consumers rely on
	// shared cache state across the harness.
	cas2, err := c.BundleCache()
	if err != nil {
		t.Fatalf("BundleCache#2: %v", err)
	}
	if cas2 != cas {
		t.Fatalf("BundleCache returned a different instance on second call")
	}
}

// TestSubsystems_InjectionRespected pins that pre-constructed
// subsystems supplied through Options.Subsystems are wired onto Core
// directly, bypassing lazy defaults.
func TestSubsystems_InjectionRespected(t *testing.T) {
	fakeLog := &fakeEventLog{}
	c, err := core.New(core.Options{
		DataDir:    t.TempDir(),
		Subsystems: core.Subsystems{Events: fakeLog},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Events != fakeLog {
		t.Fatalf("Core.Events did not pick up injected subsystem")
	}

	// Shutdown must invoke Close on the wired event log exactly once.
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if fakeLog.closeCalls != 1 {
		t.Fatalf("Events.Close calls=%d want 1", fakeLog.closeCalls)
	}
}

// TestStart_NoOpWithoutScheduler asserts Start succeeds when no
// scheduler is wired. Embedders that don't run jobs must not be
// forced to provide one.
func TestStart_NoOpWithoutScheduler(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// TestStart_WiresTelemetry confirms the telemetry init path runs
// alongside Start when DataDir + storage are available, and Telemetry()
// surfaces a non-nil instance afterwards. Shutdown must drain the
// telemetry providers before the DB closes; otherwise the SDK's flush
// would write into a closed handle.
func TestStart_WiresTelemetry(t *testing.T) {
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir, BuildVersion: "test-1.0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tel := c.Telemetry()
	if tel == nil {
		t.Fatalf("Telemetry() returned nil after Start")
	}
	if tel.InstanceID == "" {
		t.Fatalf("InstanceID empty")
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if c.Telemetry() != nil {
		t.Errorf("Telemetry() not cleared after Shutdown")
	}
}

type fakeEventLog struct {
	closeCalls int
}

func (f *fakeEventLog) Close() error {
	f.closeCalls++
	return nil
}

// Compile-time assertion: fakeEventLog satisfies event.Log.
var _ event.Log = (*fakeEventLog)(nil)
