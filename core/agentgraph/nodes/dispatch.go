package nodes

import (
	"fmt"
	"sort"
	"strings"
)

// This file implements the `dispatch:` discriminator's structural
// checks (wiring-integrity-01PMAG04 WP01, spec §3.1 / FR-002).
//
// Deliberately NOT wired into parseManifest: the loader is also used by
// hand-crafted test fixtures (resolver_test.go, loader_test.go, ...)
// that exercise inheritance/merge mechanics unrelated to dispatch and
// have no reason to carry a dispatch: declaration. ValidateDispatch is
// instead an explicit, opt-in pass callers run over a resolved Catalog
// — the shipped bundled catalog is the one that must pass it (see
// dispatch_test.go), and scripts/ci/check-node-dispatch.sh runs the
// complementary Go-registry cross-check from the parent agentgraph
// package (which cannot import this package the other way — see the
// package doc's DIRECTIVE_001 note).

// DispatchIssue describes one manifest whose dispatch: declaration is
// missing or malformed.
type DispatchIssue struct {
	ID     string
	Reason string
}

func (i DispatchIssue) String() string { return fmt.Sprintf("%s: %s", i.ID, i.Reason) }

// ValidateDispatch checks every callable (non-archetype) manifest in
// the catalog declares a well-formed dispatch: value:
//   - Dispatch must be DispatchGraph or DispatchBuiltinTool.
//   - DispatchBuiltinTool requires a non-empty ToolName.
//   - DispatchGraph must NOT set ToolName (it has no meaning there and
//     its presence usually means an author copy-pasted a builtin_tool
//     manifest without dropping the field).
//
// Returns issues in ID-sorted order (deterministic for test/CI output).
// An empty catalog or one containing only archetypes returns no issues.
func ValidateDispatch(cat *Catalog) []DispatchIssue {
	if cat == nil {
		return nil
	}
	var issues []DispatchIssue
	for _, rm := range cat.listSorted() {
		m := rm.Manifest
		if m.IsArchetype() {
			continue
		}
		switch m.Dispatch {
		case DispatchGraph:
			if m.ToolName != "" {
				issues = append(issues, DispatchIssue{
					ID:     m.ID,
					Reason: fmt.Sprintf("dispatch: graph must not set tool_name (got %q)", m.ToolName),
				})
			}
		case DispatchBuiltinTool:
			if strings.TrimSpace(m.ToolName) == "" {
				issues = append(issues, DispatchIssue{
					ID:     m.ID,
					Reason: "dispatch: builtin_tool requires a non-empty tool_name",
				})
			}
		default:
			issues = append(issues, DispatchIssue{
				ID:     m.ID,
				Reason: fmt.Sprintf("dispatch must be %q or %q, got %q", DispatchGraph, DispatchBuiltinTool, m.Dispatch),
			})
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	return issues
}

// GraphDispatchKindIDs returns the IDs of every callable manifest
// declaring dispatch: graph, sorted. This is the set
// scripts/ci/check-node-dispatch.sh (via a small go run helper) and
// core/agentgraph's registry cross-check test compare against
// newExecutorRegistry()'s key set — see docs/wiring-audit.md's "WP01"
// section for the full contract.
func GraphDispatchKindIDs(cat *Catalog) []string {
	if cat == nil {
		return nil
	}
	var out []string
	for _, rm := range cat.listSorted() {
		if rm.Manifest.IsArchetype() {
			continue
		}
		if rm.Manifest.Dispatch == DispatchGraph {
			out = append(out, rm.Manifest.ID)
		}
	}
	sort.Strings(out)
	return out
}

// BuiltinToolDispatchKindIDs returns the IDs of every callable manifest
// declaring dispatch: builtin_tool, sorted, alongside the tool name
// each dispatches through.
func BuiltinToolDispatchKindIDs(cat *Catalog) map[string]string {
	if cat == nil {
		return nil
	}
	out := map[string]string{}
	for _, rm := range cat.listSorted() {
		if rm.Manifest.IsArchetype() {
			continue
		}
		if rm.Manifest.Dispatch == DispatchBuiltinTool {
			out[rm.Manifest.ID] = rm.Manifest.ToolName
		}
	}
	return out
}
