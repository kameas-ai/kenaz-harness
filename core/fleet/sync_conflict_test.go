package fleet

// sync_conflict_test.go — a stale write is surfaced as a conflict instead of
// being silently reverted, and a device whose clock is off does not upload.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func conflictOf(s *Syncer, cat SyncCategory) (bool, string) {
	for _, st := range s.Status() {
		if st.Category == cat {
			return st.Conflict, st.LastError
		}
	}
	return false, ""
}

// conflictRig: device B, enabled and confirmed, whose model_prefs edit
// collides with a write another device made with a clock 4 minutes fast
// (inside Fleet's 5-minute window, so Fleet stores it).
func conflictRig(t *testing.T, dir string) (*Syncer, *fakeFleetSync, *recordingApplier, string, string) {
	t.Helper()
	fake, base := startFake(t)
	var open atomic.Bool
	open.Store(true)
	app := &recordingApplier{}
	b := newStateSyncer(t, base, dir, &open, `{"maxAgentTurns":7}`, app)
	if err := b.SetEnabled(context.Background(), SyncCategoryModelPrefs, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	other := `{"maxAgentTurns":42}`
	fake.put(SyncCategoryModelPrefs, time.Now().Add(4*time.Minute), other)
	return b, fake, app, other, base
}

// TestSyncConflict_StaleWriteSurfacedNotSilentlyReverted is the regression:
// before the fix Push answered 409 by pulling and applying the other
// device's write and returning nil, so B's newer edit vanished.
func TestSyncConflict_StaleWriteSurfacedNotSilentlyReverted(t *testing.T) {
	b, fake, app, other, _ := conflictRig(t, "")
	ctx := context.Background()
	puts := fake.countMethod(http.MethodPut)

	if err := b.Push(ctx, SyncCategoryModelPrefs); !errors.Is(err, ErrSyncConflict) {
		t.Fatalf("Push on 409 = %v, want ErrSyncConflict", err)
	}
	if n := app.seen.Load(); n != 0 {
		t.Fatalf("the other device's write was applied %d time(s) without asking", n)
	}
	conflict, msg := conflictOf(b, SyncCategoryModelPrefs)
	if !conflict || !strings.Contains(msg, "Another device changed") {
		t.Fatalf("Status conflict=%v LastError=%q, want the conflict surfaced", conflict, msg)
	}
	// The poll loop's pull and automatic pushes leave it alone.
	if err := b.Pull(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("auto Pull: %v", err)
	}
	if err := b.Push(ctx, SyncCategoryModelPrefs); !errors.Is(err, ErrSyncConflict) {
		t.Fatalf("auto Push while in conflict = %v, want ErrSyncConflict", err)
	}
	if n := app.seen.Load(); n != 0 {
		t.Fatal("auto Pull resolved the conflict behind the user's back")
	}
	if n := fake.countMethod(http.MethodPut); n != puts+1 {
		t.Fatalf("PUTs = %d, want exactly one more (the rejected one)", n-puts)
	}
	if row, _ := fake.get(SyncCategoryModelPrefs); string(row.Payload) != other {
		t.Fatalf("Fleet row changed to %s during the conflict", row.Payload)
	}
}

// TestSyncConflict_ForcePushKeepsThisDevice: the user's Push resolves the
// conflict for this device, stamped above Fleet's row.
func TestSyncConflict_ForcePushKeepsThisDevice(t *testing.T) {
	b, fake, _, _, _ := conflictRig(t, "")
	ctx := context.Background()
	_ = b.Push(ctx, SyncCategoryModelPrefs)
	if err := b.ForcePush(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("ForcePush: %v", err)
	}
	if row, _ := fake.get(SyncCategoryModelPrefs); string(row.Payload) != `{"maxAgentTurns":7}` {
		t.Fatalf("Fleet row = %s, want this device's", row.Payload)
	}
	if conflict, _ := conflictOf(b, SyncCategoryModelPrefs); conflict {
		t.Error("conflict not cleared by ForcePush")
	}
}

// TestSyncConflict_ForcePullTakesOtherDevice: the user's Pull resolves the
// conflict for the other device.
func TestSyncConflict_ForcePullTakesOtherDevice(t *testing.T) {
	b, _, app, other, _ := conflictRig(t, "")
	ctx := context.Background()
	_ = b.Push(ctx, SyncCategoryModelPrefs)
	if err := b.ForcePull(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("ForcePull: %v", err)
	}
	if got := app.snapshot(); len(got) != 1 || got[0] != other {
		t.Fatalf("applied %v, want %s", got, other)
	}
	if conflict, _ := conflictOf(b, SyncCategoryModelPrefs); conflict {
		t.Error("conflict not cleared by ForcePull")
	}
}

// TestSyncConflict_SurvivesRestart: a restart does not quietly resolve a
// conflict (the next poll would otherwise apply the other device's write).
func TestSyncConflict_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	b, _, _, _, base := conflictRig(t, dir)
	_ = b.Push(context.Background(), SyncCategoryModelPrefs)
	b.Stop()

	var open atomic.Bool
	open.Store(true)
	app := &recordingApplier{}
	b2 := newStateSyncer(t, base, dir, &open, `{"maxAgentTurns":7}`, app)
	if conflict, _ := conflictOf(b2, SyncCategoryModelPrefs); !conflict {
		t.Fatal("conflict lost across restart")
	}
	_ = b2.RestoreEnabled(SyncCategoryModelPrefs, true)
	if err := b2.Pull(context.Background(), SyncCategoryModelPrefs); err != nil {
		t.Fatal(err)
	}
	if app.seen.Load() != 0 {
		t.Fatal("restarted device applied the other write over the unresolved conflict")
	}
}

