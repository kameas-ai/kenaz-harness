package modelcalls

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// Stats-pull bookkeeping values of model_calls.stats_state.
const (
	StatsNone    = ""        // no pull applies to this call
	StatsFetched = "fetched" // the provider's stats landed
	StatsGaveUp  = "gave_up" // retries exhausted or the pull was refused
)

// DefaultListLimit / MaxListLimit bound List.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// Store reads and writes model_calls on the unified harness DB.
type Store struct {
	db  storage.DB
	now func() time.Time
}

// NewStore returns a Store over db. db must be the opened unified DB (the
// model-calls/1800 migration registered by storage/sqlite.Open).
func NewStore(db storage.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("modelcalls: NewStore: nil db")
	}
	return &Store{db: db, now: time.Now}, nil
}

// Insert writes recs in one transaction. A span id already stored is
// skipped (ON CONFLICT DO NOTHING), so a re-delivered record is harmless.
func (s *Store) Insert(ctx context.Context, recs []genaitrace.CallRecord) error {
	if len(recs) == 0 {
		return nil
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		for _, r := range recs {
			if r.SpanID == "" {
				continue // an unrecorded span has no stable key; never stored
			}
			status := "ok"
			if !r.OK {
				status = "error"
			}
			dur := r.End.Sub(r.Start).Milliseconds()
			if dur < 0 {
				dur = 0
			}
			if _, err := tx.Exec(ctx, `INSERT INTO model_calls (
				span_id, trace_id, started_at_ms, ended_at_ms, duration_ms, status,
				session_hash, provider, request_model, response_model, response_id,
				finish_reasons, error_type, attempt, fallback, stream, max_tokens,
				temperature, input_tokens, output_tokens, cache_read_tokens,
				cache_write_tokens, reasoning_tokens, tool_count, tool_schema_tokens_est,
				ttft_ms, cost_usd, request_id, system_fingerprint, upstream,
				tool_use_prompt_tokens)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(span_id) DO NOTHING`,
				r.SpanID, r.TraceID, r.Start.UnixMilli(), r.End.UnixMilli(), dur, status,
				r.SessionHash, r.Provider, r.RequestModel, r.ResponseModel, r.ResponseID,
				strings.Join(r.FinishReasons, ","), r.ErrorType, r.Attempt, boolInt(r.Fallback),
				boolInt(r.Stream), r.MaxTokens, nullFloat(r.Temperature), r.InputTokens,
				r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.ReasoningTokens,
				r.ToolCount, r.ToolSchemaTokensEst, r.TTFTMS, nullFloat(r.CostUSD),
				r.RequestID, r.SystemFingerprint, r.Upstream, r.ToolUsePromptTokens,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetStatsState records the stats-pull state of the call with spanID.
func (s *Store) SetStatsState(ctx context.Context, spanID, state string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `UPDATE model_calls SET stats_state = ? WHERE span_id = ?`, state, spanID)
		return err
	})
}

// ApplyProviderStats stores the (already allowlist-bounded) provider
// stats on the call with spanID and marks it fetched. The upstream
// provider fills the column only when the stream did not report one.
func (s *Store) ApplyProviderStats(ctx context.Context, spanID string, st genaitrace.ProviderStats) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `UPDATE model_calls SET
			stats_state = ?, stats_fetched_at_ms = ?,
			upstream = CASE WHEN upstream = '' THEN ? ELSE upstream END,
			native_finish_reason = ?,
			native_input_tokens = ?, native_output_tokens = ?,
			native_reasoning_tokens = ?, native_cached_tokens = ?,
			provider_latency_ms = ?, provider_generation_time_ms = ?,
			provider_cost_usd = ?
			WHERE span_id = ?`,
			StatsFetched, s.now().UnixMilli(), st.Upstream, st.NativeFinishReason,
			nullInt(st.NativeInputTokens), nullInt(st.NativeOutputTokens),
			nullInt(st.NativeReasoningTokens), nullInt(st.NativeCachedTokens),
			nullInt(st.LatencyMS), nullInt(st.GenerationTimeMS), nullFloat(st.CostUSD),
			spanID)
		return err
	})
}

