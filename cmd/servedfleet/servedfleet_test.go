package servedfleet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/serve"
	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

// fakeAuth is a signed-in host broker session whose subscribers can be kicked.
type fakeAuth struct{ ch chan struct{} }

func (a *fakeAuth) State() authbroker.State    { return authbroker.StateSignedIn }
func (a *fakeAuth) Subscribe() <-chan struct{} { return a.ch }

// fakeSettings models the settings API's node_removed gate: enroll refuses
// while blocked; FleetHostIdentityPresented lifts the block only for an
// identity other than the removed one (the real rule is pinned in
// core/rpc/views/settings TestNodeRemoved_ServedHostNewIdentityClears).
type fakeSettings struct {
	mu        sync.Mutex
	removedAs string
	blocked   bool
	presented []string
	enrolls   int
}

func (f *fakeSettings) FleetHostIdentityPresented(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.presented = append(f.presented, id)
	if f.blocked && id != "" && id != f.removedAs {
		f.blocked = false
		return true
	}
	return false
}

func (f *fakeSettings) FleetRefreshIdentity(context.Context) (settings.FleetIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocked {
		return settings.FleetIdentity{}, fleet.ErrNodeRemoved
	}
	f.enrolls++
	return settings.FleetIdentity{}, nil
}

func (f *fakeSettings) snapshot() (bool, int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blocked, f.enrolls, append([]string(nil), f.presented...)
}

// TestEnrollFunc_SupervisorLiftsNodeRemovedOnNewHostIdentity drives the real
// serve.FleetEnrollSupervisor with servedfleet's Enroll step: while the host
// presents the removed account the supervisor keeps failing (blocked); when
// the host switches account the step presents the new identity first and
// the enroll succeeds. Mutation: drop the FleetHostIdentityPresented call in
// enrollFunc and the supervisor never enrolls.
func TestEnrollFunc_SupervisorLiftsNodeRemovedOnNewHostIdentity(t *testing.T) {
	var idMu sync.Mutex
	current := "alice|org|iss"
	identity := func() string { idMu.Lock(); defer idMu.Unlock(); return current }
	fs := &fakeSettings{removedAs: "alice|org|iss", blocked: true}
	auth := &fakeAuth{ch: make(chan struct{}, 1)}
	tick := make(chan time.Time)
	sup := serve.NewFleetEnrollSupervisor(serve.FleetEnrollConfig{
		Auth:     auth,
		Identity: identity,
		Enroll:   enrollFunc(fs, identity),
		After:    func(time.Duration) <-chan time.Time { return tick },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s (status %+v)", what, sup.Status())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("first blocked attempt", func() bool { return sup.Status().EnrollAttempts >= 1 })
	tick <- time.Now() // retry with the same account: still blocked
	waitFor("second blocked attempt", func() bool { return sup.Status().EnrollAttempts >= 2 })
	if blocked, n, _ := fs.snapshot(); !blocked || n != 0 {
		t.Fatalf("same account: blocked=%v enrolls=%d", blocked, n)
	}

	idMu.Lock()
	current = "bob|org|iss"
	idMu.Unlock()
	auth.ch <- struct{}{}
	waitFor("enrolled under the new identity", func() bool { return sup.Status().Enrolled })
	blocked, n, presented := fs.snapshot()
	if blocked || n != 1 {
		t.Fatalf("after new identity: blocked=%v enrolls=%d", blocked, n)
	}
	if presented[len(presented)-1] != "bob|org|iss" {
		t.Fatalf("presented identities = %v", presented)
	}
}
