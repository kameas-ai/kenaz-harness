package harness

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

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

// CedarSnippets reads EmbeddedCedar's cedar/*.cedar files into the
// map[string][]byte shape core/policy/cedar.Engine.LoadHarnessSnippets
// expects, keyed by bare filename (no "cedar/" prefix — matching the
// keys core/policy/cedar's own WP06 test fixture uses).
//
// Mission harness-self-attach-01PMHS01 UNIT-2: before this function
// EmbeddedCedar had zero non-test callers (LoadHarnessSnippets existed
// and was unit-tested at the engine level, but nothing at boot ever read
// these three files into the live engine) — every ActionUseTool
// evaluation for a harness-self tool was NotApplicable, which the
// engine's default-allow posture maps to Allow. Read failures here can
// only mean the embedded FS itself is malformed (a build-time defect,
// not a runtime one); callers must treat a non-nil error as loud, never
// as a reason to boot with the write-tool forbid floor silently absent.
func CedarSnippets() (map[string][]byte, error) {
	entries, err := fs.ReadDir(EmbeddedCedar, "cedar")
	if err != nil {
		return nil, fmt.Errorf("harness: read embedded cedar dir: %w", err)
	}
	out := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cedar") {
			continue
		}
		data, err := fs.ReadFile(EmbeddedCedar, "cedar/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("harness: read embedded cedar/%s: %w", entry.Name(), err)
		}
		out[entry.Name()] = data
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("harness: no embedded cedar snippets found in cedar/*.cedar")
	}
	return out, nil
}
