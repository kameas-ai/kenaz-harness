package chat

// advice_hook.go — laya-advisors-01LAYA001 WP07: the turn-path threading
// point for the advisor seam (core/advice).
//
// # Threading point: end-of-turn, post-persist, never on the turn's
// critical path
//
// core/advice/doc.go's contract: "Recommend takes already-extracted
// features ... Unlike the rater nothing WAITS on it" and spec §2: "the
// Advisor's 800ms budget runs concurrent with or after turn completion."
// StartStream registers fireAdvice as an ADDITIONAL callback on the
// SAME HookPostLLM boundary UsageHook/PostSendHook already use (see the
// registration block below, mirrored from those two) — which
// core/agentgraph/hooks.go's FirePostHooks documents as firing "AFTER
// session_write has persisted the assistant message." That is already
// AFTER the user has seen the full streamed answer (StreamBridge closes
// the stream independently of when the history write lands), so there
// is no turn-completion-latency budget to protect in the first place —
// the assistant's answer is already on screen. fireAdvice's own body
// additionally spawns its own goroutine with a context.Background()-
// derived, 800ms-timeout context (the v0.78.2 lesson: never inherit a
// ctx whose cancellation is tied to the request/response lifecycle for
// work that must outlive it) BEFORE doing anything else, so even
// FirePostHooks's synchronous dispatch loop (core/agentgraph/hooks.go:
// "Callbacks are run synchronously on the kernel's execution goroutine;
// long-running listeners must dispatch their own goroutine") returns
// immediately. Two independent reasons the turn is never delayed:
// hook fires after the user-visible content already streamed, AND the
// hook itself does zero synchronous work.
import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	advicebranchnow "github.com/kameas-ai/kenaz-harness/core/advice/kinds/branchnow"
	advicecompactnow "github.com/kameas-ai/kenaz-harness/core/advice/kinds/compactnow"
	adviceescalatemodel "github.com/kameas-ai/kenaz-harness/core/advice/kinds/escalatemodel"
	adviceLabels "github.com/kameas-ai/kenaz-harness/core/advice/labels"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/event/modelswitch"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// AdviceRecommendBudget is the hard per-kind timeout fireAdvice applies
// to every Recommend call, per core/advice/doc.go's "hard timeout
// (default 800ms)" contract restated at the call site that actually
// owns the ctx lifetime.
const AdviceRecommendBudget = 800 * time.Millisecond

// AdviceDeps bundles the session-state accessors and the auto-act
// executor fireAdvice needs beyond what a HookPostLLM callback already
// receives (sessionID, messageID, assistant text). Every field is
// independently nil-safe — a partially-wired AdviceDeps degrades the
// kinds it cannot fully serve to "no advice" rather than panicking,
// matching every Advisor failure mode in core/advice's own contract.
type AdviceDeps struct {
	// BranchCount returns how many branches already exist off
	// sessionID (branch_now's PriorBranchCount feature) — production
	// wiring is coreconv.Manager.ListByParent's length
	// (core/rpc/api.go). nil defaults every call's PriorBranchCount to 0.
	BranchCount func(ctx context.Context, sessionID string) (int, error)
	// AutoActBranchNow performs branch_now's Autonomous-tier auto-act
	// (spec §3): creates the child session and returns its id.
	// Production wiring is coreconv.Manager.CreateBranch with
	// CreationPath="auto_act" (core/rpc/api.go). nil disables auto-act
	// for branch_now regardless of tier — the chip still renders.
	AutoActBranchNow func(ctx context.Context, sessionID string) (childSessionID string, err error)
	// AutoActAudit receives one contextaudit.Event per branch_now
	// auto-act (spec §3: "banner + audit event"). nil silences the
	// audit trail without affecting the auto-act itself.
	AutoActAudit contextaudit.Emitter
	// ModelSwitchAudit receives one contextaudit.Event per detected
	// model switch (see checkModelSwitch below). Reuses the SAME
	// contextaudit.Emitter the confirm-each / auto-title hooks already
	// share in core/rpc/api.go (confirmAudit) — one audit sink, many
	// producers.
	ModelSwitchAudit contextaudit.Emitter
	// OnShown is called every time a recommendation clears spec §2d's
	// chip gate (advice.ShouldShowChip) — i.e. every time
	// publishAdviceChip actually fires — with the exact (sessionID,
	// kind.ID, features) triple that produced it. Production wiring
	// (core/rpc/api.go) uses this to populate a small in-memory
	// "what did the user just see" registry so the Advice_Respond RPC
	// method can reconstruct the SAME Features value a later
	// accept/dismiss click needs (Advisor.Dismiss and
	// labels.RecordActionIfSupported both key on features, not just
	// kind id — see cache.go's cacheKey). nil is a safe no-op: the chip
	// still renders, only accept/dismiss stops working (a caller with no
	// RPC surface for advice yet, e.g. a bare test chassis).
	OnShown func(sessionID, kindID string, features advice.Features)
}

