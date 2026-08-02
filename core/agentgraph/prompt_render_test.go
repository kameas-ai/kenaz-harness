package agentgraph

import (
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakePromptTemplateSource is a table-driven PromptTemplateSource fake,
// mirroring fakeTierSource's shape.
type fakePromptTemplateSource struct {
	table map[string]*corellm.PromptTemplateRef
}

func (f *fakePromptTemplateSource) PromptTemplate(providerKind, modelID string) *corellm.PromptTemplateRef {
	if f.table == nil {
		return nil
	}
	return f.table[providerKind+"/"+modelID]
}

// TestResolvePromptTemplate_NilWithoutData pins the no-behaviour-change
// guarantee: every path that lacks template data must resolve to nil,
// which selects prompts.Compose's byte-identical default renderer. A
// regression here would silently reshape every existing graph's system
// prompt.
func TestResolvePromptTemplate_NilWithoutData(t *testing.T) {
	withSource := &Env{PromptTemplates: &fakePromptTemplateSource{table: map[string]*corellm.PromptTemplateRef{}}}

	for _, tc := range []struct {
		name          string
		env           *Env
		provider, mdl string
	}{
		{"nil env", nil, "anthropic", "claude-x"},
		{"nil PromptTemplates", &Env{}, "anthropic", "claude-x"},
		{"empty model, no ActiveModelSource", withSource, "anthropic", ""},
		{"\"default\" sentinel, no ActiveModelSource", withSource, "anthropic", "default"},
		{"source has no opinion", withSource, "anthropic", "unknown-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvePromptTemplate(tc.env, tc.provider, tc.mdl); got != nil {
				t.Fatalf("resolvePromptTemplate = %#v, want nil (pre-WP01 behaviour)", got)
			}
		})
	}
}

func TestResolvePromptTemplate_UsesExplicitNodeModel(t *testing.T) {
	want := &corellm.PromptTemplateRef{Format: "xml"}
	env := &Env{PromptTemplates: &fakePromptTemplateSource{table: map[string]*corellm.PromptTemplateRef{
		"anthropic/big-model": want,
	}}}
	if got := resolvePromptTemplate(env, "anthropic", "big-model"); got != want {
		t.Fatalf("resolvePromptTemplate = %#v, want %#v", got, want)
	}
}

// TestResolvePromptTemplate_FallsBackToActiveModelSource covers the case
// shipped graphs actually hit: they hardcode model "default", so the
// template must come from whatever model the provider will really
// dispatch to.
func TestResolvePromptTemplate_FallsBackToActiveModelSource(t *testing.T) {
	want := &corellm.PromptTemplateRef{Format: "xml"}
	env := &Env{
		PromptTemplates: &fakePromptTemplateSource{table: map[string]*corellm.PromptTemplateRef{
			"openai/small-model": want,
		}},
		LLM: &fakeActiveModelProvider{kind: "openai", model: "small-model"},
	}
	for _, nodeModel := range []string{"", "default"} {
		if got := resolvePromptTemplate(env, "", nodeModel); got != want {
			t.Fatalf("resolvePromptTemplate(model=%q) = %#v, want %#v", nodeModel, got, want)
		}
	}
}

// TestResolvePromptTemplate_ExplicitModelBeatsActiveModelSource pins that
// a node author's explicit model attr is authoritative — the run's
// default model must not override it.
func TestResolvePromptTemplate_ExplicitModelBeatsActiveModelSource(t *testing.T) {
	authored := &corellm.PromptTemplateRef{Format: "xml"}
	ambient := &corellm.PromptTemplateRef{Format: "markdown"}
	env := &Env{
		PromptTemplates: &fakePromptTemplateSource{table: map[string]*corellm.PromptTemplateRef{
			"anthropic/authored": authored,
			"openai/ambient":     ambient,
		}},
		LLM: &fakeActiveModelProvider{kind: "openai", model: "ambient"},
	}
	if got := resolvePromptTemplate(env, "anthropic", "authored"); got != authored {
		t.Fatalf("resolvePromptTemplate = %#v, want %#v (explicit node model must win)", got, authored)
	}
}
