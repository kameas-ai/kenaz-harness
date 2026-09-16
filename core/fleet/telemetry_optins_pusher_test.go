package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeOptInsFleet is a race-safe fake of the fleet
// PUT /api/v1/me/telemetry-opt-ins endpoint. Records every accepted PUT
// body; can be told to fail every request until told otherwise.
//
// Failure uses 403 rather than 5xx deliberately: Client.do retries 5xx up
// to 3 times with backoff internally (http.go), which would make a
// count-based "fail N times" fake flaky/slow and would silently absorb a
// failure the test means to observe. 403 is not retried, so one failing
// response reliably produces one failed Push call.
type fakeOptInsFleet struct {
	mu   sync.Mutex
	puts [][]TelemetryOptInItem
	fail bool
}

func (f *fakeOptInsFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/me/telemetry-opt-ins" || r.Method != http.MethodPut {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var body struct {
		Updates []TelemetryOptInItem `json:"updates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.puts = append(f.puts, body.Updates)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeOptInsFleet) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

func (f *fakeOptInsFleet) snapshot() [][]TelemetryOptInItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]TelemetryOptInItem, len(f.puts))
	for i, p := range f.puts {
		cp := make([]TelemetryOptInItem, len(p))
		copy(cp, p)
		out[i] = cp
	}
	return out
}

func requireItemsAllOptedIn(t *testing.T, items []TelemetryOptInItem, want bool) {
	t.Helper()
	if len(items) == 0 {
		t.Fatal("no items received")
	}
	for _, it := range items {
		if it.OptedIn != want {
			t.Errorf("class %q: OptedIn = %v, want %v", it.Class, it.OptedIn, want)
		}
	}
}

// TestTelemetryOptInPusher_Push_Full_SendsAllTrue: set tier full -> assert
// PUT /telemetry-opt-ins received the expected per-class updates.
func TestTelemetryOptInPusher_Push_Full_SendsAllTrue(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)

	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return client })
	if err := pusher.Push(context.Background(), ConsentFull); err != nil {
		t.Fatalf("Push(full): %v", err)
	}

	puts := fake.snapshot()
	if len(puts) != 1 {
		t.Fatalf("server received %d PUTs, want 1", len(puts))
	}
	requireItemsAllOptedIn(t, puts[0], true)
	if got, want := pusher.ConfirmedLevel(), ConsentFull; got != want {
		t.Errorf("ConfirmedLevel() = %q, want %q", got, want)
	}
}

// TestTelemetryOptInPusher_Push_None_SendsAllFalse: set tier none -> all
// false.
func TestTelemetryOptInPusher_Push_None_SendsAllFalse(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)

	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return client })
	// Start from full so we can observe none actively clearing it.
	if err := pusher.Push(context.Background(), ConsentFull); err != nil {
		t.Fatalf("Push(full): %v", err)
	}
	if err := pusher.Push(context.Background(), ConsentNone); err != nil {
		t.Fatalf("Push(none): %v", err)
	}

	puts := fake.snapshot()
	if len(puts) != 2 {
		t.Fatalf("server received %d PUTs, want 2", len(puts))
	}
	requireItemsAllOptedIn(t, puts[1], false)
}

// TestTelemetryOptInPusher_GateAdmission_EndToEnd exercises the full path:
// push a tier's vector through a fake fleet server, then feed the exact
// snapshot the server received into LogKindsAdmittedBy and assert it admits
// what the tier promises.
func TestTelemetryOptInPusher_GateAdmission_EndToEnd(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)

	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return client })

	if err := pusher.Push(context.Background(), ConsentFull); err != nil {
		t.Fatalf("Push(full): %v", err)
	}
	puts := fake.snapshot()
	admitted := LogKindsAdmittedBy(puts[len(puts)-1])
	if len(admitted) != len(logKindCeiling) {
		t.Errorf("full: admitted %d kinds, want %d (every ceiling kind)", len(admitted), len(logKindCeiling))
	}

	if err := pusher.Push(context.Background(), ConsentNone); err != nil {
		t.Fatalf("Push(none): %v", err)
	}
	puts = fake.snapshot()
	admittedNone := LogKindsAdmittedBy(puts[len(puts)-1])
	if len(admittedNone) != 0 {
		t.Errorf("none: admitted %d kinds, want 0", len(admittedNone))
	}
}

// TestTelemetryOptInPusher_Push_ClientNil_ReturnsErrFleetDisabled: no fleet
// client wired -> ErrFleetDisabled, no confirmed-state written.
func TestTelemetryOptInPusher_Push_ClientNil_ReturnsErrFleetDisabled(t *testing.T) {
	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return nil })
	err := pusher.Push(context.Background(), ConsentFull)
	if err != ErrFleetDisabled {
		t.Fatalf("Push() = %v, want ErrFleetDisabled", err)
	}
	if got := pusher.ConfirmedLevel(); got != "" {
		t.Errorf("ConfirmedLevel() = %q, want empty (nothing pushed)", got)
	}
}

// TestTelemetryOptInPusher_Push_FailureThenRetrySucceeds is the failure +
// retry test: the fleet write fails, then a subsequent Push (simulating
// "next tier change") succeeds once the server recovers.
func TestTelemetryOptInPusher_Push_FailureThenRetrySucceeds(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	fake.setFail(true)
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)

	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return client })

	if err := pusher.Push(context.Background(), ConsentFull); err == nil {
		t.Fatal("Push: want error from the first (failing) attempt")
	}
	if got := pusher.ConfirmedLevel(); got != "" {
		t.Errorf("ConfirmedLevel() after failed push = %q, want empty", got)
	}
	if !pusher.Pending(ConsentFull) {
		t.Error("Pending(full) = false, want true after a failed push")
	}

	// Server recovers; retry ("next tier change" or an explicit re-attempt).
	fake.setFail(false)
	if err := pusher.Push(context.Background(), ConsentFull); err != nil {
		t.Fatalf("Push retry: %v", err)
	}
	if got := pusher.ConfirmedLevel(); got != ConsentFull {
		t.Errorf("ConfirmedLevel() after retry = %q, want full", got)
	}

	puts := fake.snapshot()
	if len(puts) != 1 {
		t.Fatalf("server recorded %d successful PUTs, want 1 (first attempt failed before being recorded)", len(puts))
	}
}

// TestTelemetryOptInPusher_Reconcile_RetriesAcrossRestart is the "next app
// start" retry path: a failed push's mismatch survives into a FRESH
// TelemetryOptInPusher instance pointed at the same dataDir (simulating a
// process restart), and Reconcile picks it up.
func TestTelemetryOptInPusher_Reconcile_RetriesAcrossRestart(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	fake.setFail(true)
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)
	dataDir := t.TempDir()

	first := NewTelemetryOptInPusher(dataDir, func() *Client { return client })
	if err := first.Push(context.Background(), ConsentFull); err == nil {
		t.Fatal("Push: want error from the first (failing) attempt")
	}

	// Server recovers before the "restart".
	fake.setFail(false)

	// Simulate a process restart: brand-new pusher, same dataDir, no
	// in-memory state carried over.
	restarted := NewTelemetryOptInPusher(dataDir, func() *Client { return client })
	if got := restarted.ConfirmedLevel(); got != "" {
		t.Fatalf("restarted.ConfirmedLevel() = %q, want empty (nothing was ever confirmed)", got)
	}
	if err := restarted.Reconcile(context.Background(), ConsentFull); err != nil {
		t.Fatalf("Reconcile after restart: %v", err)
	}
	if got := restarted.ConfirmedLevel(); got != ConsentFull {
		t.Errorf("restarted.ConfirmedLevel() = %q, want full", got)
	}

	puts := fake.snapshot()
	if len(puts) != 1 {
		t.Fatalf("server recorded %d successful PUTs, want 1", len(puts))
	}
	requireItemsAllOptedIn(t, puts[0], true)
}

// TestTelemetryOptInPusher_Reconcile_NoOpWhenAlreadyConfirmed asserts
// Reconcile makes no network call when the confirmed level already matches.
func TestTelemetryOptInPusher_Reconcile_NoOpWhenAlreadyConfirmed(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)

	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return client })
	if err := pusher.Push(context.Background(), ConsentAggregate); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if err := pusher.Reconcile(context.Background(), ConsentAggregate); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if puts := fake.snapshot(); len(puts) != 1 {
		t.Fatalf("server received %d PUTs, want 1 (Reconcile should be a no-op when confirmed already matches)", len(puts))
	}
}

// TestTelemetryOptInPusher_OnPushed_FiresWithVector asserts the OnPushed
// callback fires exactly once per successful push, with the pushed vector.
func TestTelemetryOptInPusher_OnPushed_FiresWithVector(t *testing.T) {
	if !tryInstallFakeTokens(t) {
		t.Skip("OS keychain unavailable")
	}
	fake := &fakeOptInsFleet{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := newTestClient(t, srv)

	pusher := NewTelemetryOptInPusher(t.TempDir(), func() *Client { return client })

	var mu sync.Mutex
	var received [][]TelemetryOptInItem
	pusher.SetOnPushed(func(items []TelemetryOptInItem) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, items)
	})

	if err := pusher.Push(context.Background(), ConsentFull); err != nil {
		t.Fatalf("Push: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("OnPushed fired %d times, want 1", len(received))
	}
	requireItemsAllOptedIn(t, received[0], true)
}
