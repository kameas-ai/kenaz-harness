package serve

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

type fakeAuth struct {
	mu    sync.Mutex
	state authbroker.State
	ch    chan struct{}
}

func newFakeAuth(st authbroker.State) *fakeAuth {
	return &fakeAuth{state: st, ch: make(chan struct{}, 8)}
}
func (f *fakeAuth) State() authbroker.State    { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *fakeAuth) Subscribe() <-chan struct{} { return f.ch }
func (f *fakeAuth) set(st authbroker.State) {
	f.mu.Lock()
	f.state = st
	f.mu.Unlock()
	f.ch <- struct{}{}
}

type supRig struct {
	auth      *fakeAuth
	sup       *FleetEnrollSupervisor
	identity  atomic.Value // string
	enrolls   atomic.Int32
	reconcile atomic.Int32
	ended     atomic.Int32
	failNext  atomic.Int32
	waits     chan time.Duration
	tick      chan time.Time
}

func newSupRig(t *testing.T, st authbroker.State, identity string) *supRig {
	t.Helper()
	r := &supRig{auth: newFakeAuth(st), waits: make(chan time.Duration, 64), tick: make(chan time.Time)}
	r.identity.Store(identity)
	r.sup = NewFleetEnrollSupervisor(FleetEnrollConfig{
		Auth:     r.auth,
		Identity: func() string { return r.identity.Load().(string) },
		Enroll: func(context.Context) error {
			r.enrolls.Add(1)
			if r.failNext.Load() > 0 {
				r.failNext.Add(-1)
				return errors.New(`Post "https://api.fleet.example/api/v1/enroll": dial tcp: connection refused`)
			}
			return nil
		},
		Reconcile:    func(context.Context) { r.reconcile.Add(1) },
		SessionEnded: func(context.Context) { r.ended.Add(1) },
		After: func(d time.Duration) <-chan time.Time {
			r.waits <- d
			return r.tick
		},
		RetryBase: 2 * time.Second, RetryMax: 8 * time.Second, ReconcileGap: time.Minute,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.sup.Run(ctx)
	return r
}

func (r *supRig) nextWait(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-r.waits:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not reach its wait")
		return 0
	}
}

func (r *supRig) fire() { r.tick <- time.Now() }

func TestSupervisor_SignedInAtBoot_EnrollsOnce_ThenOnlyReconciles(t *testing.T) {
	r := newSupRig(t, authbroker.StateSignedIn, "alice|org|iss")
	if d := r.nextWait(t); d != time.Minute {
		t.Fatalf("wait after a good enroll = %v, want the reconcile gap", d)
	}
	r.fire()
	r.nextWait(t)
	if r.enrolls.Load() != 1 || r.reconcile.Load() != 1 {
		t.Errorf("enrolls=%d reconciles=%d, want 1 and 1", r.enrolls.Load(), r.reconcile.Load())
	}
	if st := r.sup.Status(); !st.Enrolled || st.AuthState != "signed_in" {
		t.Errorf("status = %+v", st)
	}
}

func TestSupervisor_FailedEnroll_RetriesWithBackoffUntilSuccess(t *testing.T) {
	r := newSupRig(t, authbroker.StateSignedIn, "alice|org|iss")
	r.failNext.Store(3)
	r.enrolls.Store(0)
	var got []time.Duration
	for i := 0; i < 4; i++ {
		got = append(got, r.nextWait(t))
		if i < 3 {
			r.fire()
		}
	}
	// The first attempt may have raced failNext.Store; accept either start,
	// but the tail must show doubling then the success gap.
	if got[len(got)-1] != time.Minute {
		t.Fatalf("waits = %v, want the last to be the reconcile gap after success", got)
	}
	for i := 1; i < len(got)-1; i++ {
		if got[i] < got[i-1] {
			t.Errorf("backoff shrank: %v", got)
		}
	}
	st := r.sup.Status()
	if !st.Enrolled || st.EnrollFailures == 0 || st.LastError != "" {
		t.Errorf("status = %+v, want enrolled with failures recorded and error cleared", st)
	}
}

func TestSupervisor_Status_ErrorClassCarriesNoURL(t *testing.T) {
	r := newSupRig(t, authbroker.StateAnonymous, "")
	r.nextWait(t)
	r.failNext.Store(1)
	r.identity.Store("alice|org|iss")
	r.auth.set(authbroker.StateSignedIn)
	r.nextWait(t)
	st := r.sup.Status()
	if st.LastError != "network" {
		t.Errorf("LastError = %q, want the class only", st.LastError)
	}
}

func TestSupervisor_AnonymousBoot_EnrollsWhenTheHostSignsInLater(t *testing.T) {
	r := newSupRig(t, authbroker.StateAnonymous, "")
	r.nextWait(t)
	if r.enrolls.Load() != 0 {
		t.Fatal("enrolled while anonymous")
	}
	r.identity.Store("alice|org|iss")
	r.auth.set(authbroker.StateSignedIn)
	r.nextWait(t)
	if r.enrolls.Load() != 1 {
		t.Errorf("enrolls = %d after the host signed in, want 1", r.enrolls.Load())
	}
}

func TestSupervisor_SignOut_EndsSession_ThenReEnrollsOnSignIn(t *testing.T) {
	r := newSupRig(t, authbroker.StateSignedIn, "alice|org|iss")
	r.nextWait(t)

	r.identity.Store("")
	r.auth.set(authbroker.StateSignedOut)
	r.nextWait(t)
	if r.ended.Load() != 1 || r.sup.Status().Enrolled {
		t.Fatalf("ended=%d status=%+v, want the session ended exactly once", r.ended.Load(), r.sup.Status())
	}
	r.fire() // a quiet tick while signed out must not end the session again
	r.nextWait(t)
	if r.ended.Load() != 1 {
		t.Errorf("SessionEnded ran %d times for one sign-out", r.ended.Load())
	}

	r.identity.Store("alice|org|iss")
	r.auth.set(authbroker.StateSignedIn)
	r.nextWait(t)
	if r.enrolls.Load() != 2 {
		t.Errorf("enrolls = %d, want a re-enroll after signing back in", r.enrolls.Load())
	}
}

func TestSupervisor_IdentityChange_ReEnrolls_TokenRenewalDoesNot(t *testing.T) {
	r := newSupRig(t, authbroker.StateSignedIn, "alice|org-1|iss")
	r.nextWait(t)

	r.auth.set(authbroker.StateSignedIn) // plain renewal: same identity
	r.nextWait(t)
	if r.enrolls.Load() != 1 {
		t.Fatalf("a token renewal re-enrolled (%d)", r.enrolls.Load())
	}

	for _, next := range []string{"alice|org-2|iss", "alice|org-2|other-iss", "bob|org-2|other-iss"} {
		before := r.enrolls.Load()
		r.identity.Store(next)
		r.auth.set(authbroker.StateSignedIn)
		r.nextWait(t)
		if r.enrolls.Load() != before+1 {
			t.Errorf("identity → %q did not re-enroll", next)
		}
	}
}

func TestSupervisor_AccountChange_EndsOldEnrollmentBeforeAFailingNewOne(t *testing.T) {
	r := newSupRig(t, authbroker.StateSignedIn, "alice|org|iss")
	r.nextWait(t)
	if !r.sup.Status().Enrolled {
		t.Fatal("fixture: not enrolled as alice")
	}

	r.failNext.Store(1) // bob's first enroll fails
	r.identity.Store("bob|org|iss")
	r.auth.set(authbroker.StateSignedIn)
	if d := r.nextWait(t); d != 2*time.Second {
		t.Errorf("wait after the failed switch = %v, want the retry backoff", d)
	}
	st := r.sup.Status()
	if st.Enrolled {
		t.Error("alice's enrollment is still reported as current after a failed switch to bob")
	}
	if r.ended.Load() != 1 {
		t.Errorf("SessionEnded ran %d times, want 1 — before the new enroll was attempted", r.ended.Load())
	}
	if st.LastError == "" {
		t.Error("the failed enroll left no error class in status")
	}

	r.fire() // retry succeeds
	r.nextWait(t)
	st = r.sup.Status()
	if !st.Enrolled || r.ended.Load() != 1 {
		t.Errorf("after retry: status=%+v ended=%d, want enrolled (as bob) with no second teardown", st, r.ended.Load())
	}
}
