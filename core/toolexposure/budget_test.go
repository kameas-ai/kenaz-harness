package toolexposure

import (
	"context"
	"reflect"
	"testing"
)

// TestEffectiveBudget_Boundaries is FR-B1: min(setting, 15 % of window),
// the setting when the window is unknown, the default when unset.
func TestEffectiveBudget_Boundaries(t *testing.T) {
	cases := []struct {
		name            string
		setting, window int
		want            int
	}{
		{"setting below the window cap", 24000, 200000, 24000},
		{"window cap below the setting", 24000, 131072, 19660},
		{"setting exactly 15 % of the window", 15000, 100000, 15000},
		{"one token over the cap", 15001, 100000, 15000},
		{"unknown window keeps the setting", 24000, 0, 24000},
		{"negative window is unknown", 9000, -1, 9000},
		{"unset setting is the default", 0, 0, DefaultSchemaBudgetTokens},
		{"unset setting capped by a small window", 0, 32000, 4800},
		{"tiny window clamps the cap to 1, never 0", 24000, 5, 1},
	}
	for _, c := range cases {
		if got := EffectiveBudget(c.setting, c.window); got != c.want {
			t.Errorf("%s: EffectiveBudget(%d, %d) = %d, want %d", c.name, c.setting, c.window, got, c.want)
		}
	}
}

// budgetPartition plants sizes: hot a, b (100 each); pinned p1, p2 (50
// each); activated x, y, z (30 each), most recently used first.
func budgetPartition() (Partition, func(ResolvedTool) int) {
	rt := func(n string) ResolvedTool { return ResolvedTool{Name: n, Server: n[:1], Running: true} }
	p := Partition{
		Hot:       []ResolvedTool{rt("a"), rt("b")},
		Pinned:    []ResolvedTool{rt("p1"), rt("p2")},
		Activated: []ResolvedTool{rt("x"), rt("y"), rt("z")},
		Digest:    []ResolvedTool{rt("m")},
	}
	sizes := map[string]int{"a": 100, "b": 100, "p1": 50, "p2": 50, "x": 30, "y": 30, "z": 30}
	return p, func(t ResolvedTool) int { return sizes[t.Name] }
}

// TestFitBudget_EvictionOrder is FR-B2: activated from the tail (least
// recently used) first, then pinned from the tail with PinnedOverBy set,
// never hot; evicted tools are listed in the digest.
func TestFitBudget_EvictionOrder(t *testing.T) {
	cases := []struct {
		name          string
		budget        int
		evicted       []string
		send          []string
		overBy        int
		pinnedOverBy  int
		pinnedEvicted int
		remaining     int
		hotOverBy     int
	}{
		{"fits", 390, nil, []string{"a", "b", "p1", "p2", "x", "y", "z"}, 0, 0, 0, 0, 0},
		{"activated tail only", 350, []string{"z", "y"}, []string{"a", "b", "p1", "p2", "x"}, 40, 0, 0, 0, 0},
		{"activated exhausted, then pinned with warning", 250, []string{"z", "y", "x", "p2"}, []string{"a", "b", "p1"}, 140, 50, 1, 0, 0},
		// After the activated segment, 200 over; pinned (100) accounts
		// for 100 of it, the hot set for the other 100.
		{"hot alone over budget is never evicted", 100, []string{"z", "y", "x", "p2", "p1"}, []string{"a", "b"}, 290, 100, 2, 100, 100},
	}
	for _, c := range cases {
		p, size := budgetPartition()
		fit := p.FitBudget(c.budget, size, nil, nil)
		if got := names(fit.Evicted); !reflect.DeepEqual(got, append([]string{}, c.evicted...)) {
			t.Errorf("%s: evicted = %v, want %v", c.name, got, c.evicted)
		}
		if got := fit.Partition.SendNames(); !reflect.DeepEqual(got, c.send) {
			t.Errorf("%s: send = %v, want %v", c.name, got, c.send)
		}
		if fit.OverBy != c.overBy || fit.PinnedOverBy != c.pinnedOverBy || fit.PinnedEvicted != c.pinnedEvicted || fit.Remaining != c.remaining || fit.HotOverBy != c.hotOverBy {
			t.Errorf("%s: overBy=%d pinnedOverBy=%d pinnedEvicted=%d remaining=%d hotOverBy=%d, want %d %d %d %d %d",
				c.name, fit.OverBy, fit.PinnedOverBy, fit.PinnedEvicted, fit.Remaining, fit.HotOverBy,
				c.overBy, c.pinnedOverBy, c.pinnedEvicted, c.remaining, c.hotOverBy)
		}
		// Every evicted tool is back in the digest, which stays sorted.
		digest := names(fit.Partition.Digest)
		for _, e := range c.evicted {
			found := false
			for _, d := range digest {
				found = found || d == e
			}
			if !found {
				t.Errorf("%s: evicted %s missing from digest %v", c.name, e, digest)
			}
		}
		for i := 1; i < len(digest); i++ {
			if digest[i-1] > digest[i] {
				t.Errorf("%s: digest not sorted: %v", c.name, digest)
			}
		}
		sent := 0
		for _, n := range fit.Partition.SendNames() {
			sent += size(ResolvedTool{Name: n})
		}
		if fit.Tokens != sent {
			t.Errorf("%s: Tokens = %d, want the sent sizes' sum %d", c.name, fit.Tokens, sent)
		}
	}
}