// sessionAdviceState is the per-session, in-memory (process-lifetime,
// not persisted) bookkeeping fireAdvice/checkModelSwitch need across
// calls. Reset on process restart — an accepted limitation documented
// here rather than silently assumed: "turns since session start" and
// "turns since last branch" are conversation-scoped concepts that would
// ideally survive a restart, but no durable per-session turn cursor
// exists yet (see branchnow.Snapshot's own doc comment: "a future WP07
// caller populates this from real session/event-log state" — this WP is
// that caller, using the cheapest available real signal for each field
// rather than blocking on a new persistence layer).
type sessionAdviceState struct {
	turnsSinceStart      int
	turnsSinceLastBranch int
	lastBranchCount      int
	sawLastBranchCount   bool
	lastProfileID        string
	lastModelID          string
	hasLastModel         bool
}

func (r *ChatRunner) adviceState(sessionID string) *sessionAdviceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adviceStates == nil {
		r.adviceStates = map[string]*sessionAdviceState{}
	}
	st, ok := r.adviceStates[sessionID]
	if !ok {
		st = &sessionAdviceState{}
		r.adviceStates[sessionID] = st
	}
	return st
}

// checkModelSwitch is laya-advisors-01LAYA001 WP08's missing behavioral-
// label source (design §5.1/§5.2): "new model.switched audit + event-log
// kinds at the 01PMCH01 move-switch site." Called synchronously near the
// top of StartStream (cheap: one map read/write, an audit emit only on
// an actual change) — NOT from fireAdvice's goroutine, because this is
// itself the record of the turn's OWN model resolution, not an advisory
// judgment about it, and must be attributed to the turn it belongs to
// even if fireAdvice's later evaluation is skipped (Advisor nil, budget
// exceeded, etc).
//
// Disposition (spec's open call, per this WP's brief: "record it purely
// as audit+event for later extraction — follow design §5's behavioral-
// label pipeline reading; document your choice"): this WP records
// model.switched as audit+event ONLY, not as an advice_labels row.
// escalate_model's Recommend calls are a DIFFERENT judgment (is the
// model struggling right now) than "did a switch just happen" — folding
// the latter into the former's label stream would conflate a user
// action with a model-quality judgment under one kind's schema. A
// dedicated behavioral-label consumer that mines model.switched events
// (design §5.1's "Unprompted positives" column) is design's own
// deferred work, not this WP's.
func (r *ChatRunner) checkModelSwitch(ctx context.Context, sessionID, profileID, modelOverride string) {
	if r == nil || sessionID == "" {
		return
	}
	deps := r.cfg.AdviceDeps
	st := r.adviceState(sessionID)

	r.mu.Lock()
	hadPrior := st.hasLastModel
	fromProfile, fromModel := st.lastProfileID, st.lastModelID
	changed := hadPrior && (fromProfile != profileID || fromModel != modelOverride)
	st.lastProfileID, st.lastModelID, st.hasLastModel = profileID, modelOverride, true
	r.mu.Unlock()

	if !changed || deps == nil || deps.ModelSwitchAudit == nil {
		return
	}
	_, ev, err := modelswitch.NewSwitchedEvent(sessionID, fromProfile, fromModel, profileID, modelOverride, time.Now().UTC())
	if err != nil {
		return
	}
	if err := deps.ModelSwitchAudit.Emit(ctx, ev); err != nil {
		logging.L().Warn("chat.advice.model_switch_audit_failed", "session_id", sessionID, "err", err.Error())
	}
}

