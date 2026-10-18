package genaitrace

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	semconv136 "go.opentelemetry.io/otel/semconv/v1.36.0"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

const docRel = "docs/telemetry/gen-ai-span-attributes.md"

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// TestSpanAttributeDocInSync: the Fleet-declarable table in
// docs/telemetry/gen-ai-span-attributes.md is generated from
// SpanAttributes and must not drift from it. Regenerate with
// GENAI_ATTR_DOC_UPDATE=1.
func TestSpanAttributeDocInSync(t *testing.T) {
	path := filepath.Join(repoRoot(t), docRel)
	want := RenderAttributeTable()
	if os.Getenv("GENAI_ATTR_DOC_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with GENAI_ATTR_DOC_UPDATE=1)", docRel, err)
	}
	if string(got) != want {
		t.Fatalf("%s drifted from SpanAttributes — regenerate with "+
			"GENAI_ATTR_DOC_UPDATE=1 go test ./core/llm/genaitrace -run TestSpanAttributeDocInSync", docRel)
	}
	for _, s := range SpanAttributes {
		if !strings.Contains(string(got), "| `"+s.Key+"` |") {
			t.Errorf("doc does not list %s", s.Key)
		}
	}
}

// TestSpanAttributes_WellFormed: sorted, unique, every string attribute
// bounded, every number ranged, every enum non-empty.
func TestSpanAttributes_WellFormed(t *testing.T) {
	keys := make([]string, 0, len(SpanAttributes))
	for _, s := range SpanAttributes {
		keys = append(keys, s.Key)
		if !strings.HasPrefix(s.Key, "gen_ai.") && !strings.HasPrefix(s.Key, "kenaz.") && s.Key != "error.type" {
			t.Errorf("%s: outside the gen_ai.* / kenaz.* / error.type namespaces", s.Key)
		}
		if len(s.Spans) == 0 || s.Doc == "" {
			t.Errorf("%s: missing Spans or Doc", s.Key)
		}
		switch s.Type {
		case TypeString, TypeStringSlice:
			switch s.Class {
			case ClassID, ClassModel, ClassToken, ClassHash:
			case ClassEnum:
				if len(s.Enum) == 0 {
					t.Errorf("%s: enum with no values", s.Key)
				}
			default:
				t.Errorf("%s: string attribute without a bounded class", s.Key)
			}
		case TypeInt, TypeFloat:
			if s.Max <= s.Min {
				t.Errorf("%s: empty numeric range", s.Key)
			}
		case TypeBool:
		default:
			t.Errorf("%s: unknown type %q", s.Key, s.Type)
		}
		if strings.Contains(s.boundText(), "UNBOUNDED") {
			t.Errorf("%s: unbounded", s.Key)
		}
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("SpanAttributes is not sorted by key: %v", keys)
	}
}

// TestSpanAttributes_MatchSemconv pins every gen_ai.* key (and error.type)
// to the vendored semconv constant, so a typo or a rename cannot ship.
func TestSpanAttributes_MatchSemconv(t *testing.T) {
	known := map[string]attribute.Key{}
	for _, k := range []attribute.Key{
		semconv.ErrorTypeKey,
		semconv.GenAIOperationNameKey,
		semconv.GenAIProviderNameKey,
		semconv.GenAIRequestMaxTokensKey,
		semconv.GenAIRequestModelKey,
		semconv.GenAIRequestTemperatureKey,
		semconv.GenAIResponseFinishReasonsKey,
		semconv.GenAIResponseIDKey,
		semconv.GenAIResponseModelKey,
		semconv.GenAIUsageCacheCreationInputTokensKey,
		semconv.GenAIUsageCacheReadInputTokensKey,
		semconv.GenAIUsageInputTokensKey,
		semconv.GenAIUsageOutputTokensKey,
		semconv136.GenAISystemKey, // pre-1.37 name, emitted for compatibility
	} {
		known[string(k)] = k
	}
	for _, s := range SpanAttributes {
		if strings.HasPrefix(s.Key, "kenaz.") {
			continue
		}
		if _, ok := known[s.Key]; !ok {
			t.Errorf("%s: not a semconv key this test pins", s.Key)
		}
	}
	if semconv.GenAIOperationNameChat.Value.AsString() != "chat" {
		t.Errorf("semconv chat operation drifted")
	}
	for _, v := range []attribute.KeyValue{
		semconv.GenAIProviderNameAnthropic, semconv.GenAIProviderNameOpenAI,
		semconv.GenAIProviderNameAzureAIOpenAI, semconv.GenAIProviderNameGCPGemini,
		semconv.GenAIProviderNameGCPVertexAI, semconv.GenAIProviderNameAWSBedrock,
	} {
		if !contains(ProviderNames, v.Value.AsString()) {
			t.Errorf("ProviderNames lacks semconv well-known value %q", v.Value.AsString())
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestAttrSet_RefusesUndeclaredAndUnbounded: the only attribute writer
// drops an undeclared key, a type mismatch, free text in a bounded class,
// an unknown enum value and an out-of-range number.
func TestAttrSet_RefusesUndeclaredAndUnbounded(t *testing.T) {
	before := DroppedAttributes()
	var a attrSet
	a.str("gen_ai.prompt", "hello")                                     // undeclared
	a.int("gen_ai.request.model", 3)                                    // wrong type
	a.str("gen_ai.response.id", "two words")                            // free text in an id
	a.str("gen_ai.response.model", strings.Repeat("m", 129))            // too long
	a.str("error.type", "connection reset by peer")                     // not in the enum
	a.str("kenaz.session.hash", "not-a-hash")                           // not a hash
	a.strs("gen_ai.response.finish_reasons", []string{"stop!! please"}) // not a token
	a.float("gen_ai.request.temperature", 9)                            // out of range
	a.int("kenaz.llm.attempt", -1)                                      // out of range
	if len(a.kv) != 0 {
		t.Fatalf("attrSet kept %v", a.kv)
	}
	if got := DroppedAttributes() - before; got != 9 {
		t.Fatalf("dropped %d, want 9", got)
	}

	a.str("kenaz.provider.upstream", "Google AI Studio")
	if len(a.kv) != 1 || a.kv[0].Value.AsString() != "google-ai-studio" {
		t.Fatalf("token normalisation: %v", a.kv)
	}
}

func TestProviderName_ClosedSet(t *testing.T) {
	for kind, want := range map[string]string{
		"anthropic": "anthropic", "openai": "openai", "openrouter": "openrouter",
		"azure-openai": "azure.ai.openai", "gemini": "gcp.gemini", "bedrock": "aws.bedrock",
		"ollama": "ollama", "custom-openai": "openai_compatible", "something-new": "other",
	} {
		if got := ProviderName(kind, ""); got != want {
			t.Errorf("ProviderName(%q) = %q, want %q", kind, got, want)
		}
		if !contains(ProviderNames, want) {
			t.Errorf("%q not in ProviderNames", want)
		}
	}
	if got := ProviderName("gemini", "vertex"); got != "gcp.vertex_ai" {
		t.Errorf("vertex = %q", got)
	}
}

func TestSessionHash_KeyedAndBounded(t *testing.T) {
	h := SessionHash("sess-1")
	if !hashPattern.MatchString(h) || h == SessionHash("sess-2") || h != SessionHash("sess-1") {
		t.Fatalf("SessionHash: %q", h)
	}
	if strings.Contains(h, "sess") || SessionHash("") != "" {
		t.Fatalf("SessionHash leaks or hashes empty: %q", h)
	}
}
