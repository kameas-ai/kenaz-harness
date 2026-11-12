package subagentdispatch

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	coreagents "github.com/kameas-ai/kenaz-harness/core/agents"
)

// writeToolNames are the builtins that change the workspace.
var writeToolNames = []string{"kenaz__write_file", "kenaz__edit_file", "kenaz__bash"}

// writeCapable reports whether a profile is INTENDED to write. Profiles'
// allowed/denied lists are not enforced (see the package doc), so this is
// the profile's declared intent — the right input for a collision warning,
// not a containment decision.
func writeCapable(p coreagents.Profile) bool {
	denied := map[string]bool{}
	for _, d := range p.DeniedTools {
		denied[d] = true
	}
	if len(p.AllowedTools) == 0 {
		for _, w := range writeToolNames {
			if !denied[w] {
				return true
			}
		}
		return false
	}
	for _, a := range p.AllowedTools {
		for _, w := range writeToolNames {
			if a == w && !denied[w] {
				return true
			}
		}
	}
	return false
}

// normalizeScopes cleans declared path scopes; no scope means the whole
// workspace (".").
func normalizeScopes(in []string) []string {
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = path.Clean(strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./"))
		if p == "/" || p == "" {
			p = "."
		}
		out = append(out, strings.TrimSuffix(p, "/"))
	}
	if len(out) == 0 {
		return []string{"."}
	}
	sort.Strings(out)
	return out
}

func scopesOverlap(a, b string) bool {
	if a == "." || b == "." || a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

type activeWriter struct {
	token   int
	label   string
	profile string
	scopes  []string
}

// overlapTracker remembers the write-capable, NON-isolated sub-agents
// running per parent session so a second dispatch into overlapping paths
// can be warned about.
type overlapTracker struct {
	mu   sync.Mutex
	next int
	by   map[string][]activeWriter
}

// register records a writer and returns warnings about overlaps with
// writers already running for parent, plus a release func.
func (t *overlapTracker) register(parent, label, profile string, scopes []string) ([]string, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string][]activeWriter{}
	}
	var warnings []string
	for _, w := range t.by[parent] {
		for _, a := range scopes {
			hit := false
			for _, b := range w.scopes {
				if scopesOverlap(a, b) {
					warnings = append(warnings, fmt.Sprintf(
						"write-capable sub-agent %q (%s) is already running on overlapping paths (%s vs %s) without isolation — the two can clobber each other's edits; give them disjoint `paths` or use isolation='worktree'",
						w.label, w.profile, strings.Join(scopes, ","), strings.Join(w.scopes, ",")))
					hit = true
					break
				}
			}
			if hit {
				break
			}
		}
	}
	t.next++
	tok := t.next
	t.by[parent] = append(t.by[parent], activeWriter{token: tok, label: label, profile: profile, scopes: scopes})
	var once sync.Once
	return warnings, func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			ws := t.by[parent]
			for i, w := range ws {
				if w.token == tok {
					t.by[parent] = append(ws[:i:i], ws[i+1:]...)
					break
				}
			}
			if len(t.by[parent]) == 0 {
				delete(t.by, parent)
			}
		})
	}
}