// TestSyncClock_RefusesPushWhenFourMinutesFast: Fleet would accept this
// device's stamps (inside its 5-minute window), and they would beat every
// genuinely newer edit from other devices for four minutes. Before the fix
// the PUT went through.
func TestSyncClock_RefusesPushWhenFourMinutesFast(t *testing.T) {
	fake, base := startFake(t)
	fake.mu.Lock()
	fake.now = func() time.Time { return time.Now().Add(-4 * time.Minute) } // Fleet is 4m behind us
	fake.mu.Unlock()
	var open atomic.Bool
	open.Store(true)
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, &recordingApplier{})
	err := s.SetEnabled(context.Background(), SyncCategoryModelPrefs, true)
	if !errors.Is(err, ErrSyncClockMismatch) {
		t.Fatalf("SetEnabled = %v, want ErrSyncClockMismatch", err)
	}
	if err := s.Push(context.Background(), SyncCategoryModelPrefs); !errors.Is(err, ErrSyncClockMismatch) {
		t.Fatalf("Push = %v, want ErrSyncClockMismatch", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("%d PUTs from a clock 4 minutes fast, want 0", n)
	}
	// Date has one-second resolution, so the measured skew is 4m0s or 4m1s.
	if _, msg := conflictOf(s, SyncCategoryModelPrefs); !strings.Contains(msg, "clock is 4m") || !strings.Contains(msg, "ahead of Fleet's") {
		t.Fatalf("LastError = %q, want the skew named", msg)
	}
}

// TestSyncClock_SmallSkewStillPushes: a few seconds of skew is normal.
func TestSyncClock_SmallSkewStillPushes(t *testing.T) {
	fake, base := startFake(t)
	fake.mu.Lock()
	fake.now = func() time.Time { return time.Now().Add(-5 * time.Second) }
	fake.mu.Unlock()
	var open atomic.Bool
	open.Store(true)
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, &recordingApplier{})
	if err := s.SetEnabled(context.Background(), SyncCategoryModelPrefs, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 1 {
		t.Fatalf("PUTs = %d, want 1", n)
	}
}

// TestSyncClock_StaleOffsetIsReprobedAfterThreeRefusals: a remembered
// offset that is wrong (a caching proxy served a stale Date) must not refuse
// uploads forever. The first syncSkewReprobeAfter refusals use the
// remembered offset without a request; the next check re-reads Fleet's
// clock, finds it in range, and uploads. Before the fix every push after
// the stale reading was refused, with no request to correct it.
func TestSyncClock_StaleOffsetIsReprobedAfterThreeRefusals(t *testing.T) {
	fake, base := startFake(t) // fake clock: correct
	var open atomic.Bool
	open.Store(true)
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, &recordingApplier{})
	if err := s.RestoreEnabled(SyncCategoryModelPrefs, true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// The stale reading: Fleet's clock "five minutes behind".
	s.clockMu.Lock()
	s.clockOffset, s.clockKnown = 5*time.Minute, true
	s.clockMu.Unlock()

	for i := 0; i < syncSkewReprobeAfter; i++ {
		if err := s.Push(ctx, SyncCategoryModelPrefs); !errors.Is(err, ErrSyncClockMismatch) {
			t.Fatalf("push %d = %v, want ErrSyncClockMismatch", i+1, err)
		}
	}
	if n := len(fake.snapshot()); n != 0 {
		t.Fatalf("%d requests before the re-probe threshold, want 0", n)
	}
	if err := s.Push(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("push after re-probe = %v, want an upload", err)
	}
	reqs := fake.snapshot()
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPut {
		t.Fatalf("requests = %+v, want one probe GET then the PUT", reqs)
	}
}

// TestSyncClock_GenuineSkewStillRefusedAfterReprobe: when the fresh reading
// confirms the skew, the push is still refused (one probe per check once
// past the threshold, never a PUT).
func TestSyncClock_GenuineSkewStillRefusedAfterReprobe(t *testing.T) {
	fake, base := startFake(t)
	fake.mu.Lock()
	fake.now = func() time.Time { return time.Now().Add(-5 * time.Minute) }
	fake.mu.Unlock()
	var open atomic.Bool
	open.Store(true)
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, &recordingApplier{})
	if err := s.RestoreEnabled(SyncCategoryModelPrefs, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < syncSkewReprobeAfter+2; i++ {
		if err := s.Push(context.Background(), SyncCategoryModelPrefs); !errors.Is(err, ErrSyncClockMismatch) {
			t.Fatalf("push %d = %v, want ErrSyncClockMismatch", i+1, err)
		}
	}
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("%d PUTs from a skewed clock", n)
	}
	// 1 initial probe (offset unknown) + 1 re-probe per check past the threshold.
	if n := fake.countMethod(http.MethodGet); n != 1+2 {
		t.Fatalf("GETs = %d, want 3", n)
	}
}
