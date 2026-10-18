package rpc

// branches_wiring_test.go — model-settings-reach-the-model-01PMZ101
// UNIT-10 / WP17, AC-015.
//
// Closing-sweep finding AN-07: knownModelProviders was hardcoded to
// []string{"anthropic", "openai"}, so a gemini / azure-openai /
// openrouter parent session could only ever be answered with an
// anthropic or openai model plus a cross-provider warning. Widened per
// this WP; see knownModelProviders' own doc comment for why
// gemini/openrouter needed real capabilities catalog data (exact
// tiers: rows) in addition to the widened list, and for why bedrock /
// custom-openai / ollama are deliberately NOT in the widened list
// (bedrock: no exact model id this WP could verify without guessing a
// version-dated string; custom-openai/ollama: no fixed catalog exists
// for an arbitrary user endpoint).
//
// IMPORTANT SCOPE NOTE this test does NOT paper over: it drives
// agentgraph.BranchRecommender.Recommend DIRECTLY with an explicit
// parent (providerID, modelID) pair — it does not go through
// branchesview.API.RecommendModel. That RPC's own parentModel helper
// (core/rpc/views/branches/impl.go) is a hardcoded `return "", ""` stub
// that ignores its sessionID argument and the session.Manager it has
// access to (no session.Record field carries an "active provider/model"
// at all — that selection lives only in the frontend's per-session
// localStorage, per SessionsView.vue's readSessionConfig). Every
// RecommendModel call today resolves its cross-provider-warning check
// against an always-empty parentProvider, which can never be non-empty,
// so the warning can never fire for ANY parent — not because same-
// provider detection works, but because there is no parent to compare
// against. That is a deeper, separate defect this WP's own spec text
// never named (it describes catalog hydration only); fixing it needs
// either a session-level "active model" persistence layer or a
// RecommendModel signature change threading the frontend's already-known
// provider/model through — both real product decisions, not something
// to invent here. Recorded in docs/unwired-ledger.md as an escalation.
// AC-015 as literally written ("with bedrock, gemini, azure-openai,
// custom-openai and openrouter profiles configured, assert the
// recommender returns a candidate of the parent's own provider") is
// therefore verified here at the RECOMMENDER level, which is the part
// this WP's spec actually describes fixing; it is NOT verified through
// the live RPC end to end, because the RPC's own parentModel bug makes
// that impossible to observe today regardless of the recommender's
// correctness.