// fireAdvice is laya-advisors-01LAYA001 WP07's per-turn evaluation
// point. Spawned as its own goroutine by StartStream's HookPostLLM
// registration (see chat_runner.go) — see this file's header comment for
// why that never delays a turn. tier is the turn's already-resolved
// autonomy.ResolvedKnobs.EffectiveTier, captured by value before the
// goroutine starts (resolvedKnobs is StartStream-local and must not be
// read from a goroutine racing the next call).
func (r *ChatRunner) fireAdvice(sessionID, profileID, modelOverride, userMessage string, tier autonomy.Tier) {
	if r == nil || r.cfg.Advisor == nil || userMessage == "" {
		return
	}
	advisor := r.cfg.Advisor
	deps := r.cfg.AdviceDeps
	broker := r.cfg.Broker

	ctx, cancel := context.WithTimeout(context.Background(), AdviceRecommendBudget)
	defer cancel()

	st := r.adviceState(sessionID)
	r.mu.Lock()
	st.turnsSinceStart++
	turnsSinceStart := st.turnsSinceStart
	r.mu.Unlock()

	priorBranchCount := 0
	if deps != nil && deps.BranchCount != nil {
		if n, err := deps.BranchCount(ctx, sessionID); err == nil {
			priorBranchCount = n
		}
	}
	r.mu.Lock()
	if !st.sawLastBranchCount || priorBranchCount > st.lastBranchCount {
		st.turnsSinceLastBranch = 0
	} else {
		st.turnsSinceLastBranch++
	}
	st.lastBranchCount = priorBranchCount
	st.sawLastBranchCount = true
	turnsSinceLastBranch := st.turnsSinceLastBranch
	r.mu.Unlock()

	sess := advice.SessionContext{SessionID: sessionID}

	// branch_now — the only reversible kind (auto-act eligible).
	r.evaluateBranchNow(ctx, advisor, sess, tier, advicebranchnow.Snapshot{
		LastUserMessage:        userMessage,
		TurnsSinceSessionStart: turnsSinceStart,
		TurnsSinceLastBranch:   turnsSinceLastBranch,
		PriorBranchCount:       priorBranchCount,
		// EditResendPrecursor/ToolCallDensityWindow: no wired source yet
		// (edit-resend detection lives at the compose-box layer, which
		// this backend hook has no visibility into; a trailing tool-call
		// density accessor does not exist). `justify(blocker: "frontend
		// edit-resend signal is not threaded to StartStream; no per-turn
		// tool-call-rate accessor exists", owner: alec, date:
		// 2026-09-29)`. Both ride along as zero-value features — they
		// affect confidence, never make Heuristic itself misfire, since
		// branchnow.Heuristic (kinds/branchnow/branchnow.go) deliberately
		// does not consult them (see that function's own doc comment).
	}, broker)

	// compact_now / escalate_model — suggest-only, never auto-act.
	r.evaluateCompactNow(ctx, advisor, sess, advicecompactnow.Snapshot{
		// No live token-accounting accessor is threaded into this hook
		// yet (the compaction engine's own span accounting lives inside
		// core/agentgraph/compaction, not exposed to the chat runner
		// package). `justify(blocker: "no ChatRunner-visible accessor
		// for live context-fill/token-span accounting", owner: alec,
		// date: 2026-09-29)`. Zero-value features make
		// compactnow.Heuristic answer "no, don't compact" at 0
		// confidence — safe (never a false compact-now nudge) but never
		// yet a USEFUL one either; captured as a shown=false label row
		// regardless, so the corpus starts accumulating real heuristic
		// outputs (all "no, 0%") the moment real accounting is wired.
	}, broker)

	r.evaluateEscalateModel(ctx, advisor, sess, adviceescalatemodel.Snapshot{
		// ConsecutiveToolFailures/RetriesInWindow/DoomLoopRepeatCount/
		// CurrentRung/ErrorKindCounts/BudgetRemainingFraction: no
		// ChatRunner-visible source exists yet. The kernel's tool-dispatch
		// executor tracks failure/doom-loop state internally
		// (exec_dispatch.go's toolCallHistory) but does not export it, and
		// FirePostToolHooks's ToolPostHookCallback signature
		// (core/agentgraph/hooks.go) carries only the flattened
		// (toolName, toolArgs, toolResult string, duration) tuple — no
		// structured success/failure flag a listener could tally without
		// guessing at string content. `justify(blocker: "ToolPostHookCallback
		// needs a structured outcome field (or the dispatcher needs to
		// export its failure-streak counter) before escalate_model's
		// heuristic can see real tool-failure data", owner: alec, date:
		// 2026-09-29)`. Zero-value features make escalatemodel.Heuristic
		// answer "no, don't escalate" at 0 confidence — safe (never a
		// false escalation nudge) — captured as a shown=false label row
		// regardless, same reasoning as compact_now above.
	}, broker)
}

