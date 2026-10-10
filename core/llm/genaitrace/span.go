// Package genaitrace emits one OpenTelemetry GenAI-semconv span per
// model provider call (llm-call-telemetry-01LLMTEL01 WP01) — the
// OpenLLMetry conventions, implemented directly: there is no Go
// OpenLLMetry SDK, and the harness pulls in neither the Python nor the JS
// one.
//
// # The choke point
//
// Every provider call the harness makes goes through registry.Stream's
// retry closure, which calls ProviderAdapter.Stream once per attempt. That
// closure (and the structured-output repair's) wraps the adapter call in
// Instrument, so each attempt — first try, retry, fallback hop, repair —
// is one gen_ai.chat span, and no adapter touches OTel. Adapters only map
// provider-returned extras onto llm.CallMeta (Response.Meta), which
// Instrument reads at Final().
//
// # Never content
//
// Attributes are set only through attrSet, against the SpanAttributes
// allowlist (attrs.go): ids, model ids, closed enums, counts. No prompt,
// completion, tool argument, tool result or system text is ever set, and
// no span event, link or status description carries data. The CI gate
// check-genai-span-no-content.sh pins it.
//
// # Where spans go
//
// Through the process TracerProvider: the local exporter always. The
// Fleet span lane (core/fleet closableSpanExporter) drops this package's
// instrumentation scope (ScopeName) unless the explicit, default-off
// "Share model-call telemetry with Fleet" setting is on (WP02), and then
// only spans started after it was turned on; general telemetry consent
// does not carry them.
//
// Every finished gen_ai.chat call is also handed, with the same
// attributes, to the installed Recorder (record.go) — the durable local
// model-call store, core/modelcalls (WP02).
package genaitrace

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// ScopeName is the instrumentation scope of every span this package
// emits. The Fleet span lane keys its model-call filter on it.
const ScopeName = "github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"

func tracer() trace.Tracer { return otel.Tracer(ScopeName) }

// ── attrSet: the only attribute writer ───────────────────────────────────────

// droppedAttrs counts attributes refused by attrSet (undeclared key or
// out-of-bound value). Tests assert it stays zero for every fixture.
var droppedAttrs atomic.Int64

// DroppedAttributes reports how many attributes attrSet has refused since
// process start (test/diagnostic seam; payload-free).
func DroppedAttributes() int64 { return droppedAttrs.Load() }

// attrSet accumulates validated attributes. scope, when set, names the
// span (or StoredRecord) the attributes are for: a key whose Spans list
// does not name it is refused, so a stats-pull-only key can never land on
// a span and a chat-only key can never land on the turn span.
type attrSet struct {
	kv    []attribute.KeyValue
	scope string
}

func (a *attrSet) spec(key string, t AttrType) (AttrSpec, bool) {
	s, ok := specByKey[key]
	if !ok || s.Type != t || (a.scope != "" && !s.carriedOn(a.scope)) {
		droppedAttrs.Add(1)
		return AttrSpec{}, false
	}
	return s, true
}

// carriedOn reports whether the spec lists scope in Spans.
func (s AttrSpec) carriedOn(scope string) bool {
	for _, sp := range s.Spans {
		if sp == scope {
			return true
		}
	}
	return false
}

func (a *attrSet) str(key, v string) {
	if v == "" {
		return
	}
	s, ok := a.spec(key, TypeString)
	if !ok {
		return
	}
	out, ok := boundString(s.Class, s.Enum, v)
	if !ok {
		droppedAttrs.Add(1)
		return
	}
	a.kv = append(a.kv, attribute.String(key, out))
}

func (a *attrSet) strs(key string, vs []string) {
	s, ok := a.spec(key, TypeStringSlice)
	if !ok {
		return
	}
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		if v == "" {
			continue
		}
		b, ok := boundString(s.Class, s.Enum, v)
		if !ok {
			droppedAttrs.Add(1)
			continue
		}
		out = append(out, b)
	}
	if len(out) > 0 {
		a.kv = append(a.kv, attribute.StringSlice(key, out))
	}
}

func (a *attrSet) int(key string, v int64) {
	s, ok := a.spec(key, TypeInt)
	if !ok {
		return
	}
	if float64(v) < s.Min || float64(v) > s.Max {
		droppedAttrs.Add(1)
		return
	}
	a.kv = append(a.kv, attribute.Int64(key, v))
}

