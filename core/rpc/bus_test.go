package rpc

import (
	"context"
	"testing"
	"time"
)

func TestEventBus_PubSub(t *testing.T) {
	bus := NewEventBus()

	ch, cancel := bus.Subscribe(0)
	defer cancel()

	bus.Publish("topic.a", "payload-a")
	bus.Publish("topic.b", "payload-b")

	deadline := time.After(100 * time.Millisecond)
	received := make([]BusEvent, 0, 2)
	for len(received) < 2 {
		select {
		case ev := <-ch:
			received = append(received, ev)
		case <-deadline:
			t.Fatalf("timed out waiting for events; got %d/2", len(received))
		}
	}
	if received[0].Topic != "topic.a" || received[0].Payload != "payload-a" {
		t.Errorf("event 0: got {%s %v}, want {topic.a payload-a}", received[0].Topic, received[0].Payload)
	}
	if received[1].Topic != "topic.b" || received[1].Payload != "payload-b" {
		t.Errorf("event 1: got {%s %v}, want {topic.b payload-b}", received[1].Topic, received[1].Payload)
	}
}

func TestEventBus_TopicFilter(t *testing.T) {
	bus := NewEventBus()

	chFiltered, cancelFiltered := bus.Subscribe(0, "want.this")
	defer cancelFiltered()

	chAll, cancelAll := bus.Subscribe(0)
	defer cancelAll()

	bus.Publish("want.this", "yes")
	bus.Publish("skip.this", "no")

	// chFiltered should only receive the "want.this" event.
	select {
	case ev := <-chFiltered:
		if ev.Topic != "want.this" {
			t.Errorf("filtered sub: expected want.this, got %s", ev.Topic)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("filtered subscriber did not receive expected event")
	}

	// chFiltered should NOT receive the "skip.this" event — verify no extra frame.
	select {
	case unexpected := <-chFiltered:
		t.Errorf("filtered sub got unexpected event: %s", unexpected.Topic)
	case <-time.After(50 * time.Millisecond):
		// correct — nothing arrived
	}

	// chAll should receive both events.
	got := 0
	deadline := time.After(100 * time.Millisecond)
	for got < 2 {
		select {
		case <-chAll:
			got++
		case <-deadline:
			t.Fatalf("all-topics subscriber only got %d/2 events", got)
		}
	}
}

func TestEventBus_Cancel(t *testing.T) {
	bus := NewEventBus()

	ch, cancel := bus.Subscribe(4)
	cancel() // unsubscribe immediately

	// Channel should be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after cancel")
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("channel not closed after cancel")
	}

	// Publish after cancel should not panic.
	bus.Publish("any", "value")
}

func TestEventBus_NonBlockingSlowSubscriber(t *testing.T) {
	bus := NewEventBus()

	// Tiny buffer so the subscriber is immediately full.
	_, cancelSlow := bus.Subscribe(1)
	defer cancelSlow()

	// Fast subscriber should still receive all events.
	chFast, cancelFast := bus.Subscribe(64)
	defer cancelFast()

	// Publish more than the slow subscriber's buffer — must not block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 20 {
			bus.Publish("t", i)
		}
	}()

	select {
	case <-done:
		// good — Publish did not block
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Publish blocked on slow subscriber")
	}

	// Fast subscriber must have received at least one event.
	select {
	case <-chFast:
		// got at least one
	case <-time.After(100 * time.Millisecond):
		t.Fatal("fast subscriber received no events")
	}
}

func TestMultiEmitter(t *testing.T) {
	var calls []string
	rec1 := EmitterFunc(func(_ context.Context, topic string, _ any) { calls = append(calls, "r1:"+topic) })
	rec2 := EmitterFunc(func(_ context.Context, topic string, _ any) { calls = append(calls, "r2:"+topic) })

	me := NewMultiEmitter(rec1, rec2)
	me.Emit(context.Background(), "x", nil)
	me.Emit(context.Background(), "y", nil)

	want := []string{"r1:x", "r2:x", "r1:y", "r2:y"}
	if len(calls) != len(want) {
		t.Fatalf("got %v, want %v", calls, want)
	}
	for i, c := range calls {
		if c != want[i] {
			t.Errorf("call[%d]: got %q, want %q", i, c, want[i])
		}
	}
}

// EmitterFunc is a func that implements Emitter.
type EmitterFunc func(ctx context.Context, topic string, payload any)

func (f EmitterFunc) Emit(ctx context.Context, topic string, payload any) { f(ctx, topic, payload) }
