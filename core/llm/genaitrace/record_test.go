package genaitrace

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestAttrSet_ScopeRefusesKeysOffTheirSpan: a stored-record-only key can
// never be set on a span — the stats-pull keys, and gen_ai.response.id
// (Fleet/counsel D32), not even through an unscoped set — and a
// chat-only key never on the turn span. The response id IS admitted on
// the stored record.
func TestAttrSet_ScopeRefusesKeysOffTheirSpan(t *testing.T) {
	before := DroppedAttributes()
	chat := attrSet{scope: SpanNameChat}
	chat.int("kenaz.provider.latency_ms", 10)
	chat.float("kenaz.provider.cost_usd", 0.1)
	chat.str("gen_ai.response.id", "gen-1760000000-AbC")
	turn := attrSet{scope: SpanNameTurn}
	turn.int("gen_ai.usage.input_tokens", 3)
	turn.str("gen_ai.response.id", "gen-1760000000-AbC")
	var unscoped attrSet
	unscoped.str("gen_ai.response.id", "gen-1760000000-AbC")
	if len(chat.kv)+len(turn.kv)+len(unscoped.kv) != 0 {
		t.Fatalf("attrSet kept %v / %v / %v", chat.kv, turn.kv, unscoped.kv)
	}
	if got := DroppedAttributes() - before; got != 6 {
		t.Fatalf("dropped %d, want 6", got)
	}
	stored := attrSet{scope: StoredRecord}
	stored.str("gen_ai.response.id", "gen-1760000000-AbC")
	if len(stored.kv) != 1 || stored.kv[0].Value.AsString() != "gen-1760000000-AbC" {
		t.Fatalf("stored-record set refused the response id: %v", stored.kv)
	}
	for _, s := range SpanAttributes {
		if s.carriedOn(StoredRecord) && !s.carriedOn(SpanNameChat) &&
			!strings.HasPrefix(s.Key, "kenaz.provider.") && s.Key != "gen_ai.response.id" {
			t.Errorf("%s: stored-only keys are the provider stats-pull keys (kenaz.provider.*) and gen_ai.response.id", s.Key)
		}
	}
	if s := specByKey["kenaz.provider.request_id"]; s.carriedOn(SpanNameChat) || s.carriedOn(SpanNameTurn) || !s.carriedOn(StoredRecord) {
		t.Fatalf("kenaz.provider.request_id Spans = %v, want stored record only (legal D82 (e))", s.Spans)
	}
	if s := specByKey["gen_ai.response.id"]; s.carriedOn(SpanNameChat) || s.carriedOn(SpanNameTurn) || !s.carriedOn(StoredRecord) {
		t.Fatalf("gen_ai.response.id Spans = %v, want stored record only (Fleet/counsel D32)", s.Spans)
	}
}

// TestBoundProviderStats: every stats field goes through the allowlist —
// enum tokens normalised, junk and out-of-range values dropped.
func TestBoundProviderStats(t *testing.T) {
	i := func(v int64) *int64 { return &v }
	f := func(v float64) *float64 { return &v }
	got := BoundProviderStats(RawProviderStats{
		Upstream: "Google AI Studio", NativeFinishReason: "STOP",
		NativeInputTokens: i(10), NativeOutputTokens: i(-1), NativeReasoningTokens: nil,
		NativeCachedTokens: i(2), LatencyMS: f(812.7), GenerationTimeMS: f(-5), CostUSD: f(1e9),
	})
	if got.Upstream != "google-ai-studio" || got.NativeFinishReason != "stop" ||
		got.NativeInputTokens == nil || *got.NativeInputTokens != 10 || got.NativeOutputTokens != nil ||
		got.NativeReasoningTokens != nil || *got.NativeCachedTokens != 2 || *got.LatencyMS != 812 ||
		got.GenerationTimeMS != nil || got.CostUSD != nil {
		t.Fatalf("bounded: %+v", got)
	}
	if junk := BoundProviderStats(RawProviderStats{Upstream: "free text, with: punctuation!", NativeFinishReason: strings.Repeat("x", 65)}); junk.Upstream != "" || junk.NativeFinishReason != "" {
		t.Fatalf("free text survived: %+v", junk)
	}
}

type captureRecorder struct {
	mu   sync.Mutex
	recs []CallRecord
}

func (c *captureRecorder) RecordCall(r CallRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r)
}

func (c *captureRecorder) snapshot() []CallRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CallRecord(nil), c.recs...)
}

type doneStream struct{ resp llm.Response }

