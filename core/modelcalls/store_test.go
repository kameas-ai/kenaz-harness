package modelcalls_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/modelcalls"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// openDB opens the REAL unified sqlite DB (storagesqlite.Open, every
// mission's migrations applied — model-calls/1800 included), never a
// memory fake (WP-persistence-integrity AC-PI-2).
func openDB(t *testing.T) storage.DB {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

func newStore(t *testing.T) (*modelcalls.Store, storage.DB) {
	t.Helper()
	db := openDB(t)
	s, err := modelcalls.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func fullRecord(span string, start time.Time) genaitrace.CallRecord {
	return genaitrace.CallRecord{
		SpanID: span, TraceID: "trace-" + span, Start: start, End: start.Add(1500 * time.Millisecond), OK: true,
		ProfileID: "profile-never-stored", SessionHash: "0123456789abcdef", Provider: "openrouter",
		RequestModel: "openai/gpt-4o", ResponseModel: "openai/gpt-4o-2024", ResponseID: "gen-" + span,
		FinishReasons: []string{"stop", "length"}, Attempt: 2, Fallback: true, Stream: true, MaxTokens: 512,
		Temperature: f64(0.3), InputTokens: 100, OutputTokens: 20, CacheReadTokens: 40, CacheWriteTokens: 5,
		ReasoningTokens: 7, ToolCount: 3, ToolSchemaTokensEst: 900, TTFTMS: 230, CostUSD: f64(0.0123),
		RequestID: "req-1", SystemFingerprint: "fp_1", ToolUsePromptTokens: 0,
	}
}

// TestStore_RoundTrip_RealSQLite: every typed field written by Insert
// reads back through ListBySessionHash; a re-delivered span id is not
// duplicated; a span-less record is never stored; the profile id (a
// routing value) is not persisted anywhere.
func TestStore_RoundTrip_RealSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, db := newStore(t)
	start := time.UnixMilli(1_700_000_000_000)
	rec := fullRecord("span-a", start)
	if err := s.Insert(ctx, []genaitrace.CallRecord{rec, rec, {Start: start, End: start}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM model_calls").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (duplicate and span-less records skipped)", n)
	}
	got, err := s.ListBySessionHash(ctx, rec.SessionHash, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %v, %v", got, err)
	}
	v := got[0]
	want := modelcalls.CallView{
		SpanID: "span-a", StartedAtMS: start.UnixMilli(), DurationMS: 1500, Status: "ok",
		Provider: "openrouter", RequestModel: "openai/gpt-4o", ResponseModel: "openai/gpt-4o-2024",
		ResponseID: "gen-span-a", FinishReasons: []string{"stop", "length"}, Attempt: 2, Fallback: true,
		InputTokens: 100, OutputTokens: 20, CacheReadTokens: 40, CacheWriteTokens: 5, ReasoningTokens: 7,
		TTFTMS: 230,
	}
	if v.CostUSD == nil || *v.CostUSD != 0.0123 {
		t.Errorf("cost = %v", v.CostUSD)
	}
	v.CostUSD = nil
	if fmt.Sprint(v) != fmt.Sprint(want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", v, want)
	}
	var temp float64
	var fp, reqID string
	var toolCount, schema int64
	if err := db.Reader().QueryRow(ctx, `SELECT temperature, system_fingerprint, request_id, tool_count, tool_schema_tokens_est FROM model_calls`).
		Scan(&temp, &fp, &reqID, &toolCount, &schema); err != nil {
		t.Fatal(err)
	}
	if temp != 0.3 || fp != "fp_1" || reqID != "req-1" || toolCount != 3 || schema != 900 {
		t.Errorf("columns: temp %v fp %q req %q tools %d schema %d", temp, fp, reqID, toolCount, schema)
	}
	if dump := dumpTable(t, db); strings.Contains(dump, "profile-never-stored") {
		t.Errorf("the routing-only profile id was persisted: %s", dump)
	}

	// An error record reads back as status=error with its class.
	bad := fullRecord("span-b", start.Add(time.Second))
	bad.OK, bad.ErrorType = false, "rate_limited"
	if err := s.Insert(ctx, []genaitrace.CallRecord{bad}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListBySessionHash(ctx, rec.SessionHash, 10)
	if len(got) != 2 || got[0].SpanID != "span-b" || got[0].Status != "error" || got[0].ErrorType != "rate_limited" {
		t.Fatalf("newest-first error row: %+v", got)
	}
	if other, _ := s.ListBySessionHash(ctx, "fedcba9876543210", 10); len(other) != 0 {
		t.Fatalf("another session's hash listed %d rows", len(other))
	}
}

// TestStore_ApplyProviderStats_RealSQLite: the stats land on the row,
// upstream fills only when the stream left it empty.
func TestStore_ApplyProviderStats_RealSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := newStore(t)
	start := time.UnixMilli(1_700_000_000_000)
	withUp := fullRecord("span-up", start)
	withUp.Upstream = "anthropic"
	noUp := fullRecord("span-noup", start.Add(time.Second))
	if err := s.Insert(ctx, []genaitrace.CallRecord{withUp, noUp}); err != nil {
		t.Fatal(err)
	}
	st := genaitrace.ProviderStats{
		Upstream: "google-ai-studio", NativeFinishReason: "end_turn",
		NativeInputTokens: i64(101), NativeOutputTokens: i64(21), NativeReasoningTokens: i64(3),
		NativeCachedTokens: i64(40), LatencyMS: i64(812), GenerationTimeMS: i64(1490), CostUSD: f64(0.0042),
	}
	for _, id := range []string{"span-up", "span-noup"} {
		if err := s.ApplyProviderStats(ctx, id, st); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.ListBySessionHash(ctx, withUp.SessionHash, 10)
	by := map[string]modelcalls.CallView{}
	for _, v := range got {
		by[v.SpanID] = v
	}
	if by["span-up"].Upstream != "anthropic" || by["span-noup"].Upstream != "google-ai-studio" {
		t.Errorf("upstream fill: %q / %q", by["span-up"].Upstream, by["span-noup"].Upstream)
	}
	v := by["span-noup"]
	if v.StatsState != modelcalls.StatsFetched || v.NativeFinishReason != "end_turn" ||
		*v.NativeInputTokens != 101 || *v.NativeOutputTokens != 21 || *v.NativeReasoningToks != 3 ||
		*v.NativeCachedTokens != 40 || *v.ProviderLatencyMS != 812 || *v.ProviderGenTimeMS != 1490 ||
		*v.ProviderCostUSD != 0.0042 {
		t.Errorf("stats row: %+v", v)
	}
}

// TestPruner_RetentionReadFromSettingEveryPass: the retention closure is
// read on every pass — 0 means 30 days, 7 means 7 — against real rows.
func TestPruner_RetentionReadFromSettingEveryPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, db := newStore(t)
	now := time.UnixMilli(1_800_000_000_000)
	day := 24 * time.Hour
	var recs []genaitrace.CallRecord
	for _, age := range []int{40, 10, 1} {
		recs = append(recs, fullRecord(fmt.Sprintf("age-%d", age), now.Add(-time.Duration(age)*day)))
	}
	if err := s.Insert(ctx, recs); err != nil {
		t.Fatal(err)
	}
	days := 0
	p := modelcalls.NewPruner(s, func() int { return days }, nil)
	modelcalls.SetPrunerClock(p, func() time.Time { return now })

	if n, err := p.PruneOnce(ctx); err != nil || n != 1 {
		t.Fatalf("default (30d) pass deleted %d, %v; want 1", n, err)
	}
	days = 7
	if n, err := p.PruneOnce(ctx); err != nil || n != 1 {
		t.Fatalf("7-day pass deleted %d, %v; want 1", n, err)
	}
	var left string
	if err := db.Reader().QueryRow(ctx, "SELECT group_concat(span_id) FROM model_calls").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != "age-1" {
		t.Fatalf("left %q, want age-1", left)
	}
	for in, want := range map[int]int{0: 30, -3: 30, 7: 7, 99999: modelcalls.MaxRetentionDays} {
		if got := modelcalls.EffectiveRetentionDays(in); got != want {
			t.Errorf("EffectiveRetentionDays(%d) = %d, want %d", in, got, want)
		}
	}
}

// dumpTable renders every value of every model_calls row as text.
func dumpTable(t *testing.T, db storage.DB) string {
	t.Helper()
	rows, err := db.Reader().Query(context.Background(), "SELECT * FROM model_calls")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		vals := make([]sql.NullString, len(genaitrace.RecordColumns)+len(genaitrace.RecordStructuralColumns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan (column count changed? update dumpTable): %v", err)
		}
		for _, v := range vals {
			b.WriteString(v.String)
			b.WriteByte('|')
		}
		b.WriteByte('\n')
	}
	return b.String()
}
