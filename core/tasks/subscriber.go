package tasks

import "sync"

// subscriberSet holds a set of channels that receive Line broadcasts when
// a task produces new output. Used by the __monitor watch mode.
//
// subscriberSet is safe for concurrent use.
type subscriberSet struct {
	mu   sync.Mutex
	subs map[chan<- Line]struct{}
}

func newSubscriberSet() *subscriberSet {
	return &subscriberSet{
		subs: make(map[chan<- Line]struct{}),
	}
}

// Add registers ch to receive future Line broadcasts.
func (s *subscriberSet) Add(ch chan<- Line) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs[ch] = struct{}{}
}

// Remove deregisters ch. After Remove returns the channel will never be
// written to again by this set.
func (s *subscriberSet) Remove(ch chan<- Line) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, ch)
}

// broadcast sends ln to all registered subscribers. The send is
// non-blocking: if a subscriber channel is full the line is dropped for
// that subscriber (the ring buffer retains the data for drain calls).
func (s *subscriberSet) broadcast(ln Line) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- ln:
		default:
			// Subscriber is slow — drop rather than block.
		}
	}
}

// closeAll closes all subscriber channels and removes them. Called when a
// task reaches a terminal state so __monitor watch loops unblock.
func (s *subscriberSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		close(ch)
	}
	s.subs = make(map[chan<- Line]struct{})
}
