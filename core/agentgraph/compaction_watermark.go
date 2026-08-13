package agentgraph

import "sync"

// The kernel's automatic compaction sites (exec_compute.go pre_call and
// post_tool) used to be gated by a single boolean,
// a suppression flag on the Env which the chat surface set
// unconditionally on every run. That flag existed because
// compaction-convergence-01PMDL05 found a real double-fire — compact
// pre-send, then compact *again* at the first pre-call site against a
// transcript the pre-send pass had just produced.
//
// The side effect was the ceiling this mission
// (turn-context-runway-01PMAG03 WP02) removes: a chat turn compacted
// exactly once, before the turn, and then grew monotonically for up to
// 25 model turns and 200 tool calls until it overflowed.
//
// The double-fire the flag prevents is specifically a *staleness*
// condition, not a permanent one. CompactionWatermark encodes that
// reading: record the token count the run starts at, and let an
// automatic site fire only when the live count has grown past it by a
// margin. The first pre-call site is suppressed by construction —
// nothing has grown yet, because that site's own observation is what
// latches the baseline — while a site fifteen iterations later, after
// 200KB of tool output, is free to fire.
//
// LIFTED (agentgraph-total-convergence-01PMGX01 WP08). Both the
// suppression flag and the pre-send pass are now gone (spec §6 I4
// forbids either symbol from reappearing, so they are not named here):
// compaction runs as a `compact` node placed between history_read and
// the agent loop, and this policy is what gates the kernel's own
// automatic sites behind it. The pre-send double-fire the boolean
// prevented cannot recur, because there is no pre-send pass — but the
// watermark still earns its place, since the node and the pre_call site
// are two firing points on the same turn and the first pre-call visit
// must not re-compact what the node just compacted.
//
// CompactionWatermarkPolicy stayed a plain policy value throughout —
// threshold plus margin, no chat-runner or kernel internals — which is
// what made the lift a no-op. Do not grow it a dependency on any
// particular caller.

const (
	// DefaultCompactionWatermarkMarginTokens is the absolute growth a
	// run must show before an automatic site may fire. It is the floor
	// of the margin computation, so a run that starts from an almost
	// empty transcript still has to accumulate real volume before
	// paying for a compaction call.
	DefaultCompactionWatermarkMarginTokens = 8192

	// DefaultCompactionWatermarkMarginFraction scales the margin with
	// the run's starting size, so a large conversation is not compacted
	// for a proportionally trivial amount of growth.
	//
	// plan.md open question 1 ("what is the watermark margin?") wants a
	// measurement pass on real transcripts. These defaults are the
	// conservative starting point: with the WP01 tool-output cap in
	// front of it, a turn has to accumulate a genuinely large amount of
	// context before the first mid-run compaction.
	DefaultCompactionWatermarkMarginFraction = 0.5
)

// CompactionWatermarkPolicy is the trigger policy for automatic
// compaction: how far past its starting size a run's live context must
// grow before an automatic site is allowed to fire.
//
// The zero value selects the package defaults.
//
// SEMANTICS OF THE THREE VALUE CLASSES — worth reading before any
// Settings dial or autonomy knob is pointed at these fields, because a
// UI that treats them as ordinary numbers can produce a surprising
// configuration:
//
//	 0  = "unset", replaced by the package default.
//	>0  = that value.
//	<0  = that term is DISABLED and contributes nothing to the margin.
//
// Setting BOTH fields negative therefore disables both terms, leaving
// only Margin's floor of 1 — a watermark that fires on any growth at
// all beyond the baseline. That is a legitimate configuration for a
// test that wants a deterministic trigger, but it is almost certainly
// not what a user dragging a slider to its minimum intends. A dial
// should clamp to a positive minimum rather than pass a negative
// through.
type CompactionWatermarkPolicy struct {
	// MarginTokens is the absolute growth floor. Zero selects
	// DefaultCompactionWatermarkMarginTokens; negative disables this
	// term.
	MarginTokens int
	// MarginFraction is the growth expressed as a fraction of the
	// baseline. Zero selects DefaultCompactionWatermarkMarginFraction;
	// negative disables this term.
	MarginFraction float64
}

// resolved fills the zero fields with the package defaults.
//
// It must be idempotent — NewCompactionWatermark stores a resolved
// policy and Margin resolves again on every call, so a resolution that
// mapped a "disabled" term onto the zero value would silently resurrect
// the default on the second pass. Negative therefore means *disabled*
// all the way through, and only Margin collapses it to zero.
func (p CompactionWatermarkPolicy) resolved() CompactionWatermarkPolicy {
	if p.MarginTokens == 0 {
		p.MarginTokens = DefaultCompactionWatermarkMarginTokens
	}
	if p.MarginFraction == 0 {
		p.MarginFraction = DefaultCompactionWatermarkMarginFraction
	}
	return p
}

// Margin is the growth, in tokens, required over the given baseline.
// The absolute floor and the relative term compose by max(): the floor
// protects small baselines, the fraction protects large ones. A
// negative term is disabled and contributes nothing.
func (p CompactionWatermarkPolicy) Margin(baseline int) int {
	r := p.resolved()
	margin := r.MarginTokens
	if margin < 0 {
		margin = 0
	}
	if baseline > 0 && r.MarginFraction > 0 {
		if scaled := int(float64(baseline) * r.MarginFraction); scaled > margin {
			margin = scaled
		}
	}
	if margin < 1 {
		// A zero margin would let the very first site fire against the
		// baseline it just latched — exactly the double-fire this
		// policy exists to prevent. Never allow it.
		margin = 1
	}
	return margin
}

