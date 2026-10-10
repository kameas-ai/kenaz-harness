package genaitrace_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace/spantest"
)

// chunkStream emits n text chunks then finishes with resp.
type chunkStream struct {
	ch   chan llm.StreamEvent
	resp llm.Response
	err  error
}

func newChunkStream(n int, resp llm.Response, err error) *chunkStream {
	ch := make(chan llm.StreamEvent, 4)
	go func() {
		defer close(ch)
		for i := 0; i < n; i++ {
			ch <- llm.StreamEvent{Kind: llm.StreamText, Text: spantest.CompletionText}
		}
	}()
	return &chunkStream{ch: ch, resp: resp, err: err}
}

func (s *chunkStream) Events() <-chan llm.StreamEvent { return s.ch }
func (s *chunkStream) Cancel() error                  { return nil }
func (s *chunkStream) Final() (llm.Response, error) {
	for range s.ch {
	}
	return s.resp, s.err
}

func call() genaitrace.Call {
	return genaitrace.CallFromRequest(spantest.Request("p"), llm.ProviderProfile{Kind: "openrouter", Model: "openai/gpt-4o"})
}

func drain(t *testing.T, s llm.Stream) (llm.Response, error) {
	t.Helper()
	for range s.Events() {
	}
	return s.Final()
}

