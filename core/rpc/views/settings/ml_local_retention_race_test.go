package settings

import (
	"context"
	"sync"
	"testing"
)

// interleavingStore is a real FileStore whose LoadAll runs a hook after the
// read, so a test can land another writer between a caller's read and its
// write. Update is the embedded FileStore's own (it never calls LoadAll).
type interleavingStore struct {
	*FileStore
	once  sync.Once
	after func()
}

func (s *interleavingStore) LoadAll() (Settings, error) {
	cur, err := s.FileStore.LoadAll()
	if s.after != nil {
		s.once.Do(s.after)
	}
	return cur, err
}

// A retention save must not write back a stale copy over a model-call
// opt-out that lands between its read and its write (legal D82: a revert
// would resume sharing at the next start without the opt-in being shown).
func TestSetMLLocalActivityRetentionDays_DoesNotRevertConcurrentOptOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other := NewAPI(fs)
	if err := other.SetModelCallTelemetry(ctx, ModelCallTelemetrySettings{StatsPullEnabled: true, ShareWithFleet: true}); err != nil {
		t.Fatal(err)
	}

	st := &interleavingStore{FileStore: fs}
	st.after = func() {
		if err := other.SetModelCallTelemetry(ctx, ModelCallTelemetrySettings{StatsPullEnabled: true}); err != nil {
			t.Error(err)
		}
	}
	a := NewAPI(st)
	if err := a.SetMLLocalActivityRetentionDays(ctx, 30); err != nil {
		t.Fatal(err)
	}
	// Run the opt-out now if the retention save never read through LoadAll.
	st.once.Do(st.after)

	got, err := fs.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShareModelCallTelemetryWithFleet {
		t.Fatal("retention save reverted a concurrent model-call opt-out")
	}
	if got.MLLocalActivityRetentionDays != 30 {
		t.Fatalf("retention = %d, want 30", got.MLLocalActivityRetentionDays)
	}
}

// The same under real concurrency, for -race: every opt-out and every
// retention save lands, whatever the order.
func TestSetMLLocalActivityRetentionDays_ConcurrentWithModelCallTelemetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := NewAPI(fs)
	if err := a.SetModelCallTelemetry(ctx, ModelCallTelemetrySettings{StatsPullEnabled: true, ShareWithFleet: true}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := a.SetMLLocalActivityRetentionDays(ctx, 10+i); err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		if err := a.SetModelCallTelemetry(ctx, ModelCallTelemetrySettings{StatsPullEnabled: true}); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	got, err := fs.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShareModelCallTelemetryWithFleet {
		t.Fatal("a retention save reverted the opt-out")
	}
	if got.MLLocalActivityRetentionDays != 29 {
		t.Fatalf("retention = %d, want the last save (29)", got.MLLocalActivityRetentionDays)
	}
}
