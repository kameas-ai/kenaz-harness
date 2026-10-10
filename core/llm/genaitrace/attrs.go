package genaitrace

// attrs.go — THE attribute allowlist for the gen_ai.chat and
// kenaz.chat.turn spans (llm-call-telemetry-01LLMTEL01 WP01).
//
// Every attribute either span carries is declared in SpanAttributes below,
// with its type and its value bound. Three things read this one list:
//
//  1. attrSet (span.go), the only way this package sets an attribute: an
//     undeclared key is dropped, and a value outside its bound is dropped,
//     so a span can never carry anything this list does not describe.
//  2. scripts/ci/cmd/checkgenaispan (check-genai-span-no-content.sh), which
//     parses this literal and fails the build on a content key
//     (gen_ai.prompt*, gen_ai.completion*, *.content, *.message*,
//     *.arguments, *.result) or on a string attribute with no bound.
//  3. TestSpanAttributeDocInSync, which renders this list into
//     docs/telemetry/gen-ai-span-attributes.md — the table Fleet copies
//     into its receiver's span allowlist — and fails on drift.
//
// There is no free-text class. A string attribute is an id, a model id,
// a provider token, a closed enum or a keyed hash; nothing else can be
// declared, and the gate rejects any other class.
//
// Key names follow the OpenTelemetry GenAI semantic conventions pinned at
// SemconvSchemaURL; attrs_semconv_test.go cross-checks every gen_ai.* key
// against the vendored semconv package so a typo cannot ship.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// SemconvSchemaURL pins the GenAI semantic-conventions version the key
// names follow (v1.40.0: gen_ai.provider.name, the cache_read /
// cache_creation usage keys).
const SemconvSchemaURL = semconv.SchemaURL

// Span names. Fleet's receiver drops any span whose NAME it has not
// declared, so both names are fixed strings.
//
// SpanNameChat deliberately deviates from the semconv recommendation
// ("{gen_ai.operation.name} {gen_ai.request.model}", e.g. "chat
// claude-sonnet-4-5"): a model-bearing span name is an unbounded name
// set no allowlist can declare. The model travels in gen_ai.request.model
// instead.
const (
	SpanNameChat = "gen_ai.chat"
	SpanNameTurn = "kenaz.chat.turn"
)

// AttrType is the OTel attribute value type.
type AttrType string

const (
	TypeString      AttrType = "string"
	TypeInt         AttrType = "int"
	TypeFloat       AttrType = "float"
	TypeBool        AttrType = "bool"
	TypeStringSlice AttrType = "string[]"
)

// ValueClass bounds a string attribute's value. There is deliberately no
// free-text class.
type ValueClass string

const (
	// ClassNone is for non-string types; their bound is Min/Max.
	ClassNone ValueClass = ""
	// ClassID is a provider-issued identifier: ≤128 chars of
	// [A-Za-z0-9._:/@+=-].
	ClassID ValueClass = "id"
	// ClassModel is a model or provider id: ≤128 chars of
	// [A-Za-z0-9._:/@+=-].
	ClassModel ValueClass = "model"
	// ClassToken is a provider-reported enum token (a finish reason, an
	// upstream provider name), normalised to lower case with spaces as
	// '-': ≤64 chars of [a-z0-9._-].
	ClassToken ValueClass = "token"
	// ClassEnum is a closed set the harness itself defines (Enum).
	ClassEnum ValueClass = "enum"
	// ClassHash is a keyed hash: exactly 16 lowercase hex chars.
	ClassHash ValueClass = "hash"
)

// AttrSpec declares one attribute.
type AttrSpec struct {
	Key   string
	Type  AttrType
	Class ValueClass
	// Enum is the closed value set for ClassEnum.
	Enum []string
	// Min / Max bound TypeInt and TypeFloat values (inclusive).
	Min, Max float64
	// Spans names the span(s) that carry the attribute.
	Spans []string
	// Doc is a one-line description for the generated table.
	Doc string
}

const maxInt = 1 << 40 // ~1.1e12: no token count, latency or index reaches it

var (
	chatOnly  = []string{SpanNameChat}
	bothSpans = []string{SpanNameChat, SpanNameTurn}
)

// ProviderNames is the closed gen_ai.provider.name / gen_ai.system value
// set (semconv well-known values plus the harness's own kinds). A provider
// kind the harness does not know maps to "other".
var ProviderNames = []string{
	"anthropic", "aws.bedrock", "azure.ai.openai", "gcp.gemini", "gcp.vertex_ai",
	"ollama", "openai", "openai_compatible", "openrouter", "other",
}

// ErrorTypes is the closed error.type value set: the typed failure codes
// of llm.ClassifyFailure plus "cancelled". Never message text.
var ErrorTypes = []string{
	"auth_invalid", "cancelled", "forbidden", "invalid_request", "model_not_found",
	"network", "payment_required", "provider_unavailable", "rate_limited",
	"timeout", "unknown", "unsupported",
}