// PruneBefore deletes every call that started before cutoff and returns
// how many went.
func (s *Store) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, `DELETE FROM model_calls WHERE started_at_ms < ?`, cutoff.UnixMilli())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// CallView is one stored call as the read-only "Model calls" view shows
// it. Every string is a bounded allowlist value; nothing is content.
type CallView struct {
	SpanID              string   `json:"spanId"`
	StartedAtMS         int64    `json:"startedAtMs"`
	DurationMS          int64    `json:"durationMs"`
	Status              string   `json:"status"`
	Provider            string   `json:"provider"`
	RequestModel        string   `json:"requestModel"`
	ResponseModel       string   `json:"responseModel"`
	ResponseID          string   `json:"responseId"`
	Upstream            string   `json:"upstream"`
	FinishReasons       []string `json:"finishReasons"`
	ErrorType           string   `json:"errorType"`
	Attempt             int64    `json:"attempt"`
	Fallback            bool     `json:"fallback"`
	InputTokens         int64    `json:"inputTokens"`
	OutputTokens        int64    `json:"outputTokens"`
	CacheReadTokens     int64    `json:"cacheReadTokens"`
	CacheWriteTokens    int64    `json:"cacheWriteTokens"`
	ReasoningTokens     int64    `json:"reasoningTokens"`
	TTFTMS              int64    `json:"ttftMs"`
	CostUSD             *float64 `json:"costUsd"`
	StatsState          string   `json:"statsState"`
	ProviderLatencyMS   *int64   `json:"providerLatencyMs"`
	ProviderGenTimeMS   *int64   `json:"providerGenerationTimeMs"`
	ProviderCostUSD     *float64 `json:"providerCostUsd"`
	NativeInputTokens   *int64   `json:"nativeInputTokens"`
	NativeOutputTokens  *int64   `json:"nativeOutputTokens"`
	NativeReasoningToks *int64   `json:"nativeReasoningTokens"`
	NativeCachedTokens  *int64   `json:"nativeCachedTokens"`
	NativeFinishReason  string   `json:"nativeFinishReason"`
}

// ListBySessionHash returns the most recent calls of one session (newest
// first). limit <= 0 means DefaultListLimit; it is capped at MaxListLimit.
func (s *Store) ListBySessionHash(ctx context.Context, sessionHash string, limit int) ([]CallView, error) {
	if sessionHash == "" {
		return []CallView{}, nil
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	rows, err := s.db.Reader().Query(ctx, `SELECT
		span_id, started_at_ms, duration_ms, status, provider, request_model,
		response_model, response_id, upstream, finish_reasons, error_type, attempt,
		fallback, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		reasoning_tokens, ttft_ms, cost_usd, stats_state, provider_latency_ms,
		provider_generation_time_ms, provider_cost_usd, native_input_tokens,
		native_output_tokens, native_reasoning_tokens, native_cached_tokens,
		native_finish_reason
		FROM model_calls WHERE session_hash = ?
		ORDER BY started_at_ms DESC, id DESC LIMIT ?`, sessionHash, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CallView{}
	for rows.Next() {
		var (
			v                                    CallView
			finish                               string
			fallback                             int64
			cost, pcost                          sql.NullFloat64
			plat, pgen, nin, nout, nreas, ncache sql.NullInt64
		)
		if err := rows.Scan(&v.SpanID, &v.StartedAtMS, &v.DurationMS, &v.Status, &v.Provider,
			&v.RequestModel, &v.ResponseModel, &v.ResponseID, &v.Upstream, &finish, &v.ErrorType,
			&v.Attempt, &fallback, &v.InputTokens, &v.OutputTokens, &v.CacheReadTokens,
			&v.CacheWriteTokens, &v.ReasoningTokens, &v.TTFTMS, &cost, &v.StatsState, &plat,
			&pgen, &pcost, &nin, &nout, &nreas, &ncache, &v.NativeFinishReason); err != nil {
			return nil, err
		}
		v.FinishReasons = []string{}
		if finish != "" {
			v.FinishReasons = strings.Split(finish, ",")
		}
		v.Fallback = fallback != 0
		v.CostUSD = floatPtr(cost)
		v.ProviderCostUSD = floatPtr(pcost)
		v.ProviderLatencyMS = intPtr(plat)
		v.ProviderGenTimeMS = intPtr(pgen)
		v.NativeInputTokens = intPtr(nin)
		v.NativeOutputTokens = intPtr(nout)
		v.NativeReasoningToks = intPtr(nreas)
		v.NativeCachedTokens = intPtr(ncache)
		out = append(out, v)
	}
	return out, rows.Err()
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullFloat(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func nullInt(i *int64) any {
	if i == nil {
		return nil
	}
	return *i
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	f := n.Float64
	return &f
}

func intPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	i := n.Int64
	return &i
}
