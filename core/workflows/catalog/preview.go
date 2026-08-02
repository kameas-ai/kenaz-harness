package catalog

import (
	"fmt"

	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// cedarGrantForKind maps step kinds to the Cedar action grant names that
// the workflow will need.
var cedarGrantForKind = map[corewf.StepKind]string{
	corewf.StepKindWebFetch:    "network",
	corewf.StepKindWebScrape:   "network",
	corewf.StepKindShell:       "shell",
	corewf.StepKindHTTPRequest: "network",
}

// ProjectEntry extracts Entry metadata from a parsed Workflow without a
// registry lookup (no credential checking — see concrete.go for the
// version that checks the recipe registry).
//
// It is the stateless projection the test suite calls directly.
func ProjectEntry(w corewf.Workflow) Entry {
	seen := make(map[string]bool)
	var grants []string
	for _, st := range w.Steps {
		if g, ok := cedarGrantForKind[st.Kind]; ok && !seen[g] {
			seen[g] = true
			grants = append(grants, g)
		}
	}

	estimated := estimateCostUSD(w)

	version := fmt.Sprintf("v%d", w.Version)

	return Entry{
		ID:                  w.ID,
		Name:                w.Name,
		Description:         w.Description,
		Source:              "builtin",
		Version:             version,
		RequiresCedarGrants: grants,
		EstimatedCostUSD:    estimated,
		InstallStatus:       "not_installed",
	}
}

// avgPromptTokens / avgCompletionTokens are rough estimates used when a
// step has no explicit token budget. Based on typical agentic-loop step
// sizes (WP03 spec).
const (
	avgPromptTokens     = 2000
	avgCompletionTokens = 500
)

// modelPricing maps known model IDs (or prefixes) to (input, output)
// pricing in USD per million tokens. Values match Anthropic public pricing
// as of 2025-05 and are intentionally conservative.
//
// This is a cost-estimation table, not a tier-classification lookup —
// out of versioned-model-profile-01PMDL04 WP04's scope (which closed the
// tierFromModelID frozen-core violation in core/agentgraph). Each row is
// individually marked model-lit-allow so scripts/ci/check-no-model-
// family-literals.sh's lint passes; a future pass could migrate this
// onto core/llm/capabilities data the same way WP04 did for tiers.
var modelPricing = []struct {
	prefix  string
	inPerM  float64
	outPerM float64
}{
	{"claude-3-7-sonnet", 3.00, 15.00}, // model-lit-allow: cost-estimation table, not classification
	{"claude-3-5-sonnet", 3.00, 15.00}, // model-lit-allow: cost-estimation table, not classification
	{"claude-3-5-haiku", 0.80, 4.00},   // model-lit-allow: cost-estimation table, not classification
	{"claude-3-haiku", 0.25, 1.25},     // model-lit-allow: cost-estimation table, not classification
	{"claude-3-sonnet", 3.00, 15.00},   // model-lit-allow: cost-estimation table, not classification
	{"claude-3-opus", 15.00, 75.00},    // model-lit-allow: cost-estimation table, not classification
	{"claude-sonnet-4", 3.00, 15.00},   // model-lit-allow: cost-estimation table, not classification
	// default: treat unknown models like claude-3-sonnet pricing.
}

// estimateCostUSD returns a rough per-run cost in USD by summing the
// projected token cost across every model_turn step.
func estimateCostUSD(w corewf.Workflow) float64 {
	var total float64
	for _, st := range w.Steps {
		if st.Kind != corewf.StepKindModelTurn {
			continue
		}
		in, out := lookupPricing(st.Model)
		promptTok := avgPromptTokens
		completionTok := avgCompletionTokens
		if len(st.UserPrompt) > 0 {
			// Rough: ~4 chars per token.
			charTok := len(st.UserPrompt) / 4
			if charTok > promptTok {
				promptTok = charTok
			}
		}
		total += float64(promptTok)/1e6*in + float64(completionTok)/1e6*out
	}
	return total
}

// lookupPricing returns the (in, out) USD/M-token rates for modelID.
func lookupPricing(modelID string) (float64, float64) {
	for _, p := range modelPricing {
		if len(modelID) >= len(p.prefix) && modelID[:len(p.prefix)] == p.prefix {
			return p.inPerM, p.outPerM
		}
	}
	// Default: claude-3-sonnet tier.
	return 3.00, 15.00
}
