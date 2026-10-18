package mlsidecar

import (
	"testing"
	"time"
)

// TestDemandProbe_Close_CancelsInflightAndStopsFutureDemand: app shutdown
// must not wait out a 30s startup poll, and nothing may be scheduled after.
func TestDemandProbe_Close_CancelsInflightAndStopsFutureDemand(t *testing.T) {
	l, _ := installedLayout(t)
	entered := make(chan struct{})
	spawner := &fakeSpawner{onSpawn: func() { close(entered) }}
	// Nothing ever answers: the Manager will sit in its startup poll.
	m := NewManager(l, NewClient(unreachableBaseURL, nil), spawner, "harness", "0.85.0")
	m.StartupWait = 30 * time.Second
	m.StartupPoll = 5 * time.Millisecond
	p := &DemandProbe{M: m}
	p.Healthy()
	<-entered
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the in-flight startup wait")
	}
	spawner2 := &fakeSpawner{}
	m.Spawner = spawner2
	p.Healthy()
	p.Wait()
	if spawner2.called {
		t.Fatal("a closed probe scheduled a new Ensure")
	}
	p.Close() // idempotent
}
