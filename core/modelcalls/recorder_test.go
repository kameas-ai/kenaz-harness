package modelcalls_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/modelcalls"
)

const (
	recPrompt     = "SECRET-RECORDER-PROMPT"
	recSystem     = "SECRET-RECORDER-SYSTEM"
	recCompletion = "SECRET-RECORDER-COMPLETION"
	recSessionID  = "raw-session-id-recorder"
)

type finished struct {
	resp llm.Response
	err  error
}

func (f *finished) Events() <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Kind: llm.StreamText, Text: recCompletion}
	close(ch)
	return ch
}
func (f *finished) Cancel() error                { return nil }
func (f *finished) Final() (llm.Response, error) { return f.resp, f.err }

// TestRecorder_SpanToStore_NoContent drives a content-bearing provider call
// through the real choke point (genaitrace.Instrument) with the production
// Recorder installed, into real sqlite: the row carries the span's
// allowlisted values (session as the keyed hash, not the raw id), a failed
// call lands as an error row, and no content string appears in any column.
// The provider response id lands in response_id while NO span carries it
// (Fleet/counsel D32: gen_ai.response.id is stored-record only).
//
// Not parallel: SetRecorder and the OTel provider are process-global.
func TestRecorder_SpanToStore_NoContent(t *testing.T) {
	s, db := newStore(t)
	rec := modelcalls.NewRecorder(s, nil)
	// The stats pull still sees the generation id although no span does:
	// an unstarted puller holds the job it would run.
	puller := modelcalls.NewStatsPuller(modelcalls.StatsPullerOptions{Store: s})
	defer puller.Stop()
	rec.OnStored = puller.Consider
	rec.Start()
	restore := genaitrace.SetRecorder(rec)
	defer restore()
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer func() { otel.SetTracerProvider(prev); _ = tp.Shutdown(context.Background()) }()

	req := llm.GenerationRequest{
		SessionID: recSessionID,
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleSystem, recSystem),
			llm.NewTextMessage(llm.RoleUser, recPrompt),
		},
	}
	call := genaitrace.CallFromRequest(req, llm.ProviderProfile{ID: "p-1", Kind: "openrouter", Model: "openai/gpt-4o"})
	st, err := genaitrace.Instrument(context.Background(), call, nil, func(context.Context) (llm.Stream, error) {
		return &finished{resp: llm.Response{FinishReason: "stop", Usage: llm.Usage{InputTokens: 33, OutputTokens: 4},
			Meta: &llm.CallMeta{ResponseID: "gen-rec-0001", UpstreamProvider: "Anthropic"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range st.Events() {
	}
	if _, err := st.Final(); err != nil {
		t.Fatal(err)
	}
	if _, err := genaitrace.Instrument(context.Background(), call, nil, func(context.Context) (llm.Stream, error) {
		return nil, &llm.ErrTransient{Status: 429, Message: "slow down " + recPrompt}
	}); err == nil {
		t.Fatal("want the open error")
	}
	if err := rec.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.Written() != 2 || rec.Dropped() != 0 {
		t.Fatalf("written %d dropped %d, want 2/0", rec.Written(), rec.Dropped())
	}

	got, err := s.ListBySessionHash(context.Background(), genaitrace.SessionHash(recSessionID), 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("list: %v %v", got, err)
	}
	var ok, bad modelcalls.CallView
	for _, v := range got {
		if v.Status == "ok" {
			ok = v
		} else {
			bad = v
		}
	}
	if ok.ResponseID != "gen-rec-0001" || ok.Provider != "openrouter" || ok.RequestModel != "openai/gpt-4o" ||
		ok.InputTokens != 33 || ok.OutputTokens != 4 || ok.Upstream != "anthropic" ||
		len(ok.FinishReasons) != 1 || ok.FinishReasons[0] != "stop" || ok.TTFTMS < 0 {
		t.Errorf("ok row: %+v", ok)
	}
	if bad.ErrorType != "rate_limited" || bad.Attempt != 0 {
		t.Errorf("error row: %+v", bad)
	}
	if n := puller.Pending(); n != 1 {
		t.Errorf("stats pulls queued = %d, want 1 (the ok OpenRouter call, keyed on its generation id)", n)
	}
	if ended := spans.Ended(); len(ended) != 2 {
		t.Fatalf("ended spans = %d, want 2", len(ended))
	}
	for _, sp := range spans.Ended() {
		for _, kv := range sp.Attributes() {
			if string(kv.Key) == "gen_ai.response.id" || strings.Contains(kv.Value.Emit(), "gen-rec-0001") {
				t.Errorf("span %s carries the provider response id (%s = %q) — stored-record only", sp.Name(), kv.Key, kv.Value.Emit())
			}
		}
	}
	dump := dumpTable(t, db)
	for _, c := range []string{recPrompt, recSystem, recCompletion, recSessionID, "slow down", "p-1"} {
		if strings.Contains(dump, c) {
			t.Errorf("model_calls holds %q: %s", c, dump)
		}
	}
}

// TestRecorder_RecordCallNeverBlocks: with the writer stalled (never
// started) RecordCall still returns at once; the overflow is dropped and
// counted, and a closed recorder drops too.
func TestRecorder_RecordCallNeverBlocks(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	rec := modelcalls.NewRecorder(s, nil)
	start := time.Now()
	for i := 0; i < 600; i++ {
		rec.RecordCall(genaitrace.CallRecord{SpanID: "x"})
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("600 RecordCalls took %v — the hook blocks", el)
	}
	if rec.Dropped() != 600-512 {
		t.Fatalf("dropped %d, want %d (bounded queue of 512)", rec.Dropped(), 600-512)
	}
	_ = rec.Close(context.Background())
	rec.RecordCall(genaitrace.CallRecord{SpanID: "y"})
	if rec.Dropped() != 600-512+1 {
		t.Fatalf("a closed recorder must drop (counted), dropped %d", rec.Dropped())
	}
}
