package genaitrace

// record.go — the typed record of one gen_ai.chat call for the local
// model-call store (llm-call-telemetry-01LLMTEL01 WP02, owner ruling
// 2026-10-09: "the harness collects and stores locally").
//
// The record is built from exactly the attributes the span carries — the
// attrSet output, already validated against SpanAttributes — plus the
// stored-record-only values Instrument hands it directly at call end
// (storedResponseAttrs: the provider response id, which never goes on a
// span), validated through the same allowlist under scope StoredRecord.
// The store can never hold a key or a value the allowlist does not admit. The provider stats
// pull (ProviderStats) goes through the same attrSet, scoped to
// StoredRecord. Mapping a key onto a typed field happens here, inside the
// instrumentation package, because check-genai-span-no-content forbids
// gen_ai.* / kenaz.provider.* literals anywhere else: the store package
// sees typed fields only.

import (
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// CallRecord is one provider call as the local store keeps it. Every
// string field is a bounded value from the allowlist (id, model, token,
// enum or hash); there is no field for content and no free-text field.
type CallRecord struct {
	SpanID  string
	TraceID string
	Start   time.Time
	End     time.Time
	// OK is false for a failed or cancelled call (ErrorType says which).
	OK bool
	// ProfileID routes the provider stats pull to the profile's
	// credential. Never stored, never an attribute.
	ProfileID string

	SessionHash         string
	Provider            string
	RequestModel        string
	ResponseModel       string
	ResponseID          string
	FinishReasons       []string
	ErrorType           string
	Attempt             int64
	Fallback            bool
	Stream              bool
	MaxTokens           int64
	Temperature         *float64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	ReasoningTokens     int64
	ToolCount           int64
	ToolSchemaTokensEst int64
	TTFTMS              int64
	CostUSD             *float64
	RequestID           string
	SystemFingerprint   string
	Upstream            string
	ToolUsePromptTokens int64
}

// RecordColumns maps every column of the local model_calls table that
// holds an attribute value onto its allowlisted key. The store's DDL
// (core/modelcalls/migrations.go) may declare a column only if it is
// listed here or in RecordStructuralColumns —
// check-genai-span-no-content.sh parses both and fails the build on any
// other column, so the store cannot grow a content column the span
// allowlist would refuse.
var RecordColumns = []RecordColumn{
	{Column: "session_hash", Key: "kenaz.session.hash"},
	{Column: "provider", Key: "gen_ai.provider.name"},
	{Column: "request_model", Key: "gen_ai.request.model"},
	{Column: "response_model", Key: "gen_ai.response.model"},
	{Column: "response_id", Key: "gen_ai.response.id"},
	{Column: "finish_reasons", Key: "gen_ai.response.finish_reasons"},
	{Column: "error_type", Key: "error.type"},
	{Column: "attempt", Key: "kenaz.llm.attempt"},
	{Column: "fallback", Key: "kenaz.llm.fallback"},
	{Column: "stream", Key: "kenaz.llm.stream"},
	{Column: "max_tokens", Key: "gen_ai.request.max_tokens"},
	{Column: "temperature", Key: "gen_ai.request.temperature"},
	{Column: "input_tokens", Key: "gen_ai.usage.input_tokens"},
	{Column: "output_tokens", Key: "gen_ai.usage.output_tokens"},
	{Column: "cache_read_tokens", Key: "gen_ai.usage.cache_read.input_tokens"},
	{Column: "cache_write_tokens", Key: "gen_ai.usage.cache_creation.input_tokens"},
	{Column: "reasoning_tokens", Key: "kenaz.llm.reasoning_tokens"},
	{Column: "tool_count", Key: "kenaz.llm.tool_count"},
	{Column: "tool_schema_tokens_est", Key: "kenaz.llm.tool_schema_tokens_est"},
	{Column: "ttft_ms", Key: "kenaz.llm.ttft_ms"},
	{Column: "cost_usd", Key: "kenaz.llm.cost_usd"},
	{Column: "request_id", Key: "kenaz.provider.request_id"},
	{Column: "system_fingerprint", Key: "kenaz.provider.system_fingerprint"},
	{Column: "upstream", Key: "kenaz.provider.upstream"},
	{Column: "tool_use_prompt_tokens", Key: "kenaz.provider.tool_use_prompt_tokens"},
	{Column: "provider_cost_usd", Key: "kenaz.provider.cost_usd"},
	{Column: "provider_generation_time_ms", Key: "kenaz.provider.generation_time_ms"},
	{Column: "provider_latency_ms", Key: "kenaz.provider.latency_ms"},
	{Column: "native_cached_tokens", Key: "kenaz.provider.native_cached_tokens"},
	{Column: "native_finish_reason", Key: "kenaz.provider.native_finish_reason"},
	{Column: "native_input_tokens", Key: "kenaz.provider.native_input_tokens"},
	{Column: "native_output_tokens", Key: "kenaz.provider.native_output_tokens"},
	{Column: "native_reasoning_tokens", Key: "kenaz.provider.native_reasoning_tokens"},
}

// RecordStructuralColumns are the model_calls columns that hold no
// attribute: the row id, the span/trace ids, timestamps, the derived
// duration, the ok/error status and the stats-pull bookkeeping. Each is a
// fixed-format value the store computes, never provider or user text.
var RecordStructuralColumns = []string{
	"id", "span_id", "trace_id", "started_at_ms", "ended_at_ms", "duration_ms",
	"status", "stats_state", "stats_fetched_at_ms",
}

// RecordColumn binds one model_calls column to its allowlisted key.
type RecordColumn struct {
	Column string
	Key    string
}

// Recorder receives every finished gen_ai.chat call. RecordCall runs on
// the caller's goroutine at span end (Final / Cancel / a failed open), so
// it must not block: the production recorder (core/modelcalls) enqueues on
// a bounded queue and counts drops.
type Recorder interface {
	RecordCall(CallRecord)
}

var recorder atomic.Pointer[Recorder]

// SetRecorder installs r as the process's model-call recorder and returns
// a func restoring the previous one. nil uninstalls.
func SetRecorder(r Recorder) (restore func()) {
	var next *Recorder
	if r != nil {
		next = &r
	}
	prev := recorder.Swap(next)
	return func() { recorder.Store(prev) }
}

// recordCall hands the finished call to the installed recorder, if any.
// endKV is what the span got at its end; storedKV is the StoredRecord-
// scoped set that never touched the span.
func recordCall(c *callEnd, endKV, storedKV []attribute.KeyValue, end time.Time, ok bool) {
	rp := recorder.Load()
	if rp == nil || *rp == nil {
		return
	}
	rec := CallRecord{Start: c.start, End: end, OK: ok, ProfileID: c.profileID}
	if sc := c.span.SpanContext(); sc.IsValid() {
		rec.SpanID = sc.SpanID().String()
		rec.TraceID = sc.TraceID().String()
	}
	for _, kv := range c.startKV {
		rec.apply(kv)
	}
	for _, kv := range endKV {
		rec.apply(kv)
	}
	for _, kv := range storedKV {
		rec.apply(kv)
	}
	(*rp).RecordCall(rec)
}

// apply copies one validated attribute onto its typed field. Keys not
// listed here are ignored.
func (r *CallRecord) apply(kv attribute.KeyValue) {
	v := kv.Value
	switch string(kv.Key) {
	case "kenaz.session.hash":
		r.SessionHash = v.AsString()
	case "gen_ai.provider.name":
		r.Provider = v.AsString()
	case "gen_ai.request.model":
		r.RequestModel = v.AsString()
	case "gen_ai.response.model":
		r.ResponseModel = v.AsString()
	case "gen_ai.response.id":
		r.ResponseID = v.AsString()
	case "gen_ai.response.finish_reasons":
		r.FinishReasons = append([]string(nil), v.AsStringSlice()...)
	case "error.type":
		r.ErrorType = v.AsString()
	case "kenaz.llm.attempt":
		r.Attempt = v.AsInt64()
	case "kenaz.llm.fallback":
		r.Fallback = v.AsBool()
	case "kenaz.llm.stream":
		r.Stream = v.AsBool()
	case "gen_ai.request.max_tokens":
		r.MaxTokens = v.AsInt64()
	case "gen_ai.request.temperature":
		f := v.AsFloat64()
		r.Temperature = &f
	case "gen_ai.usage.input_tokens":
		r.InputTokens = v.AsInt64()
	case "gen_ai.usage.output_tokens":
		r.OutputTokens = v.AsInt64()
	case "gen_ai.usage.cache_read.input_tokens":
		r.CacheReadTokens = v.AsInt64()
	case "gen_ai.usage.cache_creation.input_tokens":
		r.CacheWriteTokens = v.AsInt64()
	case "kenaz.llm.reasoning_tokens":
		r.ReasoningTokens = v.AsInt64()
	case "kenaz.llm.tool_count":
		r.ToolCount = v.AsInt64()
	case "kenaz.llm.tool_schema_tokens_est":
		r.ToolSchemaTokensEst = v.AsInt64()
	case "kenaz.llm.ttft_ms":
		r.TTFTMS = v.AsInt64()
	case "kenaz.llm.cost_usd":
		f := v.AsFloat64()
		r.CostUSD = &f
	case "kenaz.provider.request_id":
		r.RequestID = v.AsString()
	case "kenaz.provider.system_fingerprint":
		r.SystemFingerprint = v.AsString()
	case "kenaz.provider.upstream":
		r.Upstream = v.AsString()
	case "kenaz.provider.tool_use_prompt_tokens":
		r.ToolUsePromptTokens = v.AsInt64()
	}
}

// RawProviderStats is what a provider's per-generation stats endpoint
// returned, decoded but NOT yet validated. It carries only numeric fields
// and two provider enum tokens — the decoder never reads a text field of
// the response.
type RawProviderStats struct {
	Upstream              string
	NativeFinishReason    string
	NativeInputTokens     *int64
	NativeOutputTokens    *int64
	NativeReasoningTokens *int64
	NativeCachedTokens    *int64
	LatencyMS             *float64
	GenerationTimeMS      *float64
	CostUSD               *float64
}

// ProviderStats is RawProviderStats after the allowlist: every field that
// survived its bound. A nil / empty field was absent or out of bound.
type ProviderStats struct {
	Upstream              string
	NativeFinishReason    string
	NativeInputTokens     *int64
	NativeOutputTokens    *int64
	NativeReasoningTokens *int64
	NativeCachedTokens    *int64
	LatencyMS             *int64
	GenerationTimeMS      *int64
	CostUSD               *float64
}

// BoundProviderStats validates raw against the allowlist (scope
// StoredRecord) and returns what survived. Out-of-bound values are
// dropped and counted in DroppedAttributes, exactly like span attributes.
func BoundProviderStats(raw RawProviderStats) ProviderStats {
	a := attrSet{scope: StoredRecord}
	a.str("kenaz.provider.upstream", raw.Upstream)
	a.str("kenaz.provider.native_finish_reason", raw.NativeFinishReason)
	optInt := func(key string, v *int64) {
		if v != nil {
			a.int(key, *v)
		}
	}
	optInt("kenaz.provider.native_input_tokens", raw.NativeInputTokens)
	optInt("kenaz.provider.native_output_tokens", raw.NativeOutputTokens)
	optInt("kenaz.provider.native_reasoning_tokens", raw.NativeReasoningTokens)
	optInt("kenaz.provider.native_cached_tokens", raw.NativeCachedTokens)
	if raw.LatencyMS != nil {
		a.int("kenaz.provider.latency_ms", int64(*raw.LatencyMS))
	}
	if raw.GenerationTimeMS != nil {
		a.int("kenaz.provider.generation_time_ms", int64(*raw.GenerationTimeMS))
	}
	if raw.CostUSD != nil {
		a.float("kenaz.provider.cost_usd", *raw.CostUSD)
	}
	var out ProviderStats
	for _, kv := range a.kv {
		v := kv.Value
		i := v.AsInt64()
		switch string(kv.Key) {
		case "kenaz.provider.upstream":
			out.Upstream = v.AsString()
		case "kenaz.provider.native_finish_reason":
			out.NativeFinishReason = v.AsString()
		case "kenaz.provider.native_input_tokens":
			out.NativeInputTokens = &i
		case "kenaz.provider.native_output_tokens":
			out.NativeOutputTokens = &i
		case "kenaz.provider.native_reasoning_tokens":
			out.NativeReasoningTokens = &i
		case "kenaz.provider.native_cached_tokens":
			out.NativeCachedTokens = &i
		case "kenaz.provider.latency_ms":
			out.LatencyMS = &i
		case "kenaz.provider.generation_time_ms":
			out.GenerationTimeMS = &i
		case "kenaz.provider.cost_usd":
			f := v.AsFloat64()
			out.CostUSD = &f
		}
	}
	return out
}