func (r *ChatRunner) evaluateBranchNow(ctx context.Context, advisor advice.Advisor, sess advice.SessionContext, tier autonomy.Tier, snap advicebranchnow.Snapshot, broker Broker) {
	kind, ok := advice.Get(advicebranchnow.KindID)
	if !ok {
		return
	}
	features, err := advicebranchnow.Extract(snap)
	if err != nil {
		return
	}
	rec, err := advisor.Recommend(ctx, kind, features, sess)
	if err != nil {
		return
	}

	// Autonomy-tiered auto-act (spec §3): ONLY at Autonomous tier, ONLY
	// for a SafetyReversible kind, enforced via RequireCanAutoAct — the
	// compile-visible, gate-and-CI-checked switch spec's #78 lesson
	// requires. A suggest-only kind (compact_now/escalate_model) never
	// reaches this branch at all — evaluateCompactNow/evaluateEscalateModel
	// below never call RequireCanAutoAct or an auto-act executor.
	//
	// The literal "branch_now" (not advicebranchnow.KindID) is
	// DELIBERATE, not a style slip: scripts/ci/cmd/checkadvicekinds's
	// check #3 statically greps `RequireCanAutoAct("<id>")` for a
	// resolvable STRING LITERAL — the same reason every kind's own
	// init() (branchnow.go et al.) registers with a literal ID instead
	// of its own KindID constant. A symbolic reference here would
	// compile identically but make this call site invisible to the gate
	// (verified: it was, before this comment/fix — the gate reported "0
	// call site(s) checked" against the symbolic form). TestRegistration
	// (branchnow_test.go) already pins that this literal and
	// advicebranchnow.KindID never drift apart.
	if rec.Decision && tier == autonomy.TierAutonomous {
		if gateErr := advice.RequireCanAutoAct("branch_now"); gateErr == nil {
			r.autoActBranchNow(ctx, sess, kind, features, rec)
			return
		}
	}

	if !advice.ShouldShowChip(rec) {
		return
	}
	r.noteShown(sess.SessionID, kind, features)
	publishAdviceChip(broker, sess.SessionID, kind, rec, branchNowChipCopy)
}

func (r *ChatRunner) evaluateCompactNow(ctx context.Context, advisor advice.Advisor, sess advice.SessionContext, snap advicecompactnow.Snapshot, broker Broker) {
	kind, ok := advice.Get(advicecompactnow.KindID)
	if !ok {
		return
	}
	features, err := advicecompactnow.Extract(snap)
	if err != nil {
		return
	}
	rec, err := advisor.Recommend(ctx, kind, features, sess)
	if err != nil {
		return
	}
	if !advice.ShouldShowChip(rec) {
		return
	}
	r.noteShown(sess.SessionID, kind, features)
	publishAdviceChip(broker, sess.SessionID, kind, rec, compactNowChipCopy)
}

func (r *ChatRunner) evaluateEscalateModel(ctx context.Context, advisor advice.Advisor, sess advice.SessionContext, snap adviceescalatemodel.Snapshot, broker Broker) {
	kind, ok := advice.Get(adviceescalatemodel.KindID)
	if !ok {
		return
	}
	features, err := adviceescalatemodel.Extract(snap)
	if err != nil {
		return
	}
	rec, err := advisor.Recommend(ctx, kind, features, sess)
	if err != nil {
		return
	}
	if !advice.ShouldShowChip(rec) {
		return
	}
	r.noteShown(sess.SessionID, kind, features)
	publishAdviceChip(broker, sess.SessionID, kind, rec, escalateModelChipCopy)
}

// noteShown forwards to AdviceDeps.OnShown when wired — see that field's
// doc comment for why the RPC layer needs this triple.
func (r *ChatRunner) noteShown(sessionID string, kind advice.AdviceKind, features advice.Features) {
	if deps := r.cfg.AdviceDeps; deps != nil && deps.OnShown != nil {
		deps.OnShown(sessionID, kind.ID, features)
	}
}

