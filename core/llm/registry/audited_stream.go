package registry

import (
	"context"
	"sync"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/events"
)

// auditedStream wraps an adapter Stream with the connector's terminal
// emit logic (response_final, cancelled, error) and CostReducer
// integration. It also relays per-chunk audit events.
type auditedStream struct {
	inner    llm.Stream
	req      llm.GenerationRequest
	prof     llm.ProviderProfile
	emitter  *events.Emitter
	reducer  CostReducer
	started  time.Time
	attempts int
	credBuf  []byte

	mu       sync.Mutex
	finalized bool
	out       chan llm.StreamEvent
	once      sync.Once
}

// Events returns a buffered channel that mirrors inner.Events() while
// emitting llm/stream_chunk audit entries for each delta.
func (s *auditedStream) Events() <-chan llm.StreamEvent {
	s.once.Do(func() {
		s.out = make(chan llm.StreamEvent, 16)
		go s.pump()
	})
	return s.out
}

func (s *auditedStream) pump() {
	defer close(s.out)
	for ev := range s.inner.Events() {
		_ = s.emitter.StreamChunk(context.Background(), s.req, s.prof, ev)
		s.out <- ev
	}
}

// Cancel terminates the upstream connection and emits llm/cancelled.
func (s *auditedStream) Cancel() error {
	err := s.inner.Cancel()
	_ = s.emitter.Cancelled(context.Background(), s.req, s.prof, "caller")
	zero(s.credBuf)
	return err
}

// Final blocks until inner.Final() completes, emits llm/response_final
// (or llm/error on failure), and returns the terminal Response.
func (s *auditedStream) Final() (llm.Response, error) {
	resp, err := s.inner.Final()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer zero(s.credBuf)
	if s.finalized {
		return resp, err
	}
	s.finalized = true

	if err != nil {
		_ = s.emitter.Error(context.Background(), s.req, s.prof, err)
		return resp, err
	}
	resp.Attempts = s.attempts
	if s.reducer != nil {
		resp.Cost = s.reducer.Derive(resp.Usage, s.prof.Kind, s.prof.Model)
	} else if resp.Cost.Currency == "" {
		resp.Cost = llm.Cost{Indeterminate: true}
	}
	if resp.SnapshotID == "" {
		// Best-effort: snapshot id might not be available on response;
		// the request_submitted payload retains it for replay.
	}
	latencyMS := time.Since(s.started).Milliseconds()
	_ = s.emitter.ResponseFinal(context.Background(), s.req, s.prof, resp, latencyMS)
	return resp, nil
}
