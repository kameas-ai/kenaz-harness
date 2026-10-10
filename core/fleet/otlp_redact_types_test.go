package fleet

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestRedactedSpan_PreservesAttributeTypes: the Fleet-bound redaction
// decorator redacts string values and blanks sensitive keys, and leaves
// every other attribute with its OTLP type. It used to stringify every
// value (Value.Emit), so an int reached Fleet as "200" and a string[] as
// one "[stop]" string (llm-call-telemetry-01LLMTEL01 WP01).
func TestRedactedSpan_PreservesAttributeTypes(t *testing.T) {
	const key = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAA-BBBBBBBBBBBBBBBBBBBBBBBB"
	ro := tracetest.SpanStub{Name: "harness.task.complete", Attributes: []attribute.KeyValue{
		attribute.Int("task.exit_code", 2),
		attribute.Int64("task.duration_ms", 1500),
		attribute.Bool("harness.hook.async", true),
		attribute.Float64("kenaz.llm.cost_usd", 0.25),
		attribute.StringSlice("gen_ai.response.finish_reasons", []string{"stop", key}),
		attribute.String("task.kind", "shell "+key),
		attribute.Int("token", 7),
	}}.Snapshot()

	got := map[string]attribute.Value{}
	for _, kv := range (&redactedSpan{ReadOnlySpan: ro}).Attributes() {
		got[string(kv.Key)] = kv.Value
	}
	if v := got["task.exit_code"]; v.Type() != attribute.INT64 || v.AsInt64() != 2 {
		t.Errorf("task.exit_code = %v (%v), want int 2", v.Emit(), v.Type())
	}
	if v := got["task.duration_ms"]; v.Type() != attribute.INT64 || v.AsInt64() != 1500 {
		t.Errorf("task.duration_ms = %v (%v)", v.Emit(), v.Type())
	}
	if v := got["harness.hook.async"]; v.Type() != attribute.BOOL || !v.AsBool() {
		t.Errorf("harness.hook.async = %v (%v)", v.Emit(), v.Type())
	}
	if v := got["kenaz.llm.cost_usd"]; v.Type() != attribute.FLOAT64 || v.AsFloat64() != 0.25 {
		t.Errorf("kenaz.llm.cost_usd = %v (%v)", v.Emit(), v.Type())
	}
	fr := got["gen_ai.response.finish_reasons"]
	if fr.Type() != attribute.STRINGSLICE || len(fr.AsStringSlice()) != 2 || fr.AsStringSlice()[0] != "stop" ||
		fr.AsStringSlice()[1] != "[REDACTED:anthropic-key]" {
		t.Errorf("finish_reasons = %v (%v), want [stop, redacted]", fr.Emit(), fr.Type())
	}
	if v := got["task.kind"]; v.AsString() != "shell [REDACTED:anthropic-key]" {
		t.Errorf("task.kind = %q, want the key redacted", v.AsString())
	}
	if v := got["token"]; v.AsString() != "[REDACTED]" {
		t.Errorf("sensitive key token = %v, want [REDACTED]", v.Emit())
	}
}