// TestInstrument_AttemptSequenceFallbackAndErrors: within one call
// sequence every provider call takes the next kenaz.llm.attempt; a
// fallback hop is flagged; a failed open ends at once with a typed
// error.type and a code-only status; a mid-stream failure does the same at
// Final; a cancelled stream reports "cancelled".
func TestInstrument_AttemptSequenceFallbackAndErrors(t *testing.T) {
	rec := spantest.Record(t)
	ctx := genaitrace.WithCallSequence(context.Background())
	ok := llm.Response{FinishReason: "stop", Usage: llm.Usage{InputTokens: 5, OutputTokens: 2}}

	// attempt 0: pre-stream 429
	if _, err := genaitrace.Instrument(ctx, call(), nil, func(context.Context) (llm.Stream, error) {
		return nil, &llm.ErrTransient{Status: 429, Message: "slow down " + spantest.PromptText}
	}); err == nil {
		t.Fatal("want the open error back")
	}
	// attempt 1: mid-stream auth failure surfacing at Final
	s, err := genaitrace.Instrument(ctx, call(), nil, func(context.Context) (llm.Stream, error) {
		return newChunkStream(1, llm.Response{}, &llm.ErrAuth{Status: 401, Message: "bad key"}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain(t, s); err == nil {
		t.Fatal("want the Final error back")
	}
	// attempt 2: a fallback hop that succeeds
	s, err = genaitrace.Instrument(genaitrace.WithFallback(ctx), call(), nil, func(context.Context) (llm.Stream, error) {
		return newChunkStream(3, ok, nil), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain(t, s); err != nil {
		t.Fatal(err)
	}
	// attempt 3: cancelled by the caller
	s, err = genaitrace.Instrument(ctx, call(), nil, func(context.Context) (llm.Stream, error) {
		return newChunkStream(0, ok, nil), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Cancel()
	_, _ = s.Final()

	spans := spantest.Named(rec, genaitrace.SpanNameChat)
	if len(spans) != 4 {
		t.Fatalf("got %d spans, want 4", len(spans))
	}
	type want struct {
		attempt  int64
		fallback bool
		errType  string
	}
	for i, w := range []want{{0, false, "rate_limited"}, {1, false, "auth_invalid"}, {2, true, ""}, {3, false, "cancelled"}} {
		sp := spans[i]
		spantest.AssertAllowlisted(t, sp, spantest.Content...)
		got := spantest.Attrs(sp)
		if got["kenaz.llm.attempt"] != w.attempt || got["kenaz.llm.fallback"] != w.fallback {
			t.Errorf("span %d attempt/fallback = %v/%v, want %d/%v", i, got["kenaz.llm.attempt"], got["kenaz.llm.fallback"], w.attempt, w.fallback)
		}
		if w.errType == "" {
			if _, has := got["error.type"]; has || got["gen_ai.usage.input_tokens"] != int64(5) {
				t.Errorf("span %d: want success attrs, got %v", i, got)
			}
			continue
		}
		if got["error.type"] != w.errType {
			t.Errorf("span %d error.type = %v, want %s", i, got["error.type"], w.errType)
		}
		if sp.Status().Code.String() != "Error" {
			t.Errorf("span %d status = %v, want Error", i, sp.Status().Code)
		}
	}
	if spans[2].SpanKind().String() != "client" || spans[2].Name() != "gen_ai.chat" {
		t.Errorf("span kind/name = %v/%s", spans[2].SpanKind(), spans[2].Name())
	}
}

// TestInstrument_FinalWithoutEventsAndConcurrentUse: Final without ever
// calling Events (the structured-output repair drains the inner stream
// first) still ends the span once, and concurrent Final/Cancel from two
// goroutines ends it exactly once (run under -race in CI).
func TestInstrument_FinalWithoutEventsAndConcurrentUse(t *testing.T) {
	rec := spantest.Record(t)
	for i := 0; i < 20; i++ {
		s, err := genaitrace.Instrument(context.Background(), call(), nil, func(context.Context) (llm.Stream, error) {
			return newChunkStream(2, llm.Response{FinishReason: "stop"}, nil), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { defer close(done); _, _ = s.Final() }()
		_ = s.Cancel()
		<-done
		_, _ = s.Final()
	}
	if n := len(spantest.Named(rec, genaitrace.SpanNameChat)); n != 20 {
		t.Fatalf("got %d ended spans, want 20 (one per call)", n)
	}
}

// slowExporter blocks every export — a stalled local store or network.
type slowExporter struct{ release chan struct{} }

func (e slowExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	select {
	case <-e.release:
	case <-ctx.Done():
	}
	return nil
}
func (slowExporter) Shutdown(context.Context) error { return nil }

// TestInstrument_StreamNotBlockedByExport is the overhead bound: with the
// production batching processor in front of an exporter that never
// returns, 200 calls of 50 chunks each still stream to completion well
// inside the budget, and the per-call instrumentation overhead against
// the same calls uninstrumented stays under 2ms on average (generous for
// -race; BenchmarkInstrument_StreamOverhead measures ~20µs per 50-chunk
// call on an M-series laptop, against network latencies of 100ms+).
func TestInstrument_StreamNotBlockedByExport(t *testing.T) {
	release := make(chan struct{})
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(slowExporter{release: release},
		sdktrace.WithMaxQueueSize(64), sdktrace.WithMaxExportBatchSize(8)))
	rec := spantest.Record(t) // serialise with other global-provider tests
	_ = rec
	restore := swapProvider(tp)
	defer func() {
		close(release)
		restore()
		_ = tp.Shutdown(context.Background())
	}()

	const calls, chunks = 200, 50
	run := func(instrumented bool) time.Duration {
		start := time.Now()
		for i := 0; i < calls; i++ {
			open := func(context.Context) (llm.Stream, error) {
				return newChunkStream(chunks, llm.Response{FinishReason: "stop", Usage: llm.Usage{InputTokens: 1}}, nil), nil
			}
			var s llm.Stream
			var err error
			if instrumented {
				s, err = genaitrace.Instrument(context.Background(), call(), nil, open)
			} else {
				s, err = open(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for range s.Events() {
				n++
			}
			if _, err := s.Final(); err != nil || n != chunks {
				t.Fatalf("stream: n=%d err=%v", n, err)
			}
		}
		return time.Since(start)
	}
	raw := run(false)
	traced := run(true)
	perCall := (traced - raw) / calls
	t.Logf("raw=%v traced=%v overhead/call=%v", raw, traced, perCall)
	if traced > 10*time.Second {
		t.Fatalf("instrumented streaming took %v with a stalled exporter — span export is on the stream path", traced)
	}
	if perCall > 2*time.Millisecond {
		t.Fatalf("instrumentation overhead %v per call exceeds the 2ms budget", perCall)
	}
}

// BenchmarkInstrument_StreamOverhead measures one 50-chunk call with and
// without the gen_ai.chat span (recording provider; export excluded).
func BenchmarkInstrument_StreamOverhead(b *testing.B) {
	rec := tracetest.NewSpanRecorder()
	restore := swapProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	defer restore()
	for _, instrumented := range []bool{false, true} {
		b.Run(fmt.Sprintf("instrumented=%v", instrumented), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				open := func(context.Context) (llm.Stream, error) {
					return newChunkStream(50, llm.Response{FinishReason: "stop"}, nil), nil
				}
				var s llm.Stream
				if instrumented {
					s, _ = genaitrace.Instrument(context.Background(), call(), nil, open)
				} else {
					s, _ = open(context.Background())
				}
				for range s.Events() {
				}
				_, _ = s.Final()
			}
		})
	}
}

var errSentinel = errors.New("x")

func TestErrorType_Closed(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&llm.ErrCancelled{Reason: "stop"}, "cancelled"},
		{context.Canceled, "cancelled"},
		{&llm.ErrTransient{Status: 503}, "provider_unavailable"},
		{&llm.ErrTransient{Status: 0}, "network"},
		{&llm.ErrPaymentRequired{Status: 402}, "payment_required"},
		{&llm.ErrInvalidRequest{Status: 404}, "model_not_found"},
		{errSentinel, "unknown"},
	} {
		if got := genaitrace.ErrorType(c.err); got != c.want {
			t.Errorf("ErrorType(%T) = %q, want %q", c.err, got, c.want)
		}
		found := false
		for _, v := range genaitrace.ErrorTypes {
			found = found || v == c.want
		}
		if !found {
			t.Errorf("%q is not in ErrorTypes", c.want)
		}
	}
}

// swapProvider installs tp as the OTel global and returns the restore.
func swapProvider(tp trace.TracerProvider) func() {
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return func() { otel.SetTracerProvider(prev) }
}