import (
	"testing"

	llmcap "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

func mustLoadCapabilitiesCatalog(t *testing.T) *llmcap.Catalog {
	t.Helper()
	cat, err := llmcap.LoadDefault()
	if err != nil {
		t.Fatalf("llmcap.LoadDefault: %v", err)
	}
	return cat
}

// TestNewBranchRecommender_RecommendsWithinParentProvider is AC-015's
// core assertion: for every WIDENED provider (excluding anthropic/openai,
// which already worked before this WP — the regression case, not the
// fix), a fork from a parent on that provider gets a same-provider
// candidate with no fallback to the parent's exact pair (a fallback
// would mean pickAtTier found NO candidate at that provider at all —
// the exact bug AN-07 named).
//
// Fails if: only anthropic and openai are configured — the pre-WP17
// literal satisfies that and proves nothing (this test's whole point is
// the other three kinds this WP actually added real catalog data for).
func TestNewBranchRecommender_RecommendsWithinParentProvider(t *testing.T) {
	cat := mustLoadCapabilitiesCatalog(t)
	rec := newBranchRecommender(cat)

	cases := []struct {
		providerID    string
		parentModelID string
	}{
		{"gemini", "gemini-2.5-flash"},
		{"azure-openai", "gpt-4o"},
		{"openrouter", "anthropic/claude-sonnet-4.5"},
	}

	for _, tc := range cases {
		t.Run(tc.providerID, func(t *testing.T) {
			got := rec.Recommend(tc.providerID, tc.parentModelID, "", "")
			if got.ProviderID != tc.providerID {
				t.Errorf("Recommend(%q, ...).ProviderID = %q, want %q — AN-07's exact defect: "+
					"a %s parent falling back to a different provider's model",
					tc.providerID, got.ProviderID, tc.providerID, tc.providerID)
			}
		})
	}
}

// TestNewBranchRecommender_ExcludedProvidersDegradeToParentPair pins the
// deliberate exclusions documented on knownModelProviders: custom-openai
// and ollama name an arbitrary user endpoint with no fixed model
// catalog; bedrock has real models but no id this WP could verify
// without guessing a version-dated string. All three should get the
// existing no-candidate-found fallback (the parent's own exact pair) —
// not an invented placeholder model id that would fail at the provider
// boundary the way WP18's bundled-profile finding did.
func TestNewBranchRecommender_ExcludedProvidersDegradeToParentPair(t *testing.T) {
	cat := mustLoadCapabilitiesCatalog(t)
	rec := newBranchRecommender(cat)

	for _, providerID := range []string{"custom-openai", "ollama", "bedrock"} {
		t.Run(providerID, func(t *testing.T) {
			const parentModel = "whatever-the-user-configured"
			got := rec.Recommend(providerID, parentModel, "", "")
			if got.ProviderID != providerID || got.ModelID != parentModel {
				t.Errorf("Recommend(%q, %q, ...) = %+v, want the parent's exact pair back "+
					"(no fixed catalog exists for this kind)", providerID, parentModel, got)
			}
		})
	}
}

// TestKnownModelProviders_EveryEntryYieldsAtLeastOneCandidate is the
// regression guard for the glob-only-tiers class of gap this WP found:
// a provider CAN be listed in knownModelProviders and STILL contribute
// zero KnownModels if its capabilities YAML has no exact (non-glob)
// tiers: row (bedrock's and openrouter's tables were exactly this shape
// before this WP added real rows). Every entry here must resolve to at
// least one concrete candidate, or its presence in the list is a lie.
func TestKnownModelProviders_EveryEntryYieldsAtLeastOneCandidate(t *testing.T) {
	cat := mustLoadCapabilitiesCatalog(t)
	for _, providerID := range knownModelProviders {
		known := cat.KnownModels(providerID)
		if len(known) == 0 {
			t.Errorf("KnownModels(%q) = empty; knownModelProviders lists it but the capabilities "+
				"catalog has no exact tiers: row for it — every glob-only tiers: table produces this",
				providerID)
		}
	}
}

// TestCatalog_AzureOpenAIResolvesViaAlias pins the Tier/KnownModels
// alias fix (core/llm/capabilities/loader.go): azure-openai has no
// dedicated YAML file (WP02's alias table points it at "openai" for
// Describe/DescribeRich/AttachmentLimits) — Tier and KnownModels are a
// fourth and fifth lookup entry point the alias table's own doc comment
// ("applied in all three lookup entry points") did not cover before
// this WP.
func TestCatalog_AzureOpenAIResolvesViaAlias(t *testing.T) {
	cat := mustLoadCapabilitiesCatalog(t)

	tier, ok := cat.Tier("azure-openai", "gpt-4o")
	if !ok || tier != "medium" {
		t.Errorf(`Tier("azure-openai", "gpt-4o") = (%q, %v), want ("medium", true)`, tier, ok)
	}

	known := cat.KnownModels("azure-openai")
	if len(known) == 0 {
		t.Fatal(`KnownModels("azure-openai") = empty, want openai's known models via the alias`)
	}
	var sawGPT4o bool
	for _, m := range known {
		if m.ModelID == "gpt-4o" {
			sawGPT4o = true
		}
	}
	if !sawGPT4o {
		t.Errorf(`KnownModels("azure-openai") = %+v, want it to include "gpt-4o"`, known)
	}
}
