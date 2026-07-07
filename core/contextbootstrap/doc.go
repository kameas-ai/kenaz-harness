// Package contextbootstrap implements the dynamic MCP-driven agentic
// context-bootstrap engine (mission context-bootstrap-01NCTX01).
//
// This package is the harness companion for WP07–WP10. It does NOT
// depend on the fleet-side recipe registry, run-status API, or
// context-graph storage — it communicates via clean seams defined below.
// Fleet integration (WP01–WP06, WP11) lives in the kenaz-fleet repo.
//
// Architecture overview:
//
//	Engine  —  the top-level service. Assembled via New(Config).
//	  │         Callable from harness-onboarding and re-run triggers.
//	  │
//	  ├── Interview  — ask what tools the user uses + who to trust
//	  │                (WP08). Seeded by a ToolChecklist (from fleet
//	  │                welcome or local default), refined agentically.
//	  │
//	  ├── Gating     — ensure each chosen MCP is installed + authorized
//	  │                before extraction begins (WP08). Blocks + guides
//	  │                the user when a connector is not live.
//	  │
//	  ├── Workflow   — assemble the bootstrap run from connected tools
//	  │                only; one ConnectorRun per connector (WP07).
//	  │                Budget/coverage control: stops cleanly + reports
//	  │                coverage, never silent truncation.
//	  │
//	  └── Extraction — call MCP read/search/list tools + run recipe
//	                   prompts per connector (WP09 / WP10). Treats ALL
//	                   source content as DATA, never as instructions.
//	                   Constrains to read-only tools; quarantines output
//	                   before writing context nodes.
//
// Fleet⇄harness contract (DEFERRED — fleet-side WP06 not yet merged):
//
//	The RecipeSource interface is the seam where fleet pushes bootstrap
//	recipes (per-connector extraction prompts, interview schema, taxonomy,
//	confidence rules). Until fleet WP06 lands, a LocalRecipeSource backed
//	by the bundled fixtures/outlook.json file satisfies the seam and proves
//	the loop end-to-end with the Outlook/email connector (WP10).
//
// Privacy invariants (enforced throughout):
//   - Source credentials NEVER leave the harness via this package.
//   - Source content is quarantined: stripped of secrets, never logged
//     via slog, never forwarded to fleet as raw bytes.
//   - Only extracted context nodes + connection status flow toward fleet.
//   - No write-capable MCP tools are called during extraction (WP09).
package contextbootstrap
