package migrations

// VersionBlock is an inclusive [Min, Max] integer range.
type VersionBlock struct {
	Min int
	Max int
}

// Contains reports whether v falls inside the block.
func (b VersionBlock) Contains(v int) bool {
	return v >= b.Min && v <= b.Max
}

// CanonicalBlocks is the version-block reservation table from plan §6.3.
// Each owning mission name maps to its inclusive integer range. The
// table is the single source of truth referenced by Registry.Register
// and by the README in kitty-specs/storage-foundations-01KQ1A3K/tasks/.
//
// To claim a new range, add an entry here and update the README.
var CanonicalBlocks = map[string]VersionBlock{
	"storage":          {Min: 1, Max: 99},
	"event-log":        {Min: 100, Max: 199},
	"secrets-keychain": {Min: 200, Max: 299},
	"sessions":         {Min: 300, Max: 399},
	"scheduler":        {Min: 400, Max: 499},
	"mcp":              {Min: 500, Max: 599},
	// a2a-signed-cards-trust-01KQ18P9 (2026-04-25) is a single mission
	// covering both the A2A protocol surface and the signed-cards trust
	// surface; blocks.go originally reserved 600-699 under BOTH names,
	// which produced no live collision (neither mission had shipped a
	// migration) but was a hazard by construction: Registry.Register
	// keys on Version globally, so any two migrations from these two
	// names landing on the same version number would fail at
	// storagesqlite.Open with ErrVersionCollision — an install that
	// will not start. Found 2026-08-20 (docs/unwired-ledger.md,
	// "Two pairs of missions share a migration block"); fixed here.
	// "a2a" keeps 600-699 since nothing has shipped in it yet and the
	// two names need distinct ranges regardless of which keeps the
	// original block.
	"a2a": {Min: 600, Max: 699},
	// signed-cards-trust-01KQ18P9: moved off the shared 600-699 block
	// (see the "a2a" comment above) to 1400-1499. Nothing has shipped
	// under this name yet, so renumbering is free — no ledger rows to
	// preserve.
	"signed-cards-trust": {Min: 1400, Max: 1499},
	// bundle-download-and-verify-01PMZ909 UNIT-3 already shipped
	// bundle/700-trust-anchors-init (see core/trust/migrations.go) —
	// this range MUST NOT be renumbered; doing so would either re-run
	// an already-applied migration or leave a stale ledger row on every
	// install that has it. shared-context-distribution moved off this
	// block instead (see below); the two were never a deliberate pair —
	// they are two unrelated missions (separate kitty-specs/_archive
	// entries: bundle-format-resolver-01KQ1A3J /
	// bundle-download-and-verify-01PMZ909 vs.
	// shared-context-distribution-01KQ18PA) that happened to claim the
	// same range, most likely a copy-paste at authoring time. Found
	// 2026-08-20 (docs/unwired-ledger.md); fixed here.
	"bundle": {Min: 700, Max: 799},
	// shared-context-distribution-01KQ18PA: moved off the shared
	// 700-799 block (see the "bundle" comment above) to 1500-1599.
	// Nothing has shipped under this name yet, so renumbering is free.
	"shared-context-distribution": {Min: 1500, Max: 1599},
	"memory-rag":                  {Min: 800, Max: 899},
	"app-layer":                   {Min: 900, Max: 999},
	// user-slash-commands-01KQ8TD9: 1000-1099.
	// model-invoked-skills-catalog-01KZNP3E may add a column-add migration
	// in this same block (version 1001+) to extend slash_commands_user.
	"user-slash-commands": {Min: 1000, Max: 1099},
	// unified-context-artifacts-01NCTXU01: 1100-1199.
	// Units + unit_versions + unit_edges tables for the unified
	// context+artifacts store (core/units package).
	"units": {Min: 1100, Max: 1199},
	// subagent-control-and-background-tasks-01PMZB11: 1200-1299.
	// The tasks table for core/tasks.Registry's persistence store.
	"tasks": {Min: 1200, Max: 1299},
	// finding-58-cedar-decision-persistence: 1300-1399. Reserved on
	// release/v0.78.1 (not yet on main as of this fix) — carried
	// forward here so this fix does not hand out an overlapping range.
	// The policy_decisions table backing core/policy/cedar.SQLDecisionStore.
	"cedar-policy": {Min: 1300, Max: 1399},
}

// LookupBlock returns the reserved block for the given owning-mission
// name. Reports false when no block is reserved.
func LookupBlock(owningMission string) (VersionBlock, bool) {
	b, ok := CanonicalBlocks[owningMission]
	return b, ok
}
