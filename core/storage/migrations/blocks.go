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
	"storage":                     {Min: 1, Max: 99},
	"event-log":                   {Min: 100, Max: 199},
	"secrets-keychain":            {Min: 200, Max: 299},
	"sessions":                    {Min: 300, Max: 399},
	"scheduler":                   {Min: 400, Max: 499},
	"mcp":                         {Min: 500, Max: 599},
	"a2a":                         {Min: 600, Max: 699},
	"signed-cards-trust":          {Min: 600, Max: 699},
	"bundle":                      {Min: 700, Max: 799},
	"shared-context-distribution": {Min: 700, Max: 799},
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
}

// LookupBlock returns the reserved block for the given owning-mission
// name. Reports false when no block is reserved.
func LookupBlock(owningMission string) (VersionBlock, bool) {
	b, ok := CanonicalBlocks[owningMission]
	return b, ok
}
