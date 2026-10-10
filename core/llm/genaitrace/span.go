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
// instrumentation scope (ScopeName) until the explicit, default-off
// "Share model-call telemetry with Fleet" opt-in lands (WP02); general
// telemetry consent does not carry them.
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

type attrSet struct{ kv []attribute.KeyValue }

func (a *attrSet) spec(key string, t AttrType) (AttrSpec, bool) {
	s, ok := specByKey[key]
	if !ok || s.Type != t {
		droppedAttrs.Add(1)
		return AttrSpec{}, false
	}
	return s, true
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

// sessionKey keys the session hash. Per process: two spans of one session
// in one run share a hash; nothing outside this process can reverse or
// recompute it, and the raw id never leaves.
var sessionKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		// crypto/rand cannot fail on supported platforms; a nil key would
		// still be a keyed construction, just a known key — refuse that.
		panic("genaitrace: crypto/rand: " + err.Error())
	}
	return k
}()

// SessionHash returns the keyed hash emitted as kenaz.session.hash, or ""
// for an empty id.
func SessionHash(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	m := hmac.New(sha256.New, sessionKey)
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
	var a attrSet
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
	var a attrSet
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

	s, err := open(ctx)
	if err != nil {
		endWithError(span, err)
		return nil, err
	}
	return &tracedStream{inner: s, span: span, start: start, cost: cost}, nil
}

func endWithError(span trace.Span, err error) {
	var a attrSet
	a.str("error.type", ErrorType(err))
	span.SetAttributes(a.kv...)
	// Code only: Fleet drops Status.Message, and a provider message is
	// text this span must not carry anyway.
	span.SetStatus(codes.Error, "")
	span.End()
}

// tracedStream ends its gen_ai.chat span exactly once, at Final() or
// Cancel().
type tracedStream struct {
	inner llm.Stream
	span  trace.Span
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
	s.endOnce.Do(func() { endWithError(s.span, &llm.ErrCancelled{Reason: "caller"}) })
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
			endWithError(s.span, err)
			return
		}
		s.span.SetAttributes(responseAttrs(resp, resp.Cost, s.ttftNS.Load()).kv...)
		s.span.End()
	})
	return resp, err
}

// responseAttrs maps a final response onto the allowlist.
func responseAttrs(resp llm.Response, cost llm.Cost, ttftNS int64) attrSet {
	var a attrSet
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
