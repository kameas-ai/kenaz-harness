package tasks

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestIsProcessAlive_SelfPID verifies that the current process's PID
// is reported as alive (FR-003: MarkCrashed does a real PID-liveness check).
func TestIsProcessAlive_SelfPID(t *testing.T) {
	self := os.Getpid()
	if !IsProcessAlive(self) {
		t.Errorf("IsProcessAlive(%d) = false, want true (current process should be alive)", self)
	}
}

// TestIsProcessAlive_ZeroPID verifies that PID 0 is treated as dead
// (sub-agent tasks that never recorded a PID).
func TestIsProcessAlive_ZeroPID(t *testing.T) {
	if IsProcessAlive(0) {
		t.Error("IsProcessAlive(0) = true, want false (pid 0 means unknown)")
	}
}

// TestIsProcessAlive_NegativePID verifies that a negative PID is treated as dead.
func TestIsProcessAlive_NegativePID(t *testing.T) {
	if IsProcessAlive(-1) {
		t.Error("IsProcessAlive(-1) = true, want false")
	}
}

// TestRecoverOrphansWithPIDCheck_MarksDeadTasks verifies that
// RecoverOrphansWithPIDCheck calls MarkCrashed on the store (FR-003).
func TestRecoverOrphansWithPIDCheck_MarksDeadTasks(t *testing.T) {
	var markCrashedCalled bool
	store := &fakeTaskStore{
		markCrashedFn: func(_ context.Context) (int, error) {
			markCrashedCalled = true
			return 2, nil
		},
	}
	reg := NewRegistry(Options{Store: store})
	RecoverOrphansWithPIDCheck(context.Background(), reg, "")
	if !markCrashedCalled {
		t.Error("RecoverOrphansWithPIDCheck did not call MarkCrashed on the store")
	}
}

// TestRecoverOrphansWithPIDCheck_NilRegistry is a nil-safety check.
func TestRecoverOrphansWithPIDCheck_NilRegistry(t *testing.T) {
	// Must not panic.
	n := RecoverOrphansWithPIDCheck(context.Background(), nil, "")
	if n != 0 {
		t.Errorf("expected 0 alive tasks with nil registry, got %d", n)
	}
}

// fakeTaskStore satisfies SQLStore for tests.
type fakeTaskStore struct {
	markCrashedFn func(ctx context.Context) (int, error)
}

func (f *fakeTaskStore) Insert(_ context.Context, _ Task) error {
	return nil
}

func (f *fakeTaskStore) UpdateStatus(_ context.Context, _ string, _ string, _ int, _ time.Time) error {
	return nil
}

func (f *fakeTaskStore) MarkCrashed(ctx context.Context) (int, error) {
	if f.markCrashedFn != nil {
		return f.markCrashedFn(ctx)
	}
	return 0, nil
}
