package rpc

import (
	"context"
	"sync"
	"sync/atomic"
)

// BusEvent is a single published event on the EventBus.
type BusEvent struct {
	Topic   string
	Payload any
}

// EventBus is a lightweight in-process pub-sub bus.  Publishers call
// Publish; subscribers receive from the channel returned by Subscribe.
// It is the non-Wails emission sink used by served mode's /ws handler to
// push events in real time without the Wails runtime context.
//
// Fan-out is non-blocking: slow subscribers are dropped rather than
// blocking the publisher.  Buffer size is configurable; default is 64.
type EventBus struct {
	mu   sync.RWMutex
	subs map[uint64]subscriber
	seq  uint64
}

type subscriber struct {
	ch      chan BusEvent
	topics  map[string]struct{} // nil means subscribe to all topics
	dropped *atomic.Uint64      // events discarded because ch was full
}

// Subscription is a live bus subscription that also accounts for events
// the bus had to discard because this subscriber's buffer was full.
//
// The plain [EventBus.Subscribe] API cannot tell a subscriber that it
// missed events, which is fine for coarse "something changed, re-read it"
// topics but NOT for token streams: silently swallowing part of a model
// response and showing the remainder as if it were complete is a
// correctness bug the user cannot even see. Consumers that forward
// per-token events to a remote client (core/serve's WebSocket fan-out)
// use [EventBus.SubscribeTracked] and surface [Subscription.Dropped] to
// that client instead of pretending nothing was lost.
type Subscription struct {
	// C delivers matching events. Closed by Close.
	C <-chan BusEvent

	dropped *atomic.Uint64
	cancel  context.CancelFunc
}

// Dropped returns the number of events the bus discarded for this
// subscription because its buffer was full. Monotonically increasing.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// Close releases the subscription and closes C. Safe to call more than once.
func (s *Subscription) Close() { s.cancel() }

// NewEventBus constructs an EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[uint64]subscriber),
	}
}

// Subscribe returns a channel that receives events published on any of the
// given topics (pass no topics to receive all events).  The returned cancel
// function must be called when the subscriber is done to release resources.
// bufSize controls the channel buffer; 0 uses a default of 64.
func (b *EventBus) Subscribe(bufSize int, topics ...string) (<-chan BusEvent, context.CancelFunc) {
	sub := b.SubscribeTracked(bufSize, topics...)
	return sub.C, sub.Close
}

// SubscribeTracked is [EventBus.Subscribe] plus drop accounting: the
// returned [Subscription] counts every event the bus had to discard
// because this subscriber's buffer was full, so the consumer can tell
// its own downstream that data was lost instead of hiding the gap.
func (b *EventBus) SubscribeTracked(bufSize int, topics ...string) *Subscription {
	if bufSize <= 0 {
		bufSize = 64
	}
	ch := make(chan BusEvent, bufSize)

	var topicSet map[string]struct{}
	if len(topics) > 0 {
		topicSet = make(map[string]struct{}, len(topics))
		for _, t := range topics {
			topicSet[t] = struct{}{}
		}
	}

	dropped := &atomic.Uint64{}

	b.mu.Lock()
	id := b.seq
	b.seq++
	b.subs[id] = subscriber{ch: ch, topics: topicSet, dropped: dropped}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		sub, ok := b.subs[id]
		if ok {
			delete(b.subs, id)
			close(sub.ch)
		}
		b.mu.Unlock()
	}
	return &Subscription{C: ch, dropped: dropped, cancel: cancel}
}

// Publish sends ev to all matching subscribers.  Non-blocking: a full
// subscriber channel is skipped rather than blocking the caller.
func (b *EventBus) Publish(topic string, payload any) {
	ev := BusEvent{Topic: topic, Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if sub.topics != nil {
			if _, ok := sub.topics[topic]; !ok {
				continue
			}
		}
		select {
		case sub.ch <- ev:
		default:
			// Subscriber is slow; drop rather than block the publisher.
			// The drop is COUNTED so a tracked subscriber can report the
			// gap downstream instead of silently losing the event.
			if sub.dropped != nil {
				sub.dropped.Add(1)
			}
		}
	}
}

// busEmitter wraps an EventBus and implements the Emitter interface so the
// StreamBroker can publish to the bus alongside the Wails desktop sink.
type busEmitter struct {
	bus *EventBus
}

func (e *busEmitter) Emit(_ context.Context, topic string, payload any) {
	e.bus.Publish(topic, payload)
}
