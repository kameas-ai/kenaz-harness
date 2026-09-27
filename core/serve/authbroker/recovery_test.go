package authbroker_test

// recovery_test.go — the session is recoverable.
//
// Before this, two states were terminal for the life of the process: an
// anonymous boot (no goroutine was ever started) and a host sign-out (the
// renewal loop returned on the first 401). Either way, signing in on the host
// did nothing for a workbench that was already running.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

func waitForTimer(t *testing.T, tf *timerFactory) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tf.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if tf.count() == 0 {
		t.Fatal("the session loop never scheduled a timer — no goroutine is running")
	}
}

func waitForSubscriberState(t *testing.T, s *authbroker.Session, ch <-chan struct{}, want authbroker.State) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for s.State() != want {
		select {
		case <-ch:
		case <-timeout:
			t.Fatalf("timed out waiting for %v; current=%v", want, s.State())
		}
	}
}

func TestSession_AnonymousBoot_RecoversWhenTheHostSignsInLater(t *testing.T) {
	fb := newFakeBroker(t)
	fb.setMode(brokerError503) // host is anonymous: the broker has no token to give
	tf := &timerFactory{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := authbroker.NewSession(ctx, authbroker.Config{
		BrokerAddr:  fb.addr(),
		BrokerToken: "test-broker-tok",
		SignedIn:    false, // anonymous at workbench start
	}, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
	)
	sub := s.Subscribe()
	if s.State() != authbroker.StateAnonymous {
		t.Fatalf("boot state = %v, want anonymous", s.State())
	}
	waitForTimer(t, tf)

	// A probe while the host is still anonymous changes nothing.
	tf.latest().fire()
	waitForCalls(t, fb, 1)
	if s.State() != authbroker.StateAnonymous || s.AccessToken() != "" {
		t.Fatalf("after a fruitless probe: state=%v token-set=%v", s.State(), s.AccessToken() != "")
	}

	// The user signs in on the host.
	fb.setMode(brokerOK)
	tf.latest().fire()
	waitForSubscriberState(t, s, sub, authbroker.StateSignedIn)
	if s.AccessToken() == "" {
		t.Error("recovered to signed_in without an access token")
	}
}

func TestSession_AnonymousBootWithoutBrokerConfig_StartsNoLoop(t *testing.T) {
	tf := &timerFactory{}
	s := authbroker.NewSession(context.Background(), authbroker.Config{SignedIn: false}, nil,
		authbroker.WithTimerFactory(tf.new))
	time.Sleep(30 * time.Millisecond)
	if tf.count() != 0 {
		t.Error("a bare local run (no broker address) must not start a probe loop")
	}
	if s.State() != authbroker.StateAnonymous {
		t.Errorf("state = %v", s.State())
	}
}

func TestSession_SignOut_IsNotTerminal_RecoversOnLaterSignIn(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}
	var ledger []string
	ledgerCh := make(chan string, 8)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := authbroker.NewSession(ctx, authbroker.Config{
		BrokerAddr:      fb.addr(),
		BrokerToken:     "test-broker-tok",
		SignedIn:        true,
		SeedAccessToken: "seed-tok",
	}, nil,
		authbroker.WithHTTPClient(fb.srv.Client()),
		authbroker.WithTimerFactory(tf.new),
		authbroker.WithLedgerEmit(func(e string) { ledgerCh <- e }),
	)
	sub := s.Subscribe()
	waitForTimer(t, tf)

	// Host signs out.
	fb.setMode(brokerSignedOut)
	tf.latest().fire()
	waitForSubscriberState(t, s, sub, authbroker.StateSignedOut)
	if s.AccessToken() != "" {
		t.Fatal("token not cleared on sign-out")
	}

	// Further 401 probes must not re-emit the ledger event or flap state.
	tf.latest().fire()
	waitForCalls(t, fb, 2)

	// Host signs back in — possibly as someone else; the broker simply hands
	// out whatever session the host now has.
	fb.setMode(brokerOK)
	tf.latest().fire()
	waitForSubscriberState(t, s, sub, authbroker.StateSignedIn)
	if s.AccessToken() == "" {
		t.Error("recovered to signed_in without an access token")
	}

	close(ledgerCh)
	for e := range ledgerCh {
		ledger = append(ledger, e)
	}
	if len(ledger) != 1 || ledger[0] != "session.signed_out" {
		t.Errorf("ledger = %v, want exactly one session.signed_out for one sign-out", ledger)
	}
}

func TestSession_Subscribe_EverySubscriberIsNotified(t *testing.T) {
	fb := newFakeBroker(t)
	tf := &timerFactory{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := authbroker.NewSession(ctx, authbroker.Config{
		BrokerAddr: fb.addr(), BrokerToken: "t", SignedIn: true, SeedAccessToken: "seed",
	}, nil, authbroker.WithHTTPClient(fb.srv.Client()), authbroker.WithTimerFactory(tf.new))

	a, b := s.Subscribe(), s.Subscribe()
	legacy := s.StateChangedCh()
	waitForTimer(t, tf)

	tf.latest().fire() // a plain renewal: state unchanged, token new

	for name, ch := range map[string]<-chan struct{}{"a": a, "b": b, "legacy": legacy} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Errorf("subscriber %s was not notified of the renewal; a second consumer would starve the first", name)
		}
	}
}

func waitForCalls(t *testing.T, fb *fakeBroker, n int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fb.callCnt.Load() >= n {
			// Let the loop finish handling the response before the caller
			// inspects state.
			time.Sleep(20 * time.Millisecond)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("broker saw %d call(s), want >= %d", fb.callCnt.Load(), n)
}
