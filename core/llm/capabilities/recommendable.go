package capabilities

// RecommendableProviderIDs returns the provider IDs the branch recommender
// enumerates concrete model candidates for.
//
// THIS LIST LIVES HERE, NOT IN core/rpc, BECAUSE OF check-no-model-family-
// literals.sh. Model-family names are core/llm's to know: core code outside
// core/llm asks a question ("which providers can you recommend for?")
// rather than restating the answer. The list previously sat in
// core/rpc/branches_wiring.go as a literal and turned that gate red the
// first time CI saw it (2026-09-12) — moving it is the fix the gate is
// asking for, not an allowlist entry.
//
// providerID and providerKind coincide for every kind listed here; a real
// multi-connection setup would key providerID off the user's configured
// connection name instead.
//
// WIDENED (model-settings-reach-the-model-01PMZ101 UNIT-10 / WP17, closing
// finding AN-07) from the original two-entry literal ("anthropic",
// "openai"): a gemini/azure-openai/openrouter parent could only ever be
// answered with an anthropic or openai model plus a cross-provider
// warning. Every kind here now has a real Catalog entry with at least one
// EXACT (non-glob) tiers: row — KnownModels skips glob rows by design
// ("classify a model rather than name one"), so openrouter's pre-existing
// tiers: table contributed ZERO candidates despite having data (an
// independent gap from the hardcoded-list one WP17 set out to fix), and
// gemini had no tiers: table at all before WP17.
//
// bedrock, custom-openai and ollama are deliberately EXCLUDED, for two
// different reasons:
//
//   - custom-openai / ollama name an arbitrary user-configured endpoint (a
//     self-hosted OpenAI-compatible server, a local Ollama install) with no
//     fixed catalog of real model ids to recommend — there is no "the
//     model" to enumerate. A parent on either kind falls back to the
//     recommender's own no-candidate-found path (the parent's exact pair),
//     the correct degrade.
//   - bedrock's tiers: table (data/bedrock.yaml) is ALSO glob-only today,
//     the same class of gap WP17 fixed for gemini/openrouter — but AWS
//     Bedrock's real model ids carry a dated version suffix
//     ("anthropic.claude-3-5-sonnet-20241022-v2:0") that changes as AWS
//     ships new snapshots, and WP17 could not verify a current one against
//     a live source the way the openrouter ids were verified against
//     openrouter.ai. Recommending a stale or wrong id is the WP18 "default
//     sentinel" class of defect (a string that claims to be a model but is
//     not one, discovered only at the provider boundary) — shipping a guess
//     would be worse than leaving bedrock out. A known, narrower follow-up,
//     not silently dropped.
//
// Returns a fresh slice per call so a caller cannot mutate the canonical
// order or contents.
func RecommendableProviderIDs() []string {
	return []string{"anthropic", "openai", "gemini", "azure-openai", "openrouter"}
}
