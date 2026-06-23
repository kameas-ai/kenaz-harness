package rpc

import (
	"context"
	"sync"
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
	ch     chan BusEvent
	topics map[string]struct{} // nil means subscribe to all topics
}

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

	b.mu.Lock()
	id := b.seq
	b.seq++
	b.subs[id] = subscriber{ch: ch, topics: topicSet}
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
	return ch, cancel
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
		default: // subscriber is slow; drop rather than block
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