// Threshold is the live token count an automatic site must strictly
// exceed, given the run's baseline.
func (p CompactionWatermarkPolicy) Threshold(baseline int) int {
	return baseline + p.Margin(baseline)
}

// Exceeds reports whether a live token count has grown past the
// watermark set by baseline.
func (p CompactionWatermarkPolicy) Exceeds(baseline, live int) bool {
	return live > p.Threshold(baseline)
}

// CompactionWatermark is the run-scoped instance of the policy: the
// policy value plus the baseline it latched on its first observation.
//
// A nil *CompactionWatermark means "no watermark" — every automatic
// site is ungated, which is the pre-mission behaviour for
// graph-authored runs that have no pre-send compactor in front of them.
//
// Safe for concurrent use: tool_dispatch fans out across goroutines and
// a LoopNode body can reach multiple sites from different stacks.
type CompactionWatermark struct {
	policy CompactionWatermarkPolicy

	mu       sync.Mutex
	baseline int
	latched  bool
	crossed  bool
}

// NewCompactionWatermark arms a watermark with the given policy. The
// baseline is latched by the first Admit call rather than supplied
// here: the run's own first observation of its live context IS the
// baseline, which is what makes the first pre-call site a no-op by
// construction and keeps the policy free of any dependency on the
// pre-send compaction path.
func NewCompactionWatermark(p CompactionWatermarkPolicy) *CompactionWatermark {
	return &CompactionWatermark{policy: p.resolved()}
}

// Policy returns the resolved policy value.
func (w *CompactionWatermark) Policy() CompactionWatermarkPolicy {
	if w == nil {
		return CompactionWatermarkPolicy{}.resolved()
	}
	return w.policy
}

// Baseline reports the latched baseline and whether anything has been
// observed yet.
func (w *CompactionWatermark) Baseline() (int, bool) {
	if w == nil {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.baseline, w.latched
}

// Admit records liveTokens as the run's current context size and
// reports whether an automatic compaction site may fire.
//
// The first call latches the baseline and always returns false — there
// has been no growth to compact, and compacting here is precisely the
// double-fire compaction-convergence-01PMDL05 was written to prevent.
// Every later call compares against the latched baseline plus the
// policy margin.
//
// A nil receiver returns true: no watermark means no gate.
func (w *CompactionWatermark) Admit(liveTokens int) bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.latched {
		w.baseline = liveTokens
		w.latched = true
		return false
	}
	if w.policy.Exceeds(w.baseline, liveTokens) {
		w.crossed = true
		return true
	}
	return false
}

// Crossed reports whether this run has grown past the watermark.
//
// Sites that cannot measure the whole conversation consult this instead
// of re-observing. The post_tool site is the case in point: it sees a
// single tool result, not the live transcript, so feeding its byte count
// to Admit would corrupt the baseline. Instead it rides the pre_call
// site's verdict — the automatic sites are live for this run once the
// conversation has actually grown past the watermark.
//
// STICKY, NOT LATEST-OBSERVATION. Once an Admit call has returned true,
// Crossed stays true until Rearm() clears it; a subsequent Admit that
// falls back under the threshold does NOT un-cross it. That is
// deliberate and matters in one real case: the pre_call site sets
// crossed, then the compaction pipeline SKIPS — because the resolved
// SiteConfig disabled it (the "off" dial tier), or because the strategy
// found nothing worth trimming. No Rearm happens, because nothing was
// compacted, and the post_tool site correctly still sees a run that has
// outgrown its baseline. A latest-observation reading would flicker the
// post_tool gate on and off with each model call's message count.
//
// A nil receiver returns true: no watermark means no gate.
func (w *CompactionWatermark) Crossed() bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.crossed
}

// Rearm resets the latch so the next Admit re-baselines. Called after a
// compaction actually fires: the transcript the run continues from is a
// new starting point, and the next site must clear the margin again
// from *there* rather than from the pre-compaction baseline. Without
// this a single long turn would compact on every subsequent site.
func (w *CompactionWatermark) Rearm() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.latched = false
	w.baseline = 0
	w.crossed = false
}

// ---- Env gates ----
//
// Both automatic compaction sites route through these two helpers so
// there is exactly one place that decides whether the kernel may
// compact.

// admitAutomaticCompaction is the pre_call gate: the site measures the
// whole prepared message slice, so its observation is the run's live
// context size.
func (env *Env) admitAutomaticCompaction(liveTokens int) bool {
	if env == nil || env.Compactor == nil {
		return false
	}
	return env.AutoCompaction.Admit(liveTokens)
}

// automaticCompactionCrossed is the post_tool gate: the site sees only
// the tool result, so it rides the watermark's existing verdict rather
// than re-observing (see CompactionWatermark.Crossed).
func (env *Env) automaticCompactionCrossed() bool {
	if env == nil || env.Compactor == nil {
		return false
	}
	return env.AutoCompaction.Crossed()
}
