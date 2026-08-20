package agentgraph

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// EmbeddedCedar holds the four shipped Cedar policy snippets for the
// graph-authoring action family (mission model-authored-graphs-01PMGA01,
// UNIT-3):
//
//   - graph_author_forbid.cedar — forbid graph.author unless the FR-006
//     consent dial is on.
//   - graph_author_permit.cedar — permit graph.author when the dial is
//     on, paired with the forbid so the enabled case is attributable to
//     the install rather than to the engine's default posture.
//   - graph_author_no_write_file.cedar — forbid graph.author when the
//     drafted graph contains a write_file node (FR-008).
//   - graph_run_unreviewed_forbid.cedar — forbid graph.run on a graph
//     still marked model-authored and unreviewed (FR-007).
//
//go:embed cedar/*.cedar
var EmbeddedCedar embed.FS

// CedarSnippets reads EmbeddedCedar's cedar/*.cedar files into the
// map[string][]byte shape core/policy/cedar.Engine.LoadHarnessSnippets
// expects, keyed by bare filename. Mirrors
// core/mcp/builtin/harness.CedarSnippets exactly — this mission's
// policies install through the SAME engine-loading mechanism (spec §9 /
// tasks.md UNIT-3: "shares its install site with
// harness-self-attach-01PMHS01 UNIT-2"; that mission's UNIT-2 landed
// first and already generalised LoadHarnessSnippets beyond the
// harness-self namespace — core/fleet/cedar_apply.go's
// CedarBundleApplier is the other non-harness-self caller). Two install
// paths for shipped Cedar policy would be rival infrastructure.
//
// Read failures here can only mean the embedded FS itself is malformed
// (a build-time defect); callers must treat a non-nil error as loud,
// never as a reason to boot with the graph-authoring gate silently
// absent — a silent skip is indistinguishable from "installed and
// permissive" to every existing listing test (spec §0's "fail-open
// through NotApplicable" risk).
func CedarSnippets() (map[string][]byte, error) {
	entries, err := fs.ReadDir(EmbeddedCedar, "cedar")
	if err != nil {
		return nil, fmt.Errorf("agentgraph: read embedded cedar dir: %w", err)
	}
	out := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cedar") {
			continue
		}
		data, err := fs.ReadFile(EmbeddedCedar, "cedar/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("agentgraph: read embedded cedar/%s: %w", entry.Name(), err)
		}
		out[entry.Name()] = data
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("agentgraph: no embedded cedar snippets found in cedar/*.cedar")
	}
	return out, nil
}