// TestFitBudget_ImmuneSkipped: a tool used this turn is never evicted,
// even at the activated tail; eviction moves on to the next one.
func TestFitBudget_ImmuneSkipped(t *testing.T) {
	p, size := budgetPartition()
	fit := p.FitBudget(350, size, func(n string) bool { return n == "z" }, nil)
	if got := names(fit.Evicted); !reflect.DeepEqual(got, []string{"y", "x"}) {
		t.Fatalf("evicted = %v, want [y x] (z immune)", got)
	}
	fit = p.FitBudget(220, size, func(n string) bool { return n == "p2" || n == "z" }, nil)
	if got := names(fit.Evicted); !reflect.DeepEqual(got, []string{"y", "x", "p1"}) {
		t.Fatalf("evicted = %v, want [y x p1] (z and p2 immune)", got)
	}
}

// TestFitBudget_PinnedOverByIsPinnedOnly is the warning's N: capped at
// the pinned segment's own size, with the rest of the overage reported
// against the hot set; no pinned tools means no pinned warning.
func TestFitBudget_PinnedOverByIsPinnedOnly(t *testing.T) {
	p, size := budgetPartition()
	fit := p.FitBudget(50, size, nil, nil)
	if fit.PinnedOverBy != 100 || fit.HotOverBy != 150 {
		t.Fatalf("budget 50: pinnedOverBy=%d hotOverBy=%d, want 100 (pinned total) and 150", fit.PinnedOverBy, fit.HotOverBy)
	}
	p.Pinned = nil
	fit = p.FitBudget(150, size, nil, nil)
	if fit.PinnedOverBy != 0 || fit.HotOverBy != 50 || fit.PinnedEvicted != 0 {
		t.Fatalf("no pinned: pinnedOverBy=%d hotOverBy=%d pinnedEvicted=%d, want 0, 50, 0", fit.PinnedOverBy, fit.HotOverBy, fit.PinnedEvicted)
	}
}

// TestFitBudget_ProtectedGoLast: a tool loaded this turn is evicted only
// after every other activated and pinned tool, and is reported when it
// still does not fit.
func TestFitBudget_ProtectedGoLast(t *testing.T) {
	p, size := budgetPartition()
	prot := func(n string) bool { return n == "z" }
	fit := p.FitBudget(250, size, nil, prot)
	if got := names(fit.Evicted); !reflect.DeepEqual(got, []string{"y", "x", "p2", "p1"}) || len(fit.ProtectedEvicted) != 0 {
		t.Fatalf("budget 250: evicted %v protectedEvicted %v, want [y x p2 p1] and none (z kept)", got, fit.ProtectedEvicted)
	}
	fit = p.FitBudget(180, size, nil, prot)
	if got := names(fit.Evicted); !reflect.DeepEqual(got, []string{"y", "x", "p2", "p1", "z"}) || !reflect.DeepEqual(fit.ProtectedEvicted, []string{"z"}) {
		t.Fatalf("budget 180: evicted %v protectedEvicted %v, want z evicted last and reported", got, fit.ProtectedEvicted)
	}
}

