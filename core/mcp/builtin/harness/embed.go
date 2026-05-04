package harness

import "embed"

// EmbeddedStarters holds the curated starter prompts that ship with the
// harness binary. Mission harness-self-mcp-onboarding-01KQ8TDU WP09 (per
// spec FR-003 + Constraint C-004).
//
// Power users override the defaults by dropping their own *.md files in
// `<DataDir>/onboarding/prompts/`; the resolver merges the user dir over
// the embedded set (see starters.go::Load).
//
//go:embed onboarding/*.md
var EmbeddedStarters embed.FS

// EmbeddedCedar holds the three default Cedar policy snippets that ship
// with the harness-self MCP server (mission WP06):
//
//   - harness_read_default.cedar       — permit harness_read_* in any session.
//   - harness_write_onboarding.cedar   — permit harness_write_* when
//                                        context.session_kind == "onboarding".
//   - harness_write_forbid.cedar       — explicit forbid for
//                                        harness_write_* outside onboarding.
//
//go:embed cedar/*.cedar
var EmbeddedCedar embed.FS
