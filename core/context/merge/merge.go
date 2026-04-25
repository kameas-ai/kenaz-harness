package merge

import (
	"fmt"
	"sort"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
	"github.com/sigil-tech/kaneaz-harness/core/context/policy"
	"github.com/sigil-tech/kaneaz-harness/core/context/scope"
)

// Request bundles the inputs needed for one merge pass. Layers may
// appear in any order — the merger sorts them by precedence internally.
// Empty / nil layers are tolerated and produce no [LayerActivation].
type Request struct {
	Layers   []LayerInput
	Workflow string
	Agent    string
	Policy   policy.Resolution
}

// Merge produces a [Result] from a [Request]. The output is byte-stable:
// repeating Merge with the same inputs yields the same result, in the
// same order. This is required for SC-005 replay byte-identity.
//
// Behaviour summary:
//
//   - Layers are processed in spec-declared precedence (personal > team >
//     org). Within each layer, scope filtering (FR-015) runs first, then
//     size-budget trimming.
//   - For each entry name, the highest-precedence layer wins. Lower
//     layers' entries with the same name are recorded as
//     [OverrideRecord]s.
//   - In ConflictFail mode the merger returns [policy.ErrConflict]
//     listing every collision (no partial results).
//   - In BudgetHardFail mode the merger returns [policy.ErrOversizeLayer]
//     for the first oversize layer.
func Merge(req Request) (*Result, error) {
	pol := req.Policy.Normalised()
	layers := sortLayersByPrecedence(req.Layers)

	res := &Result{}
	chosen := map[string]ResolvedEntry{}
	losers := map[string][]LayerInput{}
	collisions := map[string]*policy.ConflictReport{}

	for _, li := range layers {
		if li.Empty() {
			continue
		}
		// Step 1: workflow / agent scope filtering.
		filtered := scope.Filter(li.Pack.Entries, req.Workflow, req.Agent)
		// Step 2: budget enforcement (per-layer).
		budgeted, trimmed, warn, err := applyBudget(li, filtered, pol)
		if err != nil {
			return nil, err
		}

		act := LayerActivation{
			Layer:    li.Layer,
			Pack:     li.Pack.Ref,
			Entries:  len(budgeted),
			Trimmed:  trimmed,
			Warnings: warn,
		}
		res.Layers = append(res.Layers, act)
		if len(warn) > 0 {
			res.Warnings = append(res.Warnings, warn...)
		}

		// Step 3: override accumulation. Higher-precedence layers come
		// first because of sortLayersByPrecedence.
		for _, e := range budgeted {
			if existing, dup := chosen[e.Name]; dup {
				// existing wins (it came from a higher-precedence layer).
				losers[e.Name] = append(losers[e.Name], li)
				_ = existing
				if pol.Conflict == policy.ConflictFail {
					recordCollision(collisions, e, existing, li.Layer)
				}
				continue
			}
			re := ResolvedEntry{ContextEntry: e, Winner: li.Layer}
			chosen[e.Name] = re
		}
	}

	// Strict mode: refuse a snapshot if any collisions were detected.
	if pol.Conflict == policy.ConflictFail && len(collisions) > 0 {
		reports := make([]policy.ConflictReport, 0, len(collisions))
		names := make([]string, 0, len(collisions))
		for n := range collisions {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			reports = append(reports, *collisions[n])
		}
		return nil, &policy.ErrConflict{Conflicts: reports}
	}

	// Build override records in deterministic order.
	loserNames := make([]string, 0, len(losers))
	for n := range losers {
		loserNames = append(loserNames, n)
	}
	sort.Strings(loserNames)
	for _, name := range loserNames {
		winner := chosen[name]
		for _, l := range losers[name] {
			loserEntry, ok := l.Pack.EntryByName(name)
			if !ok {
				continue
			}
			res.Overrides = append(res.Overrides, OverrideRecord{
				EntryName:  name,
				Winner:     winner.Winner,
				WinnerPack: winner.SourcePack,
				Loser:      l.Layer,
				LoserPack:  loserEntry.SourcePack,
			})
		}
	}

	// Final entries in deterministic name order.
	names := make([]string, 0, len(chosen))
	for n := range chosen {
		names = append(names, n)
	}
	sort.Strings(names)
	res.Entries = make([]ResolvedEntry, 0, len(names))
	for _, n := range names {
		res.Entries = append(res.Entries, chosen[n])
	}

	return res, nil
}

// sortLayersByPrecedence orders inputs so that higher-precedence layers
// come first (personal > team > org). When two layers carry the same
// designation (callers should not, but be defensive) we preserve input
// order via stable sort.
func sortLayersByPrecedence(in []LayerInput) []LayerInput {
	out := make([]LayerInput, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Layer.Precedence() > out[j].Layer.Precedence()
	})
	return out
}

// applyBudget enforces the policy's layer size budget for one layer. In
// BudgetSoftWarn mode it keeps entries by name order until the budget is
// reached, attaches the trimmed names as a structured warning, and
// returns the kept slice. In BudgetHardFail mode it returns
// [policy.ErrOversizeLayer] when total size exceeds the budget.
func applyBudget(li LayerInput, entries []pack.ContextEntry, pol policy.Resolution) (
	[]pack.ContextEntry, []string, []pack.Warning, error,
) {
	budget := pol.EffectiveBudget()
	if budget <= 0 {
		return entries, nil, nil, nil
	}

	// Sort by name so trimming is deterministic.
	sorted := make([]pack.ContextEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var (
		total   int64
		kept    []pack.ContextEntry
		trimmed []string
	)
	for _, e := range sorted {
		if total+e.SizeBytes > budget {
			trimmed = append(trimmed, e.Name)
			continue
		}
		total += e.SizeBytes
		kept = append(kept, e)
	}

	if len(trimmed) == 0 {
		return sorted, nil, nil, nil
	}
	if pol.BudgetMode == policy.BudgetHardFail {
		return nil, nil, nil, &policy.ErrOversizeLayer{
			Layer:   li.Layer,
			Pack:    li.Pack.Ref,
			Bytes:   sumSize(sorted),
			Budget:  budget,
			Trimmed: trimmed,
		}
	}
	warn := []pack.Warning{{
		Code:    string(pack.CodeOversizeLayer),
		Subject: fmt.Sprintf("%s/%s", li.Layer, li.Pack.Ref.Name),
		Message: fmt.Sprintf("trimmed %d entries to fit %d-byte layer budget", len(trimmed), budget),
	}}
	return kept, trimmed, warn, nil
}

func sumSize(in []pack.ContextEntry) int64 {
	var s int64
	for _, e := range in {
		s += e.SizeBytes
	}
	return s
}

// recordCollision stores a [policy.ConflictReport] for the first
// observation of a given entry name. Subsequent collisions on the same
// name are folded into the same report's Reason field.
func recordCollision(m map[string]*policy.ConflictReport, candidate pack.ContextEntry,
	winner ResolvedEntry, loserLayer pack.Layer) {
	if _, ok := m[candidate.Name]; ok {
		return
	}
	m[candidate.Name] = &policy.ConflictReport{
		EntryName:  candidate.Name,
		LeftLayer:  winner.Winner,
		LeftPack:   winner.SourcePack,
		RightLayer: loserLayer,
		RightPack:  candidate.SourcePack,
		Resolved:   false,
		Reason:     "fail-on-conflict policy: layered definitions collide on entry name",
	}
}
