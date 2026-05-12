package narrative

// ChunkKind classifies a memory chunk in the narrative layer.
// The kind travels in Chunk.Kind (a string field) and gates retrieval
// weighting, compactor strategy selection, and prune-sweep skip rules.
type ChunkKind string

const (
	// ChunkKindRaw is the default for all chunks written before the
	// narrative layer (or when MemoryNarrativeEnabled=false).
	ChunkKindRaw ChunkKind = "raw"

	// ChunkKindNarrativeExtractive is a per-tool chunk produced
	// deterministically inside HookPostTool (microsecond cost, no LLM).
	// Schema: {tool, args_summary, result_summary, duration_ms, success}.
	ChunkKindNarrativeExtractive ChunkKind = "narrative_extractive"

	// ChunkKindNarrativeSynthesised is a per-turn chunk produced by the
	// Promoter worker pool using the configured summariser model. It
	// carries the {request, investigated, learned, outcome, next_steps}
	// schema and is the primary input for the compactor's narrative_first
	// strategy. When present, it replaces any extractive fallback for the
	// same (session_id, turn_id) key.
	ChunkKindNarrativeSynthesised ChunkKind = "narrative_synthesised"

	// ChunkKindNarrativeExtractiveFallback is written by BuildTurnFallback
	// when the LLM summariser fails (first failure). It uses the same
	// {request, investigated, learned, outcome, next_steps} schema, but
	// fields are populated mechanically from raw content instead of an LLM.
	// Replaced in-place when a retry succeeds.
	ChunkKindNarrativeExtractiveFallback ChunkKind = "narrative_extractive_fallback"
)

// IsNarrative reports whether k is any narrative kind (extractive, synthesised,
// or fallback). Raw chunks return false.
func IsNarrative(k ChunkKind) bool {
	switch k {
	case ChunkKindNarrativeExtractive,
		ChunkKindNarrativeSynthesised,
		ChunkKindNarrativeExtractiveFallback:
		return true
	default:
		return false
	}
}

// IsSynthesised reports whether k is the high-fidelity LLM-produced variant.
// Only ChunkKindNarrativeSynthesised returns true.
func IsSynthesised(k ChunkKind) bool {
	return k == ChunkKindNarrativeSynthesised
}

// DefaultRetrievalWeight is the retrieval-score multiplier applied to
// narrative chunks when Settings.NarrativeRetrievalWeight is zero.
const DefaultRetrievalWeight float32 = 1.5

// DefaultRawRetrievalWeight is the retrieval-score multiplier for raw
// chunks (no boost).
const DefaultRawRetrievalWeight float32 = 1.0
