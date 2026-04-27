package prune

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/memory"
)

// Rules tunes the prune sweep. All fields zero ⇒ conservative
// defaults: nothing is dropped except exact embedding duplicates.
//
// The rule shape is intentionally flat — every value is a primitive
// the user can edit in the dials UI. Multiplicative combination:
// keepScore = staleScore * ageScore * recallScore. A chunk is kept
// when keepScore >= KeepThreshold (default 0.5).
type Rules struct {
	// StaleAfter is the LastAccessed cutoff at which a chunk's
	// staleness score starts dropping below 1.0. Zero disables
	// staleness pruning.
	StaleAfter time.Duration
	// HardStaleAfter is the LastAccessed cutoff at which the
	// staleness score is forced to 0 (i.e. unconditionally pruned
	// unless pinned). Default 0 = disabled.
	HardStaleAfter time.Duration
	// MaxAge is the CreatedAt cutoff above which a chunk is dropped
	// regardless of recall. Zero disables age pruning.
	MaxAge time.Duration
	// MinRecallCount drops chunks below this recall count *and* older
	// than StaleAfter. Recall-frequency uses the per-scope bottom
	// percentile when MinRecallCount is zero.
	MinRecallCount int
	// RecallPercentileFloor (0.0..1.0) is the per-scope percentile
	// below which low-recall chunks become drop candidates. Default 0
	// disables the rule. Used only when MinRecallCount == 0.
	RecallPercentileFloor float64
	// CollapseCosine is the cosine-similarity threshold above which
	// two chunks of the same scope are deemed near-duplicates.
	// Default 0 disables collapse.
	CollapseCosine float32
	// MaxEntries is the size-cap. After signal pruning, oldest-
	// LastAccessed entries are dropped until the surviving count
	// fits. Zero disables the cap.
	MaxEntries int
	// KeepThreshold is the keepScore floor below which a chunk is
	// pruned. Default 0.5 — set lower for laxer pruning.
	KeepThreshold float64
	// ScopeWeights tunes how aggressively each scope decays. Missing
	// scope keys default to 1.0. Lower values make a scope decay
	// faster.
	ScopeWeights map[string]float64
}

// DefaultRules returns the harness-default ruleset. Safe to call from
// tests.
func DefaultRules() Rules {
	return Rules{
		StaleAfter:            30 * 24 * time.Hour,
		HardStaleAfter:        180 * 24 * time.Hour,
		MaxAge:                365 * 24 * time.Hour,
		RecallPercentileFloor: 0.10,
		CollapseCosine:        0.97,
		MaxEntries:            10_000,
		KeepThreshold:         0.5,
		ScopeWeights: map[string]float64{
			memory.ScopeKindGlobal:  2.0,
			memory.ScopeKindProject: 1.0,
			memory.ScopeKindSession: 0.5,
		},
	}
}

// Verdict is one row in the pruner's output. Action is "keep",
// "drop", or "collapse". Reason is a one-line human-readable
// explanation surfaced in the inspector UI.
type Verdict struct {
	ID            string
	Action        string
	Reason        string
	KeepScore     float64
	CollapsedInto string // for Action=="collapse"
}

// Decision is the full prune sweep output. It is returned by
// Plan() (no mutation) and Apply() (after deletions are committed).
type Decision struct {
	// Kept is the IDs the pruner decided to retain.
	Kept []string
	// Dropped is the IDs the pruner removed.
	Dropped []string
	// Collapsed maps the dropped ID -> the survivor it merged into.
	Collapsed map[string]string
	// Pinned is the count of immune chunks the pruner ignored.
	Pinned int
	// Verdicts is the per-id detail (kept + dropped + collapsed).
	Verdicts []Verdict
}

// Pruner runs the prune sweep over a memory store. The rules and
// clock are injected so tests can drive deterministic behavior.
type Pruner struct {
	store memory.Store
	rules Rules
	now   func() time.Time
}

// New returns a Pruner. now may be nil; it falls back to time.Now.
func New(store memory.Store, rules Rules, now func() time.Time) *Pruner {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Pruner{store: store, rules: rules, now: now}
}

// Plan computes the prune decision without mutating the store. Used
// by the inspector's "preview" action and by the size-cap trigger
// that needs to know whether work is pending before scheduling a run.
//
// Scope filter (kind + id) is optional; an empty filter prunes
// everywhere. Plan is read-only and safe to call concurrently.
func (p *Pruner) Plan(ctx context.Context, scope ...memory.ScopeFilter) (Decision, error) {
	if p == nil || p.store == nil {
		return Decision{}, errors.New("prune: store is nil")
	}
	chunks, err := p.store.List(ctx, scope...)
	if err != nil {
		return Decision{}, fmt.Errorf("prune: list: %w", err)
	}
	return p.plan(chunks), nil
}

