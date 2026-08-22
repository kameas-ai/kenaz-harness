package workflows

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// topoSort returns the steps in a valid topological execution order
// based on their inputs_from edges. For workflows that don't use
// inputs_from it falls back to the declared order (back-compat).
//
// It also returns the per-step parent sets so the runtime can build
// execution batches without re-computing the graph.
//
// The algorithm is Kahn's BFS — O(V+E). When a cycle is detected it
// reconstructs the offending path and wraps it in ErrWorkflowCycle.
func topoSort(steps []Step) ([]Step, error) {
	n := len(steps)
	if n == 0 {
		return steps, nil
	}

	// Check whether any step uses inputs_from. If not, return as-is.
	hasDAG := false
	for _, st := range steps {
		if len(st.InputsFrom) > 0 {
			hasDAG = true
			break
		}
	}
	if !hasDAG {
		return steps, nil
	}

	// Index step names → positions.
	idx := make(map[string]int, n)
	for i, st := range steps {
		idx[st.Name] = i
	}

	// Build adjacency list: parent → children (dependents).
	children := make([][]int, n)
	inDegree := make([]int, n)

	for i, st := range steps {
		for _, parentName := range st.InputsFrom {
			p := idx[parentName]
			children[p] = append(children[p], i)
			inDegree[i]++
		}
	}

	// Kahn's algorithm: collect nodes with zero in-degree, process
	// them in stable (alphabetical by name) order so the output is
	// deterministic.
	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	sort.Slice(queue, func(a, b int) bool {
		return steps[queue[a]].Name < steps[queue[b]].Name
	})

	sorted := make([]Step, 0, n)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, steps[cur])

		// Gather newly-zero children so we can sort them before
		// appending to keep output deterministic.
		var ready []int
		for _, child := range children[cur] {
			inDegree[child]--
			if inDegree[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Slice(ready, func(a, b int) bool {
			return steps[ready[a]].Name < steps[ready[b]].Name
		})
		queue = append(queue, ready...)
	}

	if len(sorted) != n {
		// Cycle exists. Find it via DFS so we can report the path.
		cyclePath := findCyclePath(steps, idx, children)
		return nil, fmt.Errorf("%w: %s", ErrWorkflowCycle, cyclePath)
	}

	return sorted, nil
}

// findCyclePath uses iterative DFS with a three-colour mark to locate
// one cycle and returns it as a human-readable "a → b → a" string.
// Called only when Kahn's algorithm confirms a cycle exists.
func findCyclePath(steps []Step, idx map[string]int, children [][]int) string {
	n := len(steps)
	const (
		white = 0 // unvisited
		grey  = 1 // in current DFS path
		black = 2 // fully processed
	)
	color := make([]int, n)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}

	var cyclePath string
	var dfs func(v int) bool
	dfs = func(v int) bool {
		color[v] = grey
		for _, w := range children[v] {
			if color[w] == grey {
				// Reconstruct cycle: walk parent chain from v back to w.
				path := []string{steps[w].Name}
				cur := v
				for cur != w {
					path = append([]string{steps[cur].Name}, path...)
					cur = parent[cur]
				}
				path = append([]string{steps[w].Name}, path...)
				cyclePath = strings.Join(path, " → ")
				return true
			}
			if color[w] == white {
				parent[w] = v
				if dfs(w) {
					return true
				}
			}
		}
		color[v] = black
		return false
	}

	for i := 0; i < n; i++ {
		if color[i] == white {
			if dfs(i) {
				return cyclePath
			}
		}
	}
	return "unknown"
}

// FileSizeCap is the WP01 NFR-003 ceiling on a single workflow file.
const FileSizeCap = 256 * 1024

// LoadYAML parses a single YAML payload into a Workflow and runs the
// load-time validator.
//
// LoadYAML uses ValidateForLoad, not ValidateForSave (UNIT-13, A-10):
// it is the parse step shared by Store.Load (an already-persisted
// workflow, which must keep loading even if a since-narrowed field
// like rerun_policy is set — see storage.go's Load and X-11) and by
// LoadBuiltins (trusted embedded content). Callers that ARE authoring
// a new workflow — ImportYAML's fresh-id recheck, Store.Save — apply
// ValidateForSave themselves on top of this parse.
func LoadYAML(data []byte) (Workflow, error) {
	if len(data) > FileSizeCap {
		return Workflow{}, fmt.Errorf("%w: %d bytes", ErrFileTooLarge, len(data))
	}
	var w Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return Workflow{}, fmt.Errorf("workflows: yaml unmarshal: %w", err)
	}
	if err := ValidateForLoad(w); err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// MarshalYAML returns the canonical YAML serialization of w.
func MarshalYAML(w Workflow) ([]byte, error) {
	return yaml.Marshal(w)
}

//go:embed builtin/*.yaml
var builtinFS embed.FS

// LoadBuiltins returns every workflow embedded under
// core/workflows/builtin/. Files that fail validation are skipped
// with their error returned alongside the successful results — the
// caller decides whether to log or fail-fast.
func LoadBuiltins() ([]Workflow, []error) {
	return loadFromFS(builtinFS, "builtin")
}

func loadFromFS(fsys fs.FS, root string) ([]Workflow, []error) {
	var out []Workflow
	var errs []error
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		// Empty embed (no files matched) is not an error — return
		// nil slices.
		return nil, nil
	}
	// Stable order so callers see deterministic listings.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, root+"/"+e.Name())
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", e.Name(), err))
			continue
		}
		w, err := LoadYAML(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("load %s: %w", e.Name(), err))
			continue
		}
		out = append(out, w)
	}
	return out, errs
}