// SpanAttributes is the allowlist. Keep it sorted by key.
var SpanAttributes = []AttrSpec{
	{Key: "error.type", Type: TypeString, Class: ClassEnum, Enum: ErrorTypes, Spans: chatOnly,
		Doc: "Typed failure class of a failed call; absent on success"},
	{Key: "gen_ai.operation.name", Type: TypeString, Class: ClassEnum, Enum: []string{"chat"}, Spans: chatOnly,
		Doc: "Always chat"},
	{Key: "gen_ai.provider.name", Type: TypeString, Class: ClassEnum, Enum: ProviderNames, Spans: chatOnly,
		Doc: "Provider the call was sent to (semconv v1.40 name)"},
	{Key: "gen_ai.request.max_tokens", Type: TypeInt, Min: 1, Max: maxInt, Spans: chatOnly,
		Doc: "max_tokens sent, when the request set one"},
	{Key: "gen_ai.request.model", Type: TypeString, Class: ClassModel, Spans: chatOnly,
		Doc: "Model id the request was dispatched with"},
	{Key: "gen_ai.request.temperature", Type: TypeFloat, Min: 0, Max: 2, Spans: chatOnly,
		Doc: "Sampling temperature, when the request set one"},
	{Key: "gen_ai.response.finish_reasons", Type: TypeStringSlice, Class: ClassToken, Spans: chatOnly,
		Doc: "Provider finish reason(s), e.g. stop, end_turn, tool_use, length"},
	{Key: "gen_ai.response.id", Type: TypeString, Class: ClassID, Spans: chatOnly,
		Doc: "Provider generation id (OpenRouter gen-…, Anthropic msg_…, OpenAI chatcmpl-…)"},
	{Key: "gen_ai.response.model", Type: TypeString, Class: ClassModel, Spans: chatOnly,
		Doc: "Model the provider reports having served"},
	{Key: "gen_ai.system", Type: TypeString, Class: ClassEnum, Enum: ProviderNames, Spans: chatOnly,
		Doc: "Pre-v1.37 name of gen_ai.provider.name, same value (OpenLLMetry dashboards)"},
	{Key: "gen_ai.usage.cache_creation.input_tokens", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Prompt tokens written to the provider cache, when reported"},
	{Key: "gen_ai.usage.cache_read.input_tokens", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Prompt tokens served from the provider cache, when reported"},
	{Key: "gen_ai.usage.input_tokens", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Prompt tokens as the provider reports them"},
	{Key: "gen_ai.usage.output_tokens", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Completion tokens"},
	{Key: "kenaz.llm.attempt", Type: TypeInt, Min: 0, Max: 1000, Spans: chatOnly,
		Doc: "0-based index of this provider call within the model request (retries and fallback hops count up)"},
	{Key: "kenaz.llm.cost_usd", Type: TypeFloat, Min: 0, Max: 1e6, Spans: chatOnly,
		Doc: "Call cost in USD from the cost readout (provider-reported or price-table derived); absent when indeterminate"},
	{Key: "kenaz.llm.fallback", Type: TypeBool, Spans: chatOnly,
		Doc: "True when the call is a model-fallback hop"},
	{Key: "kenaz.llm.reasoning_tokens", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Reasoning/thinking tokens, when reported"},
	{Key: "kenaz.llm.stream", Type: TypeBool, Spans: chatOnly,
		Doc: "True for a streaming call (every harness call today)"},
	{Key: "kenaz.llm.tool_count", Type: TypeInt, Min: 0, Max: 100000, Spans: chatOnly,
		Doc: "Number of tool schemas sent"},
	{Key: "kenaz.llm.tool_schema_tokens_est", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Estimated tokens of the tool schemas sent"},
	{Key: "kenaz.llm.ttft_ms", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Milliseconds from call start to the first streamed text/tool/reasoning chunk"},
	{Key: "kenaz.provider.request_id", Type: TypeString, Class: ClassID, Spans: chatOnly,
		Doc: "Provider request-id response header (Anthropic request-id, OpenAI x-request-id, Azure apim-request-id)"},
	{Key: "kenaz.provider.system_fingerprint", Type: TypeString, Class: ClassID, Spans: chatOnly,
		Doc: "OpenAI-family system_fingerprint"},
	{Key: "kenaz.provider.tool_use_prompt_tokens", Type: TypeInt, Min: 0, Max: maxInt, Spans: chatOnly,
		Doc: "Gemini usageMetadata.toolUsePromptTokenCount"},
	{Key: "kenaz.provider.upstream", Type: TypeString, Class: ClassToken, Spans: chatOnly,
		Doc: "Upstream provider a router (OpenRouter) dispatched to, normalised"},
	{Key: "kenaz.session.hash", Type: TypeString, Class: ClassHash, Spans: bothSpans,
		Doc: "Keyed HMAC-SHA256 of the session id (per-process key), never the raw id"},
}

// specByKey indexes SpanAttributes.
var specByKey = func() map[string]AttrSpec {
	m := make(map[string]AttrSpec, len(SpanAttributes))
	for _, s := range SpanAttributes {
		if _, dup := m[s.Key]; dup {
			panic("genaitrace: duplicate attribute spec " + s.Key)
		}
		m[s.Key] = s
	}
	return m
}()

var (
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9._:/@+=-]{1,128}$`)
	tokenPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
	hashPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// normaliseToken lower-cases a provider enum token and turns spaces into
// '-', so "Google AI Studio" becomes "google-ai-studio".
func normaliseToken(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
}

// boundString returns the value to emit for a string of class c, and
// false when it falls outside the bound (the attribute is then dropped).
func boundString(c ValueClass, enum []string, v string) (string, bool) {
	switch c {
	case ClassID, ClassModel:
		return v, idPattern.MatchString(v)
	case ClassToken:
		v = normaliseToken(v)
		return v, tokenPattern.MatchString(v)
	case ClassHash:
		return v, hashPattern.MatchString(v)
	case ClassEnum:
		for _, e := range enum {
			if v == e {
				return v, true
			}
		}
		return v, false
	}
	return v, false
}

// boundText renders a spec's bound for the generated table.
func (s AttrSpec) boundText() string {
	switch s.Type {
	case TypeBool:
		return "true / false"
	case TypeInt:
		return fmt.Sprintf("integer %s..%s", fmtNum(s.Min), fmtNum(s.Max))
	case TypeFloat:
		return fmt.Sprintf("number %s..%s", fmtNum(s.Min), fmtNum(s.Max))
	}
	prefix := ""
	if s.Type == TypeStringSlice {
		prefix = "each element: "
	}
	switch s.Class {
	case ClassID:
		return prefix + "provider id, ≤128 chars, `[A-Za-z0-9._:/@+=-]`"
	case ClassModel:
		return prefix + "model/provider id, ≤128 chars, `[A-Za-z0-9._:/@+=-]`"
	case ClassToken:
		return prefix + "provider enum token, lower-cased, ≤64 chars, `[a-z0-9._-]`"
	case ClassHash:
		return prefix + "16 lowercase hex chars"
	case ClassEnum:
		vals := append([]string(nil), s.Enum...)
		sort.Strings(vals)
		return prefix + "one of: " + "`" + strings.Join(vals, "`, `") + "`"
	}
	return "UNBOUNDED"
}

func fmtNum(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// RenderAttributeTable renders SpanAttributes as the Markdown document
// Fleet copies (docs/telemetry/gen-ai-span-attributes.md).
func RenderAttributeTable() string {
	var b strings.Builder
	b.WriteString("# gen_ai span attributes (Fleet-declarable)\n\n")
	b.WriteString("<!-- GENERATED by core/llm/genaitrace (RenderAttributeTable) from SpanAttributes.\n")
	b.WriteString("     Do not edit by hand: run\n")
	b.WriteString("     GENAI_ATTR_DOC_UPDATE=1 go test ./core/llm/genaitrace -run TestSpanAttributeDocInSync\n")
	b.WriteString("     TestSpanAttributeDocInSync fails when this file and the code drift. -->\n\n")
	b.WriteString("Semantic conventions: `" + SemconvSchemaURL + "`.\n\n")
	b.WriteString("## Span names\n\n")
	b.WriteString("| Span name | Kind | Parent | What it is |\n|---|---|---|---|\n")
	b.WriteString("| `" + SpanNameTurn + "` | internal | none | One chat turn (the run of the agent loop for one user message) |\n")
	b.WriteString("| `" + SpanNameChat + "` | client | `" + SpanNameTurn + "` when the call runs inside a turn | One provider call, including every retry and fallback attempt |\n\n")
	b.WriteString("`" + SpanNameChat + "` is a fixed name on purpose: the semconv recommendation (`chat {model}`) is an unbounded name set an allowlist cannot declare. The model is in `gen_ai.request.model`.\n\n")
	b.WriteString("No data travels in span events, links, tracestate or Status.Message: the spans set none of them (status is code-only).\n\n")
	b.WriteString("## Attributes\n\n")
	b.WriteString("Any key not listed here is never set. Content (prompts, completions, tool arguments, tool results, system prompt text) is never set, under any key.\n\n")
	b.WriteString("| Key | Type | Bound | Span(s) | Meaning |\n|---|---|---|---|---|\n")
	for _, s := range SpanAttributes {
		spans := "`" + strings.Join(s.Spans, "`, `") + "`"
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", s.Key, s.Type, s.boundText(), spans, s.Doc)
	}
	return b.String()
}