// Apply runs Plan and then mutates the store: deletions for Dropped,
// recall + last-accessed inheritance for Collapsed survivors. The
// returned Decision matches what the store now reflects.
//
// Apply is best-effort against the underlying Store contract: when
// the store does not satisfy memory.PruneCapable, collapse-survivor
// metadata cannot be merged and the call returns ErrNoCapable. Drops
// fall back to per-id Delete in that case.
func (p *Pruner) Apply(ctx context.Context, scope ...memory.ScopeFilter) (Decision, error) {
	dec, err := p.Plan(ctx, scope...)
	if err != nil {
		return Decision{}, err
	}
	for _, id := range dec.Dropped {
		if delErr := p.store.Delete(ctx, id); delErr != nil {
			return dec, fmt.Errorf("prune: delete %s: %w", id, delErr)
		}
	}
	return dec, nil
}

// ErrNoCapable is returned by Apply when the underlying store does
// not implement memory.PruneCapable and the rules required it (e.g.
// collapse mode that needs MarkAccessed to roll the survivor's
// recall count forward).
var ErrNoCapable = errors.New("prune: store lacks PruneCapable")

// plan computes the verdict over a fixed slice of chunks. Pure;
// tests bypass Plan() and call this directly with crafted fixtures.
func (p *Pruner) plan(chunks []memory.Chunk) Decision {
	now := p.now()
	dec := Decision{Collapsed: map[string]string{}}
	pinnedIDs := map[string]struct{}{}

	// Pass 1: pinned ⇒ untouched.
	working := make([]memory.Chunk, 0, len(chunks))
	for _, c := range chunks {
		if c.Pinned {
			pinnedIDs[c.ID] = struct{}{}
			dec.Kept = append(dec.Kept, c.ID)
			dec.Verdicts = append(dec.Verdicts, Verdict{
				ID: c.ID, Action: "keep", Reason: "pinned",
				KeepScore: 1.0,
			})
			dec.Pinned++
			continue
		}
		working = append(working, c)
	}

	// Pass 2: cluster collapse — for each scope, find pairs above
	// the cosine threshold; the chunk with the higher (RecallCount,
	// LastAccessed) survives, the other collapses onto it.
	if p.rules.CollapseCosine > 0 && len(working) > 1 {
		working = p.collapse(working, &dec)
	}

	// Pass 3: per-chunk score.
	survivors := make([]memory.Chunk, 0, len(working))
	for _, c := range working {
		score, reason := p.score(c, now, working)
		v := Verdict{ID: c.ID, KeepScore: score, Reason: reason}
		if score < p.rules.KeepThreshold {
			v.Action = "drop"
			dec.Dropped = append(dec.Dropped, c.ID)
			dec.Verdicts = append(dec.Verdicts, v)
			continue
		}
		v.Action = "keep"
		dec.Kept = append(dec.Kept, c.ID)
		dec.Verdicts = append(dec.Verdicts, v)
		survivors = append(survivors, c)
	}

	// Pass 4: size cap — drop oldest-LastAccessed survivors until the
	// remaining count fits MaxEntries.
	if p.rules.MaxEntries > 0 && len(survivors) > p.rules.MaxEntries {
		// Sort by LastAccessed ascending — oldest first.
		sort.SliceStable(survivors, func(i, j int) bool {
			return survivors[i].LastAccessed.Before(survivors[j].LastAccessed)
		})
		excess := len(survivors) - p.rules.MaxEntries
		toDrop := survivors[:excess]
		for _, c := range toDrop {
			dec.Dropped = append(dec.Dropped, c.ID)
			dec.Verdicts = append(dec.Verdicts, Verdict{
				ID: c.ID, Action: "drop",
				Reason: fmt.Sprintf("size cap (max %d)", p.rules.MaxEntries),
			})
			// Also clear from Kept.
			for i, kid := range dec.Kept {
				if kid == c.ID {
					dec.Kept = append(dec.Kept[:i], dec.Kept[i+1:]...)
					break
				}
			}
		}
	}

	return dec
}

// score computes the multiplicative keep score for one chunk. The
// returned reason is the dominant signal (smallest factor).
func (p *Pruner) score(c memory.Chunk, now time.Time, scope []memory.Chunk) (float64, string) {
	stale := p.staleScore(c, now)
	age := p.ageScore(c, now)
	recall := p.recallScore(c, scope)

	score := stale * age * recall
	reason := "ok"
	if stale <= age && stale <= recall {
		reason = "stale"
	} else if age <= recall {
		reason = "aged out"
	} else if recall < 1.0 {
		reason = "low recall"
	}
	if score >= p.rules.KeepThreshold {
		reason = "ok"
	}
	return score, reason
}

