package llm

import (
	"strings"
	"sync"
)

// SyntheticStream wraps a fully-resolved Response in the Stream surface
// so any caller that has a complete non-streaming response can still
// hand it to code that expects a Stream (the kernel's stream pump, for
// example). Lifted from core/llm/bedrock/bearer.go (WP01 of
// provider-implementation-uniformity-01KQ8V4F).
//
// The synthetic stream emits one StreamText event (the full text
// content joined from all text blocks) followed by a StreamFinish
// event, then closes the events channel. FinishReason defaults to
// "stop" when empty.
//
// This is exported so all adapters can reuse it; bedrock/bearer.go
// keeps a thin alias (syntheticStream) that delegates here until WP10
// removes the alias.
type SyntheticStream struct {
	events       chan StreamEvent
	final        Response
	finishReason string
	mu           sync.Mutex
	cancelled    bool
}

// NewSyntheticStream constructs a SyntheticStream and immediately
// starts the background goroutine that emits events. Callers receive
// the fully-typed Stream interface; the goroutine closes the events
// channel when done.
func NewSyntheticStream(resp Response, finishReason string) Stream {
	s := &SyntheticStream{
		events:       make(chan StreamEvent, 4),
		final:        resp,
		finishReason: finishReason,
	}
	go s.emit()
	return s
}

// Events returns the channel of streaming chunks.
func (s *SyntheticStream) Events() <-chan StreamEvent { return s.events }

// Cancel marks the stream as cancelled. Events already emitted are
// not recalled; Final returns ErrCancelled after Cancel.
func (s *SyntheticStream) Cancel() error {
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	return nil
}

// Final blocks until the events channel is closed then returns the
// accumulated Response. Returns ErrCancelled when Cancel was called.
func (s *SyntheticStream) Final() (Response, error) {
	// Drain remaining events so emit() can progress if the caller
	// didn't consume the channel.
	for range s.events {
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled {
		return Response{}, &ErrCancelled{Reason: "cancelled"}
	}
	return s.final, nil
}

func (s *SyntheticStream) emit() {
	defer close(s.events)
	// One text event with the concatenated text, then a finish event.
	var fullText strings.Builder
	for _, blk := range s.final.Content {
		if blk.Type == "text" {
			fullText.WriteString(blk.Text)
		}
	}
	if fullText.Len() > 0 {
		s.events <- StreamEvent{Kind: StreamText, Text: fullText.String()}
	}
	reason := s.finishReason
	if reason == "" {
		reason = "stop"
	}
	s.events <- StreamEvent{Kind: StreamFinish, Finish: reason}
}