// posInt sets key only when v > 0 (a zero count means "not reported").
func (a *attrSet) posInt(key string, v int) {
	if v > 0 {
		a.int(key, int64(v))
	}
}

func (a *attrSet) float(key string, v float64) {
	s, ok := a.spec(key, TypeFloat)
	if !ok {
		return
	}
	if v < s.Min || v > s.Max || v != v {
		droppedAttrs.Add(1)
		return
	}
	a.kv = append(a.kv, attribute.Float64(key, v))
}

func (a *attrSet) bool(key string, v bool) {
	if _, ok := a.spec(key, TypeBool); !ok {
		return
	}
	a.kv = append(a.kv, attribute.Bool(key, v))
}

// ── session hash ─────────────────────────────────────────────────────────────

// sessionKey keys the session hash. It starts as a per-process random key
// and is replaced at boot by the per-install key SetSessionHashKey loads
// from <dataDir>/telemetry/session_hash_key (LoadOrCreateSessionHashKey),
// so one session hashes the same across restarts — the local model-call
// store queries by it. The key never leaves the machine, so nothing
// outside it can reverse or recompute a hash, and the raw id never leaves.
var sessionKey atomic.Pointer[[]byte]

func init() {
	k := make([]byte, sessionHashKeyBytes)
	if _, err := rand.Read(k); err != nil {
		// crypto/rand cannot fail on supported platforms; a nil key would
		// still be a keyed construction, just a known key — refuse that.
		panic("genaitrace: crypto/rand: " + err.Error())
	}
	sessionKey.Store(&k)
}