// TestPartitionOrder_StableAcrossResolves: two resolves of an unchanged
// catalog send the same names in the same order, whatever order the
// catalog lists them in (the cacheable-prefix precondition).
func TestPartitionOrder_StableAcrossResolves(t *testing.T) {
	cat := []CatalogTool{
		{Name: "outlook__send-mail", Server: "outlook", Running: true},
		{Name: "kenaz__read_file", Server: BuiltinServer, Running: true},
		{Name: "git__status", Server: "git", Running: true},
		{Name: LoadToolsName, Server: BuiltinServer, Running: true},
		{Name: "fetch__fetch", Server: "fetch", Running: true},
		{Name: "outlook__list-messages", Server: "outlook", Running: true},
	}
	deps := Deps{
		Settings: &countingSettings{},
		Sessions: stateSessions{st: SessionState{
			ProjectID: "p",
			Activations: []Activation{
				{Name: "outlook__send-mail", Server: "outlook", LastUsedTurn: 2},
				{Name: "outlook__list-messages", Server: "outlook", LastUsedTurn: 4},
				{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 1, Sticky: true},
			},
		}},
		Projects: mapProjects{"p": {Servers: map[string]ServerExposure{"git": {Tier: TierFull}}}},
	}
	want := []string{LoadToolsName, "kenaz__read_file", "fetch__fetch", "git__status", "outlook__list-messages", "outlook__send-mail"}
	reversed := make([]CatalogTool, len(cat))
	for i, c := range cat {
		reversed[len(cat)-1-i] = c
	}
	for i, c := range [][]CatalogTool{cat, cat, reversed} {
		rc, err := Resolve(context.Background(), deps, "s", c)
		if err != nil {
			t.Fatal(err)
		}
		if got := rc.Partition().SendNames(); !reflect.DeepEqual(got, want) {
			t.Fatalf("resolve %d: send = %v, want %v", i, got, want)
		}
	}
}

// TestActivationExpiry: a non-sticky activation last used on turn 3
// survives through turn 9 (six unused turns, 4–9) and is gone at turn 10;
// sticky never expires; a stamp later than the turn is not expired; a
// TTL <= 0 is the default.
func TestActivationExpiry(t *testing.T) {
	a := Activation{Name: "o__x", Server: "o", LastUsedTurn: 3}
	for turn, want := range map[int]bool{3: false, 9: false, 10: true, 40: true} {
		if got := ActivationExpired(a, turn, 6); got != want {
			t.Errorf("turn %d: expired = %v, want %v", turn, got, want)
		}
	}
	if ActivationExpired(Activation{Name: "o__x", Server: "o", LastUsedTurn: 0, Sticky: true}, 1000, 6) {
		t.Error("sticky activation expired")
	}
	if ActivationExpired(Activation{Name: "o__x", Server: "o", LastUsedTurn: 12}, 1, 6) {
		t.Error("activation stamped after the current turn expired")
	}
	if !ActivationExpired(a, 3+DefaultActivationTTLTurns+1, 0) || ActivationExpired(a, 3+DefaultActivationTTLTurns, 0) {
		t.Error("ttl 0 is not the default TTL")
	}
	kept, expired := ExpireActivations([]Activation{
		a,
		{Name: "o__y", Server: "o", LastUsedTurn: 8},
		{Name: "o__z", Server: "o", LastUsedTurn: 0, Sticky: true},
	}, 10, 6)
	if len(kept) != 2 || kept[0].Name != "o__y" || kept[1].Name != "o__z" {
		t.Fatalf("kept = %+v, want o__y and o__z", kept)
	}
	if len(expired) != 1 || expired[0].Name != "o__x" {
		t.Fatalf("expired = %+v", expired)
	}
}