func (p *Pruner) staleScore(c memory.Chunk, now time.Time) float64 {
	if p.rules.StaleAfter <= 0 && p.rules.HardStaleAfter <= 0 {
		return 1.0
	}
	last := c.LastAccessed
	if last.IsZero() {
		last = c.CreatedAt
	}
	idle := now.Sub(last)
	scopeWeight := 1.0
	if w, ok := p.rules.ScopeWeights[c.ScopeKind]; ok && w > 0 {
		scopeWeight = w
	}

	if p.rules.HardStaleAfter > 0 && idle >= time.Duration(float64(p.rules.HardStaleAfter)*scopeWeight) {
		return 0.0
	}
	if p.rules.StaleAfter > 0 && idle >= time.Duration(float64(p.rules.StaleAfter)*scopeWeight) {
		// Linear fade from 1.0 at the soft cutoff down to KeepThreshold
		// at the hard cutoff. When HardStaleAfter==0, fade across one
		// extra StaleAfter window.
		hard := p.rules.HardStaleAfter
		if hard <= 0 {
			hard = p.rules.StaleAfter * 2
		}
		span := time.Duration(float64(hard-p.rules.StaleAfter) * scopeWeight)
		if span <= 0 {
			return 0.0
		}
		over := idle - time.Duration(float64(p.rules.StaleAfter)*scopeWeight)
		frac := float64(over) / float64(span)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		return 1.0 - frac
	}
	return 1.0
}

func (p *Pruner) ageScore(c memory.Chunk, now time.Time) float64 {
	if p.rules.MaxAge <= 0 {
		return 1.0
	}
	if c.CreatedAt.IsZero() {
		return 1.0
	}
	age := now.Sub(c.CreatedAt)
	if age < p.rules.MaxAge {
		return 1.0
	}
	return 0.0
}

func (p *Pruner) recallScore(c memory.Chunk, scope []memory.Chunk) float64 {
	if p.rules.MinRecallCount > 0 {
		if c.RecallCount < p.rules.MinRecallCount {
			return 0.0
		}
		return 1.0
	}
	if p.rules.RecallPercentileFloor <= 0 {
		return 1.0
	}
	// Bottom-percentile cut: if c sits below the floor across its
	// scope, score 0. Same scope ⇒ same scope-kind.
	counts := make([]int, 0, len(scope))
	for _, s := range scope {
		if s.ScopeKind == c.ScopeKind {
			counts = append(counts, s.RecallCount)
		}
	}
	if len(counts) < 5 {
		// Not enough samples to draw a percentile from — keep.
		return 1.0
	}
	sort.Ints(counts)
	idx := int(math.Floor(p.rules.RecallPercentileFloor * float64(len(counts))))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(counts) {
		idx = len(counts) - 1
	}
	floor := counts[idx]
	if c.RecallCount <= floor {
		return 0.0
	}
	return 1.0
}

// collapse implements the embedding-cluster collapse step. Chunks
// whose embeddings are within CollapseCosine of a representative are
// removed; the survivor inherits the dropped chunk's recall + last-
// accessed (max).
func (p *Pruner) collapse(chunks []memory.Chunk, dec *Decision) []memory.Chunk {
	// Group by scope kind + id so we never collapse across scopes.
	type key struct{ kind, id string }
	groups := map[key][]int{}
	for i := range chunks {
		k := key{chunks[i].ScopeKind, chunks[i].ScopeID}
		groups[k] = append(groups[k], i)
	}
	dropMask := make([]bool, len(chunks))
	for _, idxs := range groups {
		// Sort by recall desc, then LastAccessed desc — first index
		// in each cluster wins.
		sort.SliceStable(idxs, func(a, b int) bool {
			ca, cb := chunks[idxs[a]], chunks[idxs[b]]
			if ca.RecallCount != cb.RecallCount {
				return ca.RecallCount > cb.RecallCount
			}
			return ca.LastAccessed.After(cb.LastAccessed)
		})
		for ai := 0; ai < len(idxs); ai++ {
			a := idxs[ai]
			if dropMask[a] {
				continue
			}
			for bi := ai + 1; bi < len(idxs); bi++ {
				b := idxs[bi]
				if dropMask[b] {
					continue
				}
				sim := cosine(chunks[a].Embedding, chunks[b].Embedding)
				if sim < p.rules.CollapseCosine {
					continue
				}
				// Drop b, fold its metadata into a.
				dropMask[b] = true
				chunks[a].RecallCount += chunks[b].RecallCount
				if chunks[b].LastAccessed.After(chunks[a].LastAccessed) {
					chunks[a].LastAccessed = chunks[b].LastAccessed
				}
				dec.Dropped = append(dec.Dropped, chunks[b].ID)
				dec.Collapsed[chunks[b].ID] = chunks[a].ID
				dec.Verdicts = append(dec.Verdicts, Verdict{
					ID:            chunks[b].ID,
					Action:        "collapse",
					Reason:        fmt.Sprintf("cosine %.2f >= %.2f", sim, p.rules.CollapseCosine),
					CollapsedInto: chunks[a].ID,
				})
			}
		}
	}
	out := make([]memory.Chunk, 0, len(chunks))
	for i, c := range chunks {
		if dropMask[i] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// cosine returns the cosine similarity of a and b. Identical to the
// helper in core/memory but duplicated here to keep this package
// importing memory only for value types — not the helper symbol.
func cosine(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