// SessionHash returns the keyed hash emitted as kenaz.session.hash, or ""
// for an empty id.
func SessionHash(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	m := hmac.New(sha256.New, *sessionKey.Load())
	m.Write([]byte(sessionID))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// ── per-request context: attempt sequence + fallback flag ───────────────────

type seqKey struct{}
type fallbackKey struct{}
type sessionKeyCtx struct{}

// callSeq numbers the provider calls of one logical model request.
type callSeq struct{ n atomic.Int64 }

// WithCallSequence installs an attempt counter on ctx unless one is
// already there. Every Instrument call under ctx takes the next index, so
// kenaz.llm.attempt counts across the registry's retries, the chat
// layer's retries and fallback hops of one model request.
func WithCallSequence(ctx context.Context) context.Context {
	if _, ok := ctx.Value(seqKey{}).(*callSeq); ok {
		return ctx
	}
	return context.WithValue(ctx, seqKey{}, &callSeq{})
}

func nextAttempt(ctx context.Context) int64 {
	if s, ok := ctx.Value(seqKey{}).(*callSeq); ok {
		return s.n.Add(1) - 1
	}
	return 0
}

// WithFallback marks ctx as a model-fallback hop (kenaz.llm.fallback).
func WithFallback(ctx context.Context) context.Context {
	return context.WithValue(ctx, fallbackKey{}, true)
}

func isFallback(ctx context.Context) bool {
	v, _ := ctx.Value(fallbackKey{}).(bool)
	return v
}

// ── turn span ────────────────────────────────────────────────────────────────

// StartTurn opens the kenaz.chat.turn span every gen_ai.chat span of the
// turn nests under, and carries the session id on ctx so those calls hash
// it into kenaz.session.hash without it riding on the GenerationRequest.
// The caller ends the span when the turn's run exits.
func StartTurn(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	a := attrSet{scope: SpanNameTurn}
	a.str("kenaz.session.hash", SessionHash(sessionID))
	if sessionID != "" {
		ctx = context.WithValue(ctx, sessionKeyCtx{}, sessionID)
	}
	return tracer().Start(ctx, SpanNameTurn,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(a.kv...))
}

// ── the provider call ────────────────────────────────────────────────────────

// Call is the request-side description of one provider call: only the
// shape, never the content.
type Call struct {
	ProviderKind       string
	GeminiEndpointKind string
	RequestModel       string
	MaxTokens          int
	Temperature        *float64
	ToolCount          int
	ToolSchemaTokens   int
	SessionID          string
	// ProfileID routes the provider stats pull (WP02) to the profile's
	// credential. It is never an attribute and never stored.
	ProfileID string
}

// CallFromRequest builds the Call for req dispatched on prof (prof.Model
// already resolved to the model that will reach the wire).
func CallFromRequest(req llm.GenerationRequest, prof llm.ProviderProfile) Call {
	c := Call{
		ProviderKind:       prof.Kind,
		GeminiEndpointKind: prof.GeminiEndpointKind,
		RequestModel:       prof.Model,
		ToolCount:          len(req.Tools),
		SessionID:          req.SessionID,
		ProfileID:          prof.ID,
	}
	if len(req.Tools) > 0 {
		c.ToolSchemaTokens = llm.ToolsTokens(req.Tools)
	}
	if v, ok := numParam(req.Params, "max_tokens"); ok && v > 0 {
		c.MaxTokens = int(v)
	} else if req.Knobs != nil && req.Knobs.MaxTokens > 0 {
		c.MaxTokens = req.Knobs.MaxTokens
	}
	if v, ok := numParam(req.Params, "temperature"); ok {
		c.Temperature = &v
	} else if req.Knobs != nil && req.Knobs.Temperature != nil {
		t := *req.Knobs.Temperature
		c.Temperature = &t
	}
	return c
}

func numParam(m map[string]any, k string) (float64, bool) {
	switch v := m[k].(type) {
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// ProviderName maps an adapter kind onto the closed gen_ai.provider.name
// set.
func ProviderName(kind, geminiEndpointKind string) string {
	switch kind {
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	case "openrouter":
		return "openrouter"
	case "azure-openai":
		return "azure.ai.openai"
	case "gemini":
		if geminiEndpointKind == "vertex" {
			return "gcp.vertex_ai"
		}
		return "gcp.gemini"
	case "bedrock":
		return "aws.bedrock"
	case "ollama":
		return "ollama"
	case "custom-openai":
		return "openai_compatible"
	}
	return "other"
}

// ErrorType maps a call error onto the closed error.type set.
func ErrorType(err error) string {
	if err == nil {
		return ""
	}
	var c *llm.ErrCancelled
	if errors.As(err, &c) || errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return llm.ClassifyFailure(err, "").Code
}

// CostFunc derives the cost readout for a final response.
type CostFunc func(llm.Response) llm.Cost

// Instrument runs open — one ProviderAdapter.Stream call — inside a
// gen_ai.chat span. A failed open ends the span at once with error.type;
// a successful one returns a Stream whose Final()/Cancel() ends it with
// usage, finish reasons, provider extras and cost. Span work happens off
// the event path: the only per-chunk cost is one channel hop and, until
// the first content chunk, a timestamp.
func Instrument(ctx context.Context, call Call, cost CostFunc, open func(context.Context) (llm.Stream, error)) (llm.Stream, error) {
	start := time.Now()
	a := attrSet{scope: SpanNameChat}
	provider := ProviderName(call.ProviderKind, call.GeminiEndpointKind)
	a.str("gen_ai.operation.name", "chat")
	a.str("gen_ai.provider.name", provider)
	a.str("gen_ai.system", provider)
	a.str("gen_ai.request.model", call.RequestModel)
	if call.MaxTokens > 0 {
		a.int("gen_ai.request.max_tokens", int64(call.MaxTokens))
	}
	if call.Temperature != nil {
		a.float("gen_ai.request.temperature", *call.Temperature)
	}
	a.int("kenaz.llm.attempt", nextAttempt(ctx))
	a.bool("kenaz.llm.fallback", isFallback(ctx))
	a.bool("kenaz.llm.stream", true)
	a.int("kenaz.llm.tool_count", int64(call.ToolCount))
	a.int("kenaz.llm.tool_schema_tokens_est", int64(call.ToolSchemaTokens))
	sessionID := call.SessionID
	if sessionID == "" {
		sessionID, _ = ctx.Value(sessionKeyCtx{}).(string)
	}
	a.str("kenaz.session.hash", SessionHash(sessionID))

	ctx, span := tracer().Start(ctx, SpanNameChat,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(start),
		trace.WithAttributes(a.kv...))

	c := &callEnd{span: span, start: start, startKV: a.kv, profileID: call.ProfileID}
	s, err := open(ctx)
	if err != nil {
		c.endWithError(err)
		return nil, err
	}
	return &tracedStream{inner: s, end: c, start: start, cost: cost}, nil
}

// callEnd ends one gen_ai.chat span and hands the same attributes to the
// local model-call recorder (record.go).
type callEnd struct {
	span      trace.Span
	start     time.Time
	startKV   []attribute.KeyValue
	profileID string
}

func (c *callEnd) endWithError(err error) {
	a := attrSet{scope: SpanNameChat}
	a.str("error.type", ErrorType(err))
	c.span.SetAttributes(a.kv...)
	// Code only: Fleet drops Status.Message, and a provider message is
	// text this span must not carry anyway.
	c.span.SetStatus(codes.Error, "")
	c.finish(a.kv, false)
}

// finish ends the span at one timestamp and records the call.
func (c *callEnd) finish(endKV []attribute.KeyValue, ok bool) {
	end := time.Now()
	c.span.End(trace.WithTimestamp(end))
	recordCall(c, endKV, end, ok)
}

// tracedStream ends its gen_ai.chat span exactly once, at Final() or
// Cancel().
type tracedStream struct {
	inner llm.Stream
	end   *callEnd
	start time.Time
	cost  CostFunc

	pumpOnce sync.Once
	out      chan llm.StreamEvent
	ttftNS   atomic.Int64 // 0 until the first content chunk

	endOnce sync.Once

	costOnce  sync.Once
	finalCost llm.Cost
}

func (s *tracedStream) Events() <-chan llm.StreamEvent {
	s.pumpOnce.Do(func() {
		s.out = make(chan llm.StreamEvent, 16)
		go s.pump()
	})
	return s.out
}

func (s *tracedStream) pump() {
	defer close(s.out)
	for ev := range s.inner.Events() {
		if s.ttftNS.Load() == 0 {
			switch ev.Kind {
			case llm.StreamText, llm.StreamTool, llm.StreamReasoning:
				s.ttftNS.Store(int64(time.Since(s.start)) | 1) // |1: never 0
			}
		}
		s.out <- ev
	}
}

func (s *tracedStream) Cancel() error {
	err := s.inner.Cancel()
	s.endOnce.Do(func() { s.end.endWithError(&llm.ErrCancelled{Reason: "caller"}) })
	return err
}

func (s *tracedStream) Final() (llm.Response, error) {
	resp, err := s.inner.Final()
	if err == nil && s.cost != nil {
		// Stamp the cost readout onto the response so the caller reports
		// the number the span does, derived once however often Final is
		// called.
		s.costOnce.Do(func() { s.finalCost = s.cost(resp) })
		resp.Cost = s.finalCost
	}
	s.endOnce.Do(func() {
		if err != nil {
			s.end.endWithError(err)
			return
		}
		kv := responseAttrs(resp, resp.Cost, s.ttftNS.Load()).kv
		s.end.span.SetAttributes(kv...)
		s.end.finish(kv, true)
	})
	return resp, err
}

// responseAttrs maps a final response onto the allowlist.
func responseAttrs(resp llm.Response, cost llm.Cost, ttftNS int64) attrSet {
	a := attrSet{scope: SpanNameChat}
	u := resp.Usage
	a.int("gen_ai.usage.input_tokens", int64(u.InputTokens))
	a.int("gen_ai.usage.output_tokens", int64(u.OutputTokens))
	cacheRead := u.CachedInputRead
	if cacheRead == 0 && resp.Meta != nil {
		cacheRead = resp.Meta.CacheReadTokens
	}
	a.posInt("gen_ai.usage.cache_read.input_tokens", cacheRead)
	a.posInt("gen_ai.usage.cache_creation.input_tokens", u.CachedInputWrite)
	a.posInt("kenaz.llm.reasoning_tokens", u.ReasoningTokens)
	if resp.FinishReason != "" {
		a.strs("gen_ai.response.finish_reasons", []string{resp.FinishReason})
	}
	if ttftNS > 0 {
		a.int("kenaz.llm.ttft_ms", time.Duration(ttftNS).Milliseconds())
	}
	if !cost.Indeterminate && cost.Total > 0 && (cost.Currency == "" || cost.Currency == "USD") {
		a.float("kenaz.llm.cost_usd", cost.Total)
	}
	if m := resp.Meta; m != nil {
		a.str("gen_ai.response.id", m.ResponseID)
		a.str("gen_ai.response.model", m.ResponseModel)
		a.str("kenaz.provider.request_id", m.RequestID)
		a.str("kenaz.provider.system_fingerprint", m.SystemFingerprint)
		a.str("kenaz.provider.upstream", m.UpstreamProvider)
		a.posInt("kenaz.provider.tool_use_prompt_tokens", m.ToolUsePromptTokens)
	}
	return a
}
