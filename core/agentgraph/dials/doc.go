// Package dials defines the typed dial knobs the kernel and frontend
// expose for per-run / per-session / per-project / global tuning
// (Bundle E WP17 of the agent-kernel-graph mission).
//
// Cascading layers (highest precedence first):
//
//	per-graph  ⟶  per-session  ⟶  per-project  ⟶  global
//
// Resolve() walks the layers and produces the effective DialConfig
// plus, for each set field, the layer that contributed it. The
// cascading attribution is what the inspector UI shows the user
// ("MaxTokensPerRun = 16000 (from project)") so a surprising value
// never goes unexplained (NFR-014).
//
// The package intentionally avoids importing core/agentgraph — the
// kernel imports dials, not the other way around. Sharing a Budget
// shape with core/agentgraph would create a cycle, so this package
// keeps its own DialConfig and a converter (`AsBudget`) lives in
// core/agentgraph.
package dials