func (r *ChatRunner) autoActBranchNow(ctx context.Context, sess advice.SessionContext, kind advice.AdviceKind, features advice.Features, rec advice.Recommendation) {
	deps := r.cfg.AdviceDeps
	if deps == nil || deps.AutoActBranchNow == nil {
		// No executor wired: fall back to the passive chip rather than
		// silently doing nothing (spec §3's promise is "auto-act AND
		// notify," not "auto-act or nothing") — a chip still gives the
		// user a way to act on a reversible recommendation.
		if advice.ShouldShowChip(rec) {
			r.noteShown(sess.SessionID, kind, features)
			publishAdviceChip(r.cfg.Broker, sess.SessionID, kind, rec, branchNowChipCopy)
		}
		return
	}
	childID, err := deps.AutoActBranchNow(ctx, sess.SessionID)
	if err != nil {
		logging.L().Warn("chat.advice.auto_act_failed", "kind", kind.ID, "session_id", sess.SessionID, "err", err.Error())
		return
	}
	adviceLabels.RecordActionIfSupported(ctx, r.cfg.Advisor, sess, kind, features, adviceLabels.ActionAutoActed)
	if deps.AutoActAudit != nil {
		if err := contextaudit.Emit(ctx, deps.AutoActAudit, contextaudit.KindAdviceAutoActed, contextaudit.AdviceAutoActedPayload{
			SessionID:      sess.SessionID,
			KindID:         kind.ID,
			ChildSessionID: childID,
			ModelID:        rec.Model,
			Confidence:     rec.Confidence,
		}, time.Now().UTC()); err != nil {
			logging.L().Warn("chat.advice.auto_act_audit_failed", "session_id", sess.SessionID, "err", err.Error())
		}
	}
	if r.cfg.Broker != nil {
		r.cfg.Broker.Emit(TopicAdviceAutoActed, AdviceAutoActedPayload{
			SessionID:      sess.SessionID,
			KindID:         kind.ID,
			ChildSessionID: childID,
			Confidence:     rec.Confidence,
			Model:          rec.Model,
		})
	}
}

// TopicAdviceRecommendation is the broker topic WP07's chip rides on —
// laya-advisors-01LAYA001's own topic, not an existing chat topic
// (spec: "verify served-mode topic DELIVERY, not just allowlisting" —
// see core/serve/wsstream.go's passthroughTopics entry + the
// wsstream_advice_topic_test.go delivery test).
const TopicAdviceRecommendation = "advice:recommendation"

// TopicAdviceAutoActed is the banner-facing broker topic fired
// alongside KindBranchAdvisorAccepted's audit event when branch_now
// auto-acts at the Autonomous tier (spec §3: "auto-act AND notify").
const TopicAdviceAutoActed = "advice:auto-acted"

// AdviceChipPayload is TopicAdviceRecommendation's wire shape. Mirrors
// frontend/src/lib/types.ts's AdviceChipPayload — SessionID is required
// for served-mode delivery (core/serve/wsstream.go's D-705 fail-closed
// session filter needs it on every forwarded topic).
type AdviceChipPayload struct {
	SessionID     string `json:"session_id"`
	KindID        string `json:"kind_id"`
	Decision      bool   `json:"decision"`
	Confidence    int    `json:"confidence"`
	Model         string `json:"model"`
	Rung          string `json:"rung"`
	Title         string `json:"title"`
	Body          string `json:"body"`
	PromptVersion string `json:"prompt_version"`
}

// AdviceAutoActedPayload is TopicAdviceAutoActed's wire shape.
type AdviceAutoActedPayload struct {
	SessionID      string `json:"session_id"`
	KindID         string `json:"kind_id"`
	ChildSessionID string `json:"child_session_id"`
	Confidence     int    `json:"confidence"`
	Model          string `json:"model"`
}

// chipCopy is one kind's static, non-templated chip text (spec §2's
// kinds table names the QUESTION each kind answers; the chip's copy is
// the user-facing restatement of a "yes" answer). Kept as package-level
// constants rather than computed per-call — none of the three v1 kinds'
// copy depends on their Features (compact_now/escalate_model render no
// per-call detail; branch_now's chip intentionally does not quote the
// user's own message back at them, matching the heuristic's own
// no-raw-transcript discipline).
type chipCopy struct{ title, body string }

var (
	branchNowChipCopy     = chipCopy{title: "Branch this off?", body: "This looks like a separable thread — branch it into its own session."}
	compactNowChipCopy    = chipCopy{title: "Compact now?", body: "Compacting now may preserve more task-relevant context than waiting."}
	escalateModelChipCopy = chipCopy{title: "Try a stronger model?", body: "The current model looks like it's struggling with this task."}
)

func publishAdviceChip(broker Broker, sessionID string, kind advice.AdviceKind, rec advice.Recommendation, copy chipCopy) {
	if broker == nil {
		return
	}
	broker.Emit(TopicAdviceRecommendation, AdviceChipPayload{
		SessionID:     sessionID,
		KindID:        kind.ID,
		Decision:      rec.Decision,
		Confidence:    rec.Confidence,
		Model:         rec.Model,
		Rung:          string(rec.Rung),
		Title:         copy.title,
		Body:          copy.body,
		PromptVersion: rec.PromptVersion,
	})
}
