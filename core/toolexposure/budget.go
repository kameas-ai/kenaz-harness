package toolexposure

import "sort"

// BudgetWindowPercent caps the schema budget at this share of the
// model's context window (spec §2.3, FR-B1).
const BudgetWindowPercent = 15

// EffectiveBudget is the schema budget one call is fitted to:
// min(setting, 15 % of window). A window <= 0 is unknown and leaves the
// setting; a setting <= 0 is the harness default. The window cap is at
// least 1 token, so a tiny window yields a budget of 1 (every evictable
// tool goes) rather than 0, which FitBudget reads as "no budget".
func EffectiveBudget(setting, window int) int {
	if setting <= 0 {
		setting = DefaultSchemaBudgetTokens
	}
	if window <= 0 {
		return setting
	}
	capped := window * BudgetWindowPercent / 100
	if capped < 1 {
		capped = 1
	}
	if capped < setting {
		return capped
	}
	return setting
}

// BudgetFit is a Partition fitted to a schema budget. The budget covers
// the tool definitions in the tools array only; the digest text (the
// per-call system section listing what is not loaded) is not counted.
type BudgetFit struct {
	// Partition is what the call sends: Hot unchanged, Pinned and
	// Activated without the evicted tools, which are added to Digest
	// (re-sorted by name) so the digest lists their servers again.
	Partition Partition
	// Budget is the budget the fit was made against.
	Budget int
	// Tokens is the sum of the sent tools' sizes after eviction.
	Tokens int
	// OverBy is how far the unfitted send set exceeded Budget (0 when
	// it fit).
	OverBy int
	// Evicted are the tools taken out, in eviction order: the activated
	// segment from its tail (least recently used first), then the pinned
	// segment from its tail, then protected tools (activated, then
	// pinned, each from its tail).
	Evicted []ResolvedTool
	// PinnedEvicted is how many of Evicted came from the pinned segment.
	PinnedEvicted int
	// ProtectedEvicted names the protected tools that were evicted
	// anyway: they do not fit even with every other loaded tool gone.
	ProtectedEvicted []string
	// PinnedOverBy is the part of the overage the pinned segment
	// accounts for once the activated segment was exhausted:
	// min(remaining overage, size of the pinned segment); 0 when there
	// are no pinned tools or evicting activated tools was enough. It is
	// the N of "Pinned tools exceed the schema budget by N tokens".
	PinnedOverBy int
	// HotOverBy is how far the hot segment alone exceeds Budget: the
	// model's window is too small for the always-sent core tools.
	HotOverBy int
	// Remaining is how far the sent set still exceeds Budget: the hot
	// set and immune tools are never evicted.
	Remaining int
}

// FitBudget evicts tools until the send set's size fits budget (spec
// §2.3): the activated segment from its tail first, then the pinned
// segment from its tail; the hot segment never. size returns one tool's
// token estimate.
//
// immune, when non-nil, names tools that are never evicted (a tool the
// model called this turn). protected, when non-nil, names tools evicted
// only after every other activated and pinned tool is gone (a tool the
// model loaded this turn); those evicted anyway are listed in
// ProtectedEvicted. A budget <= 0 evicts nothing.
func (p Partition) FitBudget(budget int, size func(ResolvedTool) int, immune, protected func(name string) bool) BudgetFit {
	fit := BudgetFit{Budget: budget}
	segSize := func(seg []ResolvedTool) int {
		n := 0
		for _, t := range seg {
			n += size(t)
		}
		return n
	}
	hot, pinned := segSize(p.Hot), segSize(p.Pinned)
	total := hot + pinned + segSize(p.Activated)
	if budget > 0 && hot > budget {
		fit.HotOverBy = hot - budget
	}
	over := total - budget
	if budget <= 0 || over <= 0 {
		fit.Partition = p
		fit.Tokens = total
		return fit
	}
	fit.OverBy = over

	is := func(f func(string) bool, name string) bool { return f != nil && f(name) }
	evicted := map[string]bool{}
	evictFrom := func(seg []ResolvedTool, takeProtected bool) int {
		n := 0
		for i := len(seg) - 1; i >= 0 && over > 0; i-- {
			t := seg[i]
			if evicted[t.Name] || is(immune, t.Name) || is(protected, t.Name) != takeProtected {
				continue
			}
			evicted[t.Name] = true
			fit.Evicted = append(fit.Evicted, t)
			if takeProtected {
				fit.ProtectedEvicted = append(fit.ProtectedEvicted, t.Name)
			}
			over -= size(t)
			n++
		}
		return n
	}
	evictFrom(p.Activated, false)
	if over > 0 && len(p.Pinned) > 0 {
		fit.PinnedOverBy = min(over, pinned)
		fit.PinnedEvicted = evictFrom(p.Pinned, false)
	}
	if over > 0 {
		evictFrom(p.Activated, true)
		fit.PinnedEvicted += evictFrom(p.Pinned, true)
	}
	if over > 0 {
		fit.Remaining = over
	}
	fit.Tokens = total
	for _, t := range fit.Evicted {
		fit.Tokens -= size(t)
	}

	keep := func(seg []ResolvedTool) []ResolvedTool {
		var out []ResolvedTool
		for _, t := range seg {
			if !evicted[t.Name] {
				out = append(out, t)
			}
		}
		return out
	}
	fit.Partition = Partition{
		Hot:       p.Hot,
		Pinned:    keep(p.Pinned),
		Activated: keep(p.Activated),
		Digest:    append(append([]ResolvedTool(nil), p.Digest...), fit.Evicted...),
		Stopped:   p.Stopped,
	}
	sort.SliceStable(fit.Partition.Digest, func(i, j int) bool {
		return fit.Partition.Digest[i].Name < fit.Partition.Digest[j].Name
	})
	return fit
}

// ActivationExpired reports whether a is past its TTL at turn: it is not
// sticky and more than ttl turns have passed since the turn it was last
// loaded or called on. A ttl <= 0 is the harness default. An activation
// stamped later than turn (a session whose turn count restarted) is not
// expired.
func ActivationExpired(a Activation, turn, ttl int) bool {
	if a.Sticky {
		return false
	}
	if ttl <= 0 {
		ttl = DefaultActivationTTLTurns
	}
	return turn-a.LastUsedTurn > ttl
}

// ExpireActivations splits as into the activations still live at turn
// and the expired ones, each in input order.
func ExpireActivations(as []Activation, turn, ttl int) (kept, expired []Activation) {
	for _, a := range as {
		if ActivationExpired(a, turn, ttl) {
			expired = append(expired, a)
			continue
		}
		kept = append(kept, a)
	}
	return kept, expired
}