func (d *doneStream) Events() <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}
func (d *doneStream) Cancel() error                { return nil }
func (d *doneStream) Final() (llm.Response, error) { return d.resp, nil }

// TestRecordCall_MirrorsSpanAttributes: the recorder sees one record per
// call with the span's values, the stored-record-only response id (handed
// over directly, never via the span) and the routing-only profile id; a
// nil recorder costs nothing.
func TestRecordCall_MirrorsSpanAttributes(t *testing.T) {
	cap := &captureRecorder{}
	restore := SetRecorder(cap)
	defer restore()
	temp := 0.5
	call := Call{ProviderKind: "anthropic", RequestModel: "claude-sonnet-4-5", MaxTokens: 64, Temperature: &temp,
		ToolCount: 2, ToolSchemaTokens: 300, SessionID: "s-1", ProfileID: "prof-1"}
	s, err := Instrument(WithFallback(context.Background()), call, func(llm.Response) llm.Cost {
		return llm.Cost{Total: 0.25, Currency: "USD"}
	}, func(context.Context) (llm.Stream, error) {
		return &doneStream{resp: llm.Response{FinishReason: "end_turn",
			Usage: llm.Usage{InputTokens: 9, OutputTokens: 3, CachedInputRead: 4, CachedInputWrite: 1, ReasoningTokens: 2},
			Meta:  &llm.CallMeta{ResponseID: "msg_1", ResponseModel: "claude-sonnet-4-5-x", RequestID: "req_1"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Final()
	_, _ = s.Final() // a second Final records nothing more
	recs := cap.snapshot()
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	r := recs[0]
	if !r.OK || r.ProfileID != "prof-1" || r.Provider != "anthropic" || r.RequestModel != "claude-sonnet-4-5" ||
		r.ResponseModel != "claude-sonnet-4-5-x" || r.ResponseID != "msg_1" || r.RequestID != "req_1" ||
		r.MaxTokens != 64 || r.Temperature == nil || *r.Temperature != 0.5 || !r.Fallback || !r.Stream ||
		r.ToolCount != 2 || r.ToolSchemaTokensEst != 300 || r.InputTokens != 9 || r.OutputTokens != 3 ||
		r.CacheReadTokens != 4 || r.CacheWriteTokens != 1 || r.ReasoningTokens != 2 ||
		r.CostUSD == nil || *r.CostUSD != 0.25 || len(r.FinishReasons) != 1 || r.FinishReasons[0] != "end_turn" ||
		r.SessionHash != SessionHash("s-1") || r.End.Before(r.Start) {
		t.Fatalf("record: %+v", r)
	}
}

// TestSessionHashKey_StableAcrossRestarts: the per-install key is minted
// once (0600, hex), re-read on the next boot, and makes the hash stable;
// a malformed key file is never overwritten.
func TestSessionHashKey_StableAcrossRestarts(t *testing.T) {
	prev := *sessionKey.Load()
	defer func() { _ = SetSessionHashKey(prev) }()
	dir := t.TempDir()

	if err := UseInstallSessionHashKey(dir); err != nil {
		t.Fatal(err)
	}
	h1 := SessionHash("sess-stable")
	fi, err := os.Stat(SessionHashKeyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v, want 0600", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(SessionHashKeyPath(dir))
	if k, err := hex.DecodeString(strings.TrimSpace(string(raw))); err != nil || len(k) != 32 {
		t.Fatalf("key file is not 32 hex-encoded bytes: %q", raw)
	}

	// "Restart": a fresh per-process key, then the install key again.
	_ = SetSessionHashKey(make([]byte, 32))
	if SessionHash("sess-stable") == h1 {
		t.Fatal("test premise: a different key must give a different hash")
	}
	if err := UseInstallSessionHashKey(dir); err != nil {
		t.Fatal(err)
	}
	if got := SessionHash("sess-stable"); got != h1 || !hashPattern.MatchString(got) {
		t.Fatalf("hash after restart %q, want %q (16 hex)", got, h1)
	}

	bad := t.TempDir()
	p := SessionHashKeyPath(bad)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("not-hex\n"), 0o600)
	if err := UseInstallSessionHashKey(bad); err == nil {
		t.Fatal("a malformed key file must be an error")
	}
	if b, _ := os.ReadFile(p); string(b) != "not-hex\n" {
		t.Fatal("a malformed key file was overwritten")
	}
	if err := SetSessionHashKey([]byte("short")); err == nil {
		t.Fatal("a short key must be refused")
	}
}
