// The real scheduler.ChatRunDispatcher (mission model-scheduled-jobs-
// 01PMSJ01, WP04). Lives on the chassis side of the seam
// (core/scheduler.ChatRunDispatcher) because it needs the sessions
// manager, the LLM view and the in-process event bus; core/scheduler must
// not import core/rpc.
//
// Design (spec.md §5.2): reload the row, render the prompt, resolve the
// schedule's ONE conversation (create it on the first fire, continue it on
// every later one — sessions/0348), append the rendered prompt as a new
// user turn, start the stream
// through the SAME production surface the interactive chat path uses
// (llmview.API.StartStream, not a second chat stack), and await the
// terminal "llm:stream-closed" event on the in-process EventBus —
// topic-filtered so the per-token "llm:stream-chunk" volume cannot fill
// the subscriber channel and drop the terminal event (spec.md §2, "How
// the dispatcher awaits completion").
//
// ARMED as of WP05: every DispatchChatRun call marks its context
// runposture.Unattended before calling StartStream (spec.md §5.4, FR-003)
// — a scheduled run is unattended by construction, with no exceptions.
// That posture is what makes it safe for core/rpc/api.go to assign this
// dispatcher into scheduledchatview.Config.Dispatcher and into the WP03
// cron engine (plan.md Rule 2: arming without the posture can park a
// scheduled run on a human forever — spec.md §2 H-1/H-2/H-3).
package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// defaultDispatchTimeout bounds how long DispatchChatRun waits for the
// terminal stream-closed event after StartStream returns a subscription
// id. Generous — a scheduled run may involve several tool round trips —
// but finite: a run that never closes must not hang the cron goroutine
// (or a RunNow caller) forever.
const defaultDispatchTimeout = 10 * time.Minute

// ChatRunDispatcherDeps bundles the production seams LiveChatRunDispatcher
// needs. All fields are required in production; nil Store or nil Bus make
// DispatchChatRun return an error without touching either seam further.
type ChatRunDispatcherDeps struct {
	// Store re-reads the scheduled_chat_runs row at fire time so inline
	// edits (model, sink, template) take effect without re-registering
	// the cron schedule (spec.md §5.2 step 1).
	Store scheduler.ScheduledChatStore
	// Sessions creates the schedule's conversation on its first fire and
	// appends each fire's rendered prompt to it as a new user turn.
	Sessions sessionsview.SessionsAPI
	// ActiveRuns reports whether a turn is still executing in a session —
	// the chat runner (*chat.ChatRunner). A fire whose conversation has a
	// live turn (the previous run outliving its dispatch timeout, or one
	// the user started) is skipped and recorded as such rather than run
	// as a second, concurrent turn in one session. nil only detects
	// overlap within this dispatcher (one dispatch per schedule at a
	// time); production wires the runner (api.go).
	ActiveRuns llmview.ActiveRunChecker
	// PausedTurns reports whether a turn in a session is paused awaiting
	// provider credentials (the chat runner's key-rotation pause). Such a
	// turn is not "active" — it has no running sub — yet RedriveLastTurn
	// can re-run it in the session at any time, so a fire in that
	// conversation is skipped too. nil disables the check; production
	// wires the runner (api.go).
	PausedTurns PausedTurnChecker
	// LLM is the same production LLM view the interactive chat surface
	// calls — StartStream is dispatched through the ONE chat stack that
	// exists, not a second one built for scheduling (spec.md §1.3).
	LLM llmview.LLMConnectorAPI
	// Bus is the in-process EventBus StartStream's terminal event lands
	// on. Subscribed topic-filtered to "llm:stream-closed" only — see
	// the package doc.
	Bus *EventBus
	// DefaultProfile resolves "the active default profile" (spec.md §8
	// D-1). Lazy (called once per dispatch, not cached) so a profile
	// added after boot is picked up without a restart — the same shape
	// as core/rpc/api.go's wfDeps.DefaultProfileFunc for workflow
	// model_turn steps. Returns "" when no profile is configured.
	DefaultProfile func() string
	// DefaultModel resolves the model a run with no model override is
	// dispatched with on the given profile. It is recorded on the history
	// row so the run list can say which model ran. nil, or "" back,
	// records the model as unknown.
	DefaultModel func(profileID string) string
	// Origins records the (sessionID -> chat-run id) mapping for the
	// duration of this dispatch (model-scheduled-jobs-01PMSJ01 WP06),
	// so a filesystem-permission denial mid-run can attribute its
	// blocked_permission_requests row to the scheduled run that hit it —
	// see ScheduledRunOriginRegistry's doc for why a session-keyed side
	// channel, not ctx threading, is the seam. nil is allowed: the denial
	// still records with origin="interactive", the fail-safe default
	// ScheduledRunOriginRegistry.Resolve documents for an
	// unrecognised session.
	Origins *ScheduledRunOriginRegistry
	// Broker delivers the "banner" output sink (model-scheduled-jobs-
	// 01PMSJ01 WP07, FR-007) by publishing TopicScheduledChatBanner once
	// the run's terminal outcome is known. nil skips delivery (the run
	// still completes and its history row is still written either way —
	// banner is a notification, not part of the run's own success/
	// failure). *StreamBroker satisfies this trivially.
	Broker BannerPublisher
	// Containment binds each fired run's session to the tool allowlist
	// scheduler.ResolveRunContainment computes from the gate-time spec and
	// the re-read row (model-harness-toolset-01MHTS001 WP02, finding H-1).
	// The merged tool-permission resolver's session arm reads it per call.
	// nil is fail-closed: a run that needs containment refuses to start
	// rather than run unrestricted; an uncontained (user, no allowlist)
	// run is unaffected.
	Containment *ScheduledRunContainmentRegistry
	// Compactor enforces the schedule's context budget on its reused
	// conversation (scheduled-chat-context-budget): before a fire appends
	// its prompt, a conversation over context_budget_tokens keeps its
	// newest keep_recent_runs runs verbatim and folds the rest into one
	// rolling summary; a fire that still ends "session full" compacts
	// and retries once. Production wires chat.ScheduledBudgetCompactor
	// over the chat stack's compaction deps (api.go). nil (compaction
	// unavailable: HARNESS_COMPACTION=off, no engine) leaves every fire
	// replaying the whole conversation, as before the budget existed.
	Compactor ScheduledConversationCompactor
	// Timeout overrides defaultDispatchTimeout. Zero uses the default.
	Timeout time.Duration
}

// ScheduledConversationCompactor is the seam onto the per-schedule
// context budget. *chat.ScheduledBudgetCompactor implements it.
type ScheduledConversationCompactor interface {
	CompactForSchedule(ctx context.Context, sessionID, profileID, modelOverride string, b chat.ScheduledBudget) (chat.ScheduledBudgetOutcome, error)
}

// promptHistoryDepth bounds how many history rows a fire reads to find
// its schedule's run boundaries (prompt_message_id). Only runs still in
// the active conversation matter — at most keep_recent_runs plus the
// runs since the last compaction — so this is far above what is used.
const promptHistoryDepth = 200

// PausedTurnChecker is the seam onto the chat runner's paused-for-key-
// rotation turns. *chat.ChatRunner implements it.
type PausedTurnChecker interface {
	HasPausedTurnIn(sessionID string) bool
	// SubStillLive reports whether a sub is executing or paused (and so
	// redrivable). The containment watcher does not give up on a run
	// while one of its own subs is.
	SubStillLive(subID string) bool
}

// BannerPublisher is the minimal seam LiveChatRunDispatcher needs to
// deliver the "banner" output sink. *StreamBroker satisfies this
// trivially; the interface exists so tests can inject a recording stub
// without constructing a full broker.
type BannerPublisher interface {
	Publish(topic string, payload any)
}

// LiveChatRunDispatcher is the production scheduler.ChatRunDispatcher.
type LiveChatRunDispatcher struct {
	deps ChatRunDispatcherDeps

	// inflight holds the schedule ids with a dispatch in progress, so a
	// RunNow racing a cron fire of the same schedule (or two overlapping
	// fires) cannot both pass the active-turn check before either has
	// started its stream.
	mu       sync.Mutex
	inflight map[string]bool
}

// skipReasonInFlight / skipReasonActiveTurn are the history Error text of
// a skipped fire (Status scheduler.ChatRunStatusSkipped).
const (
	skipReasonInFlight   = "skipped: this schedule's previous run is still in progress"
	skipReasonActiveTurn = "skipped: a turn is still running in this schedule's conversation (the previous run, or one started there by hand), so this fire did not start another"
	skipReasonPausedTurn = "skipped: a turn in this schedule's conversation is paused awaiting provider credentials; it resumes once the key is rotated, so this fire did not start another"
	// skipReasonBusyAfterCompaction: a turn started in the conversation
	// while this fire was summarising older runs.
	skipReasonBusyAfterCompaction = "skipped: a turn started in this schedule's conversation while older runs were being summarised, so this fire did not start another"
	// failReasonSessionFull is the history Error of a fire that ran out
	// of context window even after the budget compacted and retried.
	failReasonSessionFull = "the conversation no longer fits the model's context window, even after summarising older runs and retrying once; this fire was not answered. Lower the schedule's context budget or keep fewer runs verbatim, or delete the conversation to start a new one"
	// failReasonSessionFullNoRetry: the same, when the retry never
	// started — nothing older was left to summarise, or summarising
	// failed (the cause is appended).
	failReasonSessionFullNoRetry = "the conversation no longer fits the model's context window and no older runs could be summarised to make room, so this fire was not answered. Lower the schedule's context budget or keep fewer runs verbatim, or delete the conversation to start a new one"
)

// claim marks id in flight and reports whether it was free.
func (d *LiveChatRunDispatcher) claim(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inflight[id] {
		return false
	}
	if d.inflight == nil {
		d.inflight = map[string]bool{}
	}
	d.inflight[id] = true
	return true
}

func (d *LiveChatRunDispatcher) unclaim(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inflight, id)
}

// NewChatRunDispatcher constructs a LiveChatRunDispatcher over deps.
func NewChatRunDispatcher(deps ChatRunDispatcherDeps) *LiveChatRunDispatcher {
	if deps.Timeout <= 0 {
		deps.Timeout = defaultDispatchTimeout
	}
	return &LiveChatRunDispatcher{deps: deps}
}

// DispatchChatRun implements scheduler.ChatRunDispatcher.
//
// Every internal failure (session creation, StartStream, an errored or
// timed-out terminal event) is reported as a ChatRunHistoryRecord with
// Status "failed" and a non-nil-string Error, with a NIL error return —
// the caller (RunNow or the cron engine) persists that record via
// AppendHistory either way. A non-nil error return is reserved for
// construction-level defects (a malformed job, missing seams) that leave
// nothing meaningful to record — see the doc on each early return below.
func (d *LiveChatRunDispatcher) DispatchChatRun(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error) {
	if job.ChatRun == nil {
		// Programmer error, not a runtime condition a schedule can hit —
		// only WP03's engine and RunNow construct chat_run jobs, and both
		// always set ChatRun. Nothing to record against.
		return scheduler.ChatRunHistoryRecord{}, errors.New("scheduler: DispatchChatRun: job.ChatRun is nil")
	}
	if d.deps.Store == nil {
		return scheduler.ChatRunHistoryRecord{}, errors.New("scheduler: DispatchChatRun: no store wired")
	}
	if d.deps.Bus == nil {
		return scheduler.ChatRunHistoryRecord{}, errors.New("scheduler: DispatchChatRun: no event bus wired")
	}

	// WP05, spec.md §5.4 / FR-003: a scheduled run is unattended BY
	// CONSTRUCTION — there is no "attended scheduled run" concept, so
	// this is marked unconditionally, not behind a config flag. Every
	// gate downstream that would otherwise park on a human (confirm_each
	// rung 5, cedar.Registry.RequestInteractive) reads this off ctx.
	ctx = runposture.Unattended(ctx)

	log := logging.L()
	id := job.ChatRun.ID

	// Step 1 (spec.md §5.2): reload the row. ChatRunSpec.ID's whole
	// documented purpose is that inline edits take effect without
	// re-registering the cron entry.
	rec, err := d.deps.Store.Get(ctx, id)
	if err != nil {
		return failedRecord(now, fmt.Sprintf("reload scheduled_chat_runs row: %v", err)), nil
	}

	// Step 2: render the prompt template. Rebuild Trigger from the
	// freshest row so {{cron_expr}} reflects the current schedule too.
	freshJob := scheduler.Job{
		ID:      rec.ID,
		Kind:    scheduler.JobKindChatRun,
		ChatRun: &scheduler.ChatRunSpec{ID: rec.ID, PromptTemplate: rec.PromptTemplate, Model: rec.Model, OutputSink: rec.OutputSink},
		Trigger: scheduler.Trigger{Cron: rec.Cron, TZ: rec.Timezone},
	}
	prompt := scheduler.RenderPromptTemplate(rec.PromptTemplate, freshJob, now)
	if prompt == "" {
		return failedRecord(now, "rendered prompt is empty"), nil
	}

	// model-harness-toolset-01MHTS001 WP02 (H-1): resolve the tool
	// boundary this run executes under BEFORE any session exists. Owner
	// ruling B-3: a model-created schedule with an absent, empty or
	// unresolvable allowlist DOES NOT RUN — checked here as well as by the
	// Cedar execute gate upstream, so a caller that reached this
	// dispatcher without that gate still cannot run one unrestricted.
	containment := scheduler.ResolveRunContainment(job.ChatRun, rec)
	if containment.Refuse != "" {
		return failedRecord(now, containment.Refuse), nil
	}
	if containment.Contained && d.deps.Containment == nil {
		return failedRecord(now, "this schedule declares a tool allowlist but no containment registry is wired; it does not run unrestricted"), nil
	}

	// Step 3: parse the output sink. First production caller
	// (spec.md §5.2 step 3) — delivery to the parsed sink is WP07's job
	// (FR-007); this dispatch only needs the parse to log what would be
	// delivered once that lands.
	sinkKind, sinkPath, sinkErr := scheduler.ParseOutputSink(rec.OutputSink)
	if sinkErr != nil {
		log.Warn("scheduler.chat_dispatch.output_sink_unparsed",
			"chat_run_id", id, "raw", rec.OutputSink, "error", sinkErr.Error())
	}

	// Step 6 (spec.md §5.2), run ahead of steps 4-5: resolve the profile
	// (and so the model) before any session exists, so a run that cannot
	// start leaves no empty "Scheduled: …"
	// session behind. rec.Model is a model OVERRIDE, not a profile —
	// spec.md §8 D-1.
	if d.deps.LLM == nil {
		return failedRecord(now, "no LLM connector wired"), nil
	}
	profileID := ""
	if d.deps.DefaultProfile != nil {
		profileID = d.deps.DefaultProfile()
	}
	if profileID == "" {
		return failedRecord(now, "no default LLM profile configured"), nil
	}
	model := rec.Model
	if model == "" && d.deps.DefaultModel != nil {
		model = d.deps.DefaultModel(profileID)
	}

	// One dispatch per schedule at a time (see LiveChatRunDispatcher.
	// inflight). Taken before the conversation is resolved, so the
	// active-turn check below cannot race a sibling dispatch's StartStream.
	if !d.claim(id) {
		return skippedRecord(rec.SessionID, now, skipReasonInFlight), nil
	}
	defer d.unclaim(id)

	// Step 4 (sessions/0348): the schedule's ONE conversation. The first
	// fire creates it; every later fire continues it.
	if d.deps.Sessions == nil {
		return failedRecord(now, "no sessions API wired"), nil
	}
	sess, created, skipReason, serr := d.conversation(ctx, rec)
	if serr != "" {
		return failedRecord(now, serr), nil
	}
	if skipReason != "" {
		return skippedRecord(sess.ID, now, skipReason), nil
	}
	// model-scheduled-jobs-01PMSJ01 WP06: record this session's origin
	// for the lifetime of the dispatch, so a filesystem-permission denial
	// mid-run (core/tools/fs.RecordingPrompter, wired at
	// builtins_wiring.go) can attribute its blocked_permission_requests
	// row to THIS scheduled run rather than defaulting to "interactive".
	// Set again on every fire of the reused conversation (an overwrite,
	// same value) and cleared on every return path via defer, so a turn
	// the user runs in the conversation between fires reads as
	// interactive. Only one dispatch per schedule runs at a time (claim
	// above) and no two schedules share a conversation, so one fire's
	// Clear cannot drop another's Set.
	if d.deps.Origins != nil {
		d.deps.Origins.Set(sess.ID, id)
		defer d.deps.Origins.Clear(sess.ID)
	}
	// WP02 (H-1): contain the session before this fire's turn — re-armed
	// on every fire of the reused conversation. Released when one of THIS
	// run's own streams ends — this sub or a key-rotation redrive of it
	// (ownStreams, below; releaseOnOwnTerminal after a timeout) — never at
	// the timeout itself, and never on another stream in the same session
	// (see ScheduledRunContainmentRegistry). The generation makes that
	// release specific to this fire: a late release from an earlier run
	// of the conversation cannot end this one's containment.
	// scheduled-chat-context-budget: enforce the schedule's context
	// budget BEFORE this fire's prompt is appended and before its
	// containment is armed — the summariser runs no tools, and the
	// containment generation must bind exactly the scheduled turn. A
	// conversation this fire just created has nothing to compact.
	var budget budgetAccount
	var runStarts []string
	if !created && d.deps.Compactor != nil {
		runStarts = d.runStartIDs(ctx, id)
		// model (the override, or what the default resolved to) is the
		// summariser fallback and the window that caps the budget.
		budget.add(d.deps.Compactor.CompactForSchedule(ctx, sess.ID, profileID, model, chat.ScheduledBudget{
			BudgetTokens:   rec.EffectiveContextBudget(),
			KeepRecentRuns: rec.EffectiveKeepRecentRuns(),
			MemoryFocus:    rec.MemoryFocus,
			RunStartIDs:    runStarts,
			PendingPrompt:  prompt,
		}))
		if budget.attempted {
			// The summary call took time: a turn the user started in the
			// conversation meanwhile must not get a concurrent one.
			if (d.deps.ActiveRuns != nil && d.deps.ActiveRuns.HasActiveRun(sess.ID)) ||
				(d.deps.PausedTurns != nil && d.deps.PausedTurns.HasPausedTurnIn(sess.ID)) {
				r := skippedRecord(sess.ID, now, skipReasonBusyAfterCompaction)
				budget.apply(&r)
				return r, nil
			}
		}
	}

	var containGen uint64
	if containment.Contained {
		containGen = d.deps.Containment.Contain(sess.ID, id, containment.Allow)
	}

	// Step 5: append the rendered prompt as the user turn BEFORE calling
	// the LLM view's StartStream. Required: llmview.API.StartStream
	// resolves the turn by scanning session history backwards for the
	// last user row (core/rpc/views/llm/impl.go) — the same contract the
	// interactive chat surface relies on (Sessions_AppendMessage then
	// LLM_StartStream). This append is the turn's ONLY write: the chat
	// runner never persists a user turn (chat-single-writer-01DOGF0G).
	// Without it, StartStream runs with no user message at all.
	promptMsg, aerr := d.deps.Sessions.AppendMessage(ctx, sess.ID, "user", prompt)
	if aerr != nil {
		if containment.Contained {
			d.deps.Containment.Release(sess.ID, containGen)
		}
		r := failedRecord2(sess.ID, now, fmt.Sprintf("append prompt: %v", aerr))
		r.Model = model
		budget.apply(&r)
		d.discardNewEmptySession(ctx, id, created, &r, prompt)
		return r, nil
	}

	// Step 7: subscribe BEFORE starting the stream so a fast completion
	// cannot race the subscription into existence. Topic-filtered to
	// "llm:stream-closed" ONLY (spec.md §2's load-bearing implementation
	// constraint) — topic filtering happens before the bus's per-
	// subscriber buffer, so the high-volume "llm:stream-chunk" topic
	// cannot fill this subscriber's channel and cause the terminal event
	// to be dropped by the bus's slow-subscriber default: arm.
	subCh, cancel := d.deps.Bus.Subscribe(64, "llm:stream-closed", topicAuthResumed)
	defer cancel()

	// Answer exactly THIS fire's prompt row (see startOwnStream).
	subID, sterr := d.startOwnStream(ctx, profileID, sess.ID, rec.Model, promptMsg.ID)
	if sterr != nil {
		// No stream started, so nothing can run under the containment.
		if containment.Contained {
			d.deps.Containment.Release(sess.ID, containGen)
		}
		r := failedRecord2(sess.ID, now, fmt.Sprintf("start stream: %v", sterr))
		r.Model = model
		r.PromptMessageID = promptMsg.ID
		budget.apply(&r)
		d.discardNewEmptySession(ctx, id, created, &r, prompt)
		return r, nil
	}

	// WP02 re-review: the run's OWN streams — this sub plus any
	// key-rotation redrive of it — decide when containment ends; a
	// stream someone else runs in the same session never does.
	own := newOwnStreams(sess.ID, subID, containGen)

	log.Info("scheduler.chat_dispatch.started",
		"chat_run_id", id, "session_id", sess.ID, "sub_id", subID,
		"continued_conversation", !created,
		"output_sink_kind", sinkKind, "output_sink_path", sinkPath)

	deadline := time.NewTimer(d.deps.Timeout)
	defer deadline.Stop()
	// retried: the one session-full retry this fire may make;
	// retryStarted: it got as far as a new stream.
	retried, retryStarted := false, false

	for {
		select {
		case ev, ok := <-subCh:
			if !ok {
				return failedRecord2(sess.ID, now, "event bus subscription closed before a terminal event arrived"), nil
			}
			if containment.Contained && own.observe(ev) {
				d.deps.Containment.Release(sess.ID, own.gen)
			}
			payload, ok := ev.Payload.(chat.StreamClosedPayload)
			if !ok {
				// Not a terminal event (an auth-resumed link, fed to own
				// above) — keep waiting.
				continue
			}
			if payload.SubID != subID {
				continue // another stream's terminal event; keep waiting.
			}
			sessionFull := payload.ErrorKind == chat.StreamClosedErrorKindSessionFull
			if sessionFull && !retried && !created && d.deps.Compactor != nil {
				// Session-full safety net: the conversation outgrew the
				// window anyway (the budget pass failed, or this run's
				// own rows are large). Compact once more — forced, this
				// prompt kept — and answer the same prompt row again.
				// Once only: a second session-full is recorded, not
				// looped on.
				retried = true
				if nsub, ngen, ok := d.retryAfterSessionFull(ctx, rec, containment, sess.ID, profileID, model, promptMsg.ID, append(append([]string(nil), runStarts...), promptMsg.ID), &budget); ok {
					subID = nsub
					retryStarted = true
					own = newOwnStreams(sess.ID, nsub, ngen)
					deadline.Reset(d.deps.Timeout)
					log.Info("scheduler.chat_dispatch.session_full_retry",
						"chat_run_id", id, "session_id", sess.ID, "sub_id", nsub)
					continue
				}
			}
			histRec := d.buildRecord(ctx, sess.ID, promptMsg.ID, now, payload)
			histRec.Model = model
			if sessionFull && d.deps.Compactor != nil && !created {
				histRec.Error = failReasonSessionFull
				if !retryStarted {
					histRec.Error = failReasonSessionFullNoRetry
				}
				if budget.err != nil {
					histRec.Error += fmt.Sprintf(" (summarising older runs failed: %v)", budget.err)
				}
			}
			budget.apply(&histRec)
			// The run is over (terminal event), so a failure that never
			// produced an assistant message in a conversation THIS fire
			// created leaves an empty session nobody will open: delete
			// it. A continued conversation is never deleted. Not on
			// timeout / ctx-cancel below — the run may still be
			// executing there.
			d.discardNewEmptySession(ctx, id, created, &histRec, prompt)
			if sinkKind == "banner" {
				d.deliverBanner(id, rec.Name, histRec.SessionID, histRec)
			}
			return histRec, nil
		case <-deadline.C:
			if containment.Contained && !own.released {
				d.releaseOnOwnTerminal(own, subCh)
			}
			histRec := failedRecord2(sess.ID, now, fmt.Sprintf("timed out after %s waiting for the run to finish", d.deps.Timeout))
			histRec.Model = model
			histRec.PromptMessageID = promptMsg.ID
			budget.apply(&histRec)
			if sinkKind == "banner" {
				d.deliverBanner(id, rec.Name, histRec.SessionID, histRec)
			}
			return histRec, nil
		case <-ctx.Done():
			if containment.Contained && !own.released {
				d.releaseOnOwnTerminal(own, subCh)
			}
			histRec := failedRecord2(sess.ID, now, fmt.Sprintf("context cancelled while awaiting completion: %v", ctx.Err()))
			histRec.Model = model
			histRec.PromptMessageID = promptMsg.ID
			budget.apply(&histRec)
			if sinkKind == "banner" {
				d.deliverBanner(id, rec.Name, histRec.SessionID, histRec)
			}
			return histRec, nil
		}
	}
}

// containmentWatchMax bounds how long releaseOnOwnTerminal waits before
// it re-checks the run. While one of the run's own subs is still live —
// executing, or paused for key rotation and so redrivable at any time
// (PausedTurns.SubStillLive) — the watch is extended: a redrive days
// later must still be seen ending. A paused turn that is dropped instead
// is closed by the runner ("abandoned"), which the watch sees as the
// run's own terminal. Only a run whose subs are neither live nor ever
// observed ending stays contained when the watch gives up — the
// fail-safe direction — and the watcher goroutine does not leak. A var so
// tests can shorten it.
var containmentWatchMax = 24 * time.Hour

// topicAuthResumed is the chat runner's redrive announcement
// (chat.AuthResumedPayload), which links a key-rotation redrive's new sub
// id to the paused sub it continues.
const topicAuthResumed = "provider:auth-resumed"

// ownStreams tracks which streams are the scheduled run's OWN: the
// dispatched sub plus every key-rotation redrive descended from it
// (WP02 re-review — release on ANY stream in the session let a user who
// opened the "Scheduled:" session mid-run end containment by finishing an
// interactive turn while the scheduled stream still ran). A redrive's
// terminal event can reach the bus BEFORE its auth-resumed link (the
// runner emits auth-resumed after StartStream returns), so terminals of
// not-yet-owned subs in the session are remembered and matched when the
// link arrives. Not safe for concurrent use; one goroutine feeds it.
type ownStreams struct {
	sessionID   string
	owned       map[string]bool
	endedOthers map[string]bool
	released    bool
	// gen is the containment generation this run armed (Contain's return);
	// its release presents it, so it can only end its own binding.
	gen uint64
}

func newOwnStreams(sessionID, subID string, gen uint64) *ownStreams {
	return &ownStreams{sessionID: sessionID, owned: map[string]bool{subID: true}, endedOthers: map[string]bool{}, gen: gen}
}

// observe feeds one bus event and reports whether it is the moment the
// run's own stream ended (true at most once).
func (o *ownStreams) observe(ev BusEvent) bool {
	if o.released {
		return false
	}
	switch p := ev.Payload.(type) {
	case chat.StreamClosedPayload:
		if p.SessionID != o.sessionID {
			return false
		}
		if o.owned[p.SubID] {
			o.released = true
			return true
		}
		o.endedOthers[p.SubID] = true
	case chat.AuthResumedPayload:
		if !o.owned[p.PausedSubID] || p.NewSubID == "" {
			return false
		}
		o.owned[p.NewSubID] = true
		if o.endedOthers[p.NewSubID] {
			o.released = true
			return true
		}
	}
	return false
}

// releaseOnOwnTerminal handles a run that timed out or was cancelled while
// its stream may still be executing: containment stays in place NOW and is
// released when one of the run's OWN streams (the original, or a
// key-rotation redrive of it) terminates — never on someone else's stream
// in the same session. The new subscription is taken BEFORE pending is
// drained, so nothing is lost in the hand-off. After containmentWatchMax
// the watch stops and the session stays contained (fail-safe).
func (d *LiveChatRunDispatcher) releaseOnOwnTerminal(own *ownStreams, pending <-chan BusEvent) {
	ch, cancel := d.deps.Bus.Subscribe(64, "llm:stream-closed", topicAuthResumed)
	for drained := false; !drained; {
		select {
		case ev, ok := <-pending:
			if !ok {
				drained = true
			} else if own.observe(ev) {
				cancel()
				d.deps.Containment.Release(own.sessionID, own.gen)
				return
			}
		default:
			drained = true
		}
	}
	watchMax := containmentWatchMax
	go func() {
		defer cancel()
		timer := time.NewTimer(watchMax)
		defer timer.Stop()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if own.observe(ev) {
					d.deps.Containment.Release(own.sessionID, own.gen)
					return
				}
			case <-timer.C:
				if d.ownSubLive(own) {
					timer.Reset(watchMax)
					continue
				}
				return
			}
		}
	}()
}

// ownSubLive reports whether the run may still be running (see
// containmentWatchMax): one of its own subs is executing or paused, or a
// turn is executing in its session — a redrive of the run whose link
// (auth-resumed) the watch has not seen yet. The latter can also be a
// user's turn; that only keeps the WATCH alive, never the containment.
// Without the seams nothing is known to be live.
func (d *LiveChatRunDispatcher) ownSubLive(own *ownStreams) bool {
	if d.deps.PausedTurns != nil {
		for sub := range own.owned {
			if d.deps.PausedTurns.SubStillLive(sub) {
				return true
			}
		}
	}
	return d.deps.ActiveRuns != nil && d.deps.ActiveRuns.HasActiveRun(own.sessionID)
}

// deliverBanner publishes TopicScheduledChatBanner with this run's
// terminal outcome (model-scheduled-jobs-01PMSJ01 WP07, FR-007). A nil
// Broker is a silent no-op — see ChatRunDispatcherDeps.Broker's doc.
func (d *LiveChatRunDispatcher) deliverBanner(chatRunID, name, sessionID string, rec scheduler.ChatRunHistoryRecord) {
	if d.deps.Broker == nil {
		return
	}
	d.deps.Broker.Publish(TopicScheduledChatBanner, ScheduledChatBannerPayload{
		ChatRunID:     chatRunID,
		Name:          name,
		SessionID:     sessionID,
		Status:        rec.Status,
		OutputSnippet: rec.OutputSnippet,
		Error:         rec.Error,
	})
}

// buildRecord translates the terminal payload's Reason/ErrorKind into a
// ChatRunHistoryRecord.Status and reads the assistant reply back from
// PERSISTED history, not from the stream (spec.md §5.2 step 8 / D-4 — the
// stream is drop-tolerant, history is not). Snippet and cost come from
// THIS run's turn only (the rows spanned by promptID, runTurnRows): the
// conversation also holds every earlier run and any turn the user ran in
// it, so neither "the last assistant row" nor the session's cumulative
// usage answers what this run did or cost.
func (d *LiveChatRunDispatcher) buildRecord(ctx context.Context, sessionID, promptID string, startedAt time.Time, payload chat.StreamClosedPayload) scheduler.ChatRunHistoryRecord {
	ended := time.Now().UTC()
	rec := scheduler.ChatRunHistoryRecord{
		SessionID:       sessionID,
		StartedAt:       startedAt,
		EndedAt:         &ended,
		PromptMessageID: promptID,
	}
	switch {
	case payload.Reason == "completed" && payload.FinishReason == "paused":
		// The chat graph re-entered ask_user mid-turn and parked. Without
		// WP05's unattended posture nothing answers it, so treat this as
		// a failure rather than report a completion the run never
		// reached (FR-002) — WP05 closes this by withholding the ask
		// tool / seeding a DefaultAnswer for unattended runs.
		rec.Status = "failed"
		rec.Error = "run paused on ask_user; requires WP05's unattended posture"
	case payload.Reason == "completed":
		rec.Status = "completed"
	default:
		rec.Status = "failed"
		if payload.Message != "" {
			rec.Error = payload.Message
		} else {
			rec.Error = payload.Reason
		}
	}
	if d.deps.Sessions != nil {
		if msgs, err := d.deps.Sessions.ListMessages(ctx, sessionID); err == nil {
			turn := runTurnRows(msgs, promptID)
			rec.OutputSnippet = lastAssistantSnippet(turn)
			rec.CostUSD = turnCost(turn)
		}
	}
	return rec
}

// runTurnRows returns the rows the turn opened by user message promptID
// wrote: every later row whose TurnSpanID is promptID, plus — for rows
// written without span metadata — the unspanned non-user rows before the
// next user message. Rows of any other turn (an earlier run, a turn the
// user ran in the conversation) are excluded. nil when promptID is not in
// msgs.
func runTurnRows(msgs []sessionsview.Message, promptID string) []sessionsview.Message {
	start := -1
	for i, m := range msgs {
		if m.ID == promptID {
			start = i
			break
		}
	}
	if start < 0 || promptID == "" {
		return nil
	}
	var out []sessionsview.Message
	otherTurnBegan := false
	for _, m := range msgs[start+1:] {
		switch {
		case m.TurnSpanID == promptID:
			out = append(out, m)
		case m.Role == "user":
			otherTurnBegan = true
		case m.TurnSpanID == "" && !otherTurnBegan:
			out = append(out, m)
		}
	}
	return out
}

// turnCost sums the per-row cost the usage pipeline recorded on a turn's
// rows (session_messages.cost_usd). Rows with no recorded cost add 0.
func turnCost(rows []sessionsview.Message) float64 {
	var total float64
	for _, m := range rows {
		if m.CostUSD != nil {
			total += *m.CostUSD
		}
	}
	return total
}

// discardEmptyFailedSession deletes the run's session when a failed run
// left nothing in it but the dispatcher's own prompt. Such a session has
// no output to read and is invisible in the sidebar; the history row
// keeps the error, model and time, and its SessionID is cleared so
// nothing links to the deleted session.
//
// The rule is deliberately strict: the session is kept if it holds ANY
// row other than the user prompt this dispatch appended — an assistant
// segment, a tool call or tool result (the record of what the agent
// did, e.g. a mail it sent before failing), a system note. A session
// whose rows cannot be listed is kept. Completed runs never reach here.
func (d *LiveChatRunDispatcher) discardEmptyFailedSession(ctx context.Context, rec *scheduler.ChatRunHistoryRecord, prompt string) {
	if rec.Status != "failed" || rec.SessionID == "" || d.deps.Sessions == nil {
		return
	}
	msgs, err := d.deps.Sessions.ListMessages(ctx, rec.SessionID)
	if err != nil {
		return // cannot prove it is empty; keep it
	}
	for _, m := range msgs {
		if m.Role != "user" || m.Content != prompt {
			return
		}
	}
	if derr := d.deps.Sessions.Delete(context.WithoutCancel(ctx), rec.SessionID); derr != nil {
		logging.L().Warn("scheduler.chat_dispatch.discard_session_failed",
			"session_id", rec.SessionID, "error", derr.Error())
		return
	}
	logging.L().Info("scheduler.chat_dispatch.discarded_empty_session",
		"session_id", rec.SessionID, "reason", rec.Error)
	rec.SessionID = ""
}

// discardNewEmptySession applies discardEmptyFailedSession only to a
// conversation THIS fire created (sessions/0348): a continued conversation
// holds earlier runs and is never deleted, whatever this fire left in it.
// When the new session is deleted the schedule's stored link is cleared
// too, so the next fire starts a fresh conversation instead of chasing a
// deleted id.
func (d *LiveChatRunDispatcher) discardNewEmptySession(ctx context.Context, chatRunID string, created bool, rec *scheduler.ChatRunHistoryRecord, prompt string) {
	if !created {
		return
	}
	sessionID := rec.SessionID
	d.discardEmptyFailedSession(ctx, rec, prompt)
	if rec.SessionID == "" && sessionID != "" && d.deps.Store != nil {
		if err := d.deps.Store.SetSessionID(context.WithoutCancel(ctx), chatRunID, ""); err != nil {
			logging.L().Warn("scheduler.chat_dispatch.clear_conversation_failed",
				"chat_run_id", chatRunID, "error", err.Error())
		}
	}
}

// conversation resolves the session this fire runs in (sessions/0348):
// the schedule's stored conversation when it still exists, else a new
// "Scheduled: <name>" session whose id is stored on the schedule. A
// non-empty skipReason means the stored conversation is busy — a turn is
// executing (ActiveRuns) or paused awaiting credentials (PausedTurns) —
// and the fire must not start a second turn there. A non-empty failure is
// the history Error for a fire that cannot proceed.
func (d *LiveChatRunDispatcher) conversation(ctx context.Context, rec scheduler.ChatRunRecord) (sess sessionsview.Session, created bool, skipReason, failure string) {
	if rec.SessionID != "" {
		existing, err := d.deps.Sessions.Get(ctx, rec.SessionID)
		switch {
		case err == nil:
			if d.deps.ActiveRuns != nil && d.deps.ActiveRuns.HasActiveRun(existing.ID) {
				return existing, false, skipReasonActiveTurn, ""
			}
			if d.deps.PausedTurns != nil && d.deps.PausedTurns.HasPausedTurnIn(existing.ID) {
				return existing, false, skipReasonPausedTurn, ""
			}
			return existing, false, "", ""
		case errors.Is(err, session.ErrSessionNotFound):
			// The user deleted the conversation: start a new one below.
			logging.L().Info("scheduler.chat_dispatch.conversation_gone",
				"chat_run_id", rec.ID, "session_id", rec.SessionID)
		default:
			// Cannot tell whether it still exists; creating a second
			// conversation on a transient read error would split the
			// schedule's history, so this fire fails instead.
			return sessionsview.Session{}, false, "", fmt.Sprintf("look up the schedule's conversation: %v", err)
		}
	}
	name := rec.Name
	if name == "" {
		name = "Scheduled chat run"
	}
	fresh, err := d.deps.Sessions.Create(ctx, "Scheduled: "+name)
	if err != nil {
		return sessionsview.Session{}, false, "", fmt.Sprintf("create session: %v", err)
	}
	if serr := d.deps.Store.SetSessionID(ctx, rec.ID, fresh.ID); serr != nil {
		// The run still proceeds in the new session; the next fire,
		// finding no link, starts another conversation. Logged, because
		// that is exactly the one-chat-per-fire behaviour this column
		// exists to end.
		logging.L().Warn("scheduler.chat_dispatch.store_conversation_failed",
			"chat_run_id", rec.ID, "session_id", fresh.ID, "error", serr.Error())
	}
	return fresh, true, "", ""
}

// lastAssistantSnippet returns the newest non-empty assistant reply among
// a run's turn rows (read back from persisted history, never from the
// stream — spec.md D-4), truncated to a reasonable snippet length.
func lastAssistantSnippet(rows []sessionsview.Message) string {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Role == "assistant" && rows[i].Content != "" {
			const maxSnippet = 2000
			c := rows[i].Content
			if len(c) > maxSnippet {
				c = c[:maxSnippet]
			}
			return c
		}
	}
	return ""
}

// startOwnStream answers exactly the prompt row promptID, never "the
// newest user row": the conversation is shared with the user, and a
// message they send between the append and this call would otherwise be
// the turn this run answers (and runs contained), with the scheduled
// prompt never answered. A view that cannot target a row is refused
// rather than risked.
func (d *LiveChatRunDispatcher) startOwnStream(ctx context.Context, profileID, sessionID, model, promptID string) (string, error) {
	ts, ok := d.deps.LLM.(llmview.MessageTargetedStreamer)
	if !ok {
		return "", errors.New("the LLM view cannot start a stream for a specific message, so a scheduled run could answer another turn in its conversation")
	}
	return ts.StartStreamForMessage(ctx, profileID, sessionID, model, promptID)
}

// runStartIDs returns the session rows this schedule's earlier fires
// appended their prompts as — the run boundaries the context budget
// counts (scheduled_chat_run_history.prompt_message_id, sessions/0349).
// A read failure returns none: the budget then counts every user row as
// a run, the reading it gives a conversation from before 0349.
func (d *LiveChatRunDispatcher) runStartIDs(ctx context.Context, chatRunID string) []string {
	hist, err := d.deps.Store.History(ctx, chatRunID, promptHistoryDepth)
	if err != nil {
		logging.L().Warn("scheduler.chat_dispatch.run_boundaries_unread",
			"chat_run_id", chatRunID, "error", err.Error())
		return nil
	}
	ids := make([]string, 0, len(hist))
	for _, h := range hist {
		if h.PromptMessageID != "" {
			ids = append(ids, h.PromptMessageID)
		}
	}
	return ids
}

// retryAfterSessionFull is the one retry a fire gets after its stream
// ended "session full": a forced budget pass that keeps this fire's
// prompt (and keep_recent_runs earlier runs, budget permitting), then a
// new stream for the same prompt row, contained again under a fresh
// generation when the schedule is contained. ok=false when nothing was
// compacted or the stream could not start; the caller records the
// failure.
func (d *LiveChatRunDispatcher) retryAfterSessionFull(ctx context.Context, rec scheduler.ChatRunRecord, containment scheduler.RunContainment,
	sessionID, profileID, model, promptID string, runStarts []string, budget *budgetAccount) (subID string, gen uint64, ok bool) {
	budget.add(d.deps.Compactor.CompactForSchedule(ctx, sessionID, profileID, model, chat.ScheduledBudget{
		BudgetTokens:   rec.EffectiveContextBudget(),
		KeepRecentRuns: rec.EffectiveKeepRecentRuns() + 1, // + this fire's own run
		MemoryFocus:    rec.MemoryFocus,
		RunStartIDs:    runStarts,
		Force:          true,
		MinKeepRuns:    1,
	}))
	if !budget.compactedLast {
		return "", 0, false
	}
	if containment.Contained {
		gen = d.deps.Containment.Contain(sessionID, rec.ID, containment.Allow)
	}
	sub, err := d.startOwnStream(ctx, profileID, sessionID, rec.Model, promptID)
	if err != nil {
		if containment.Contained {
			d.deps.Containment.Release(sessionID, gen)
		}
		logging.L().Warn("scheduler.chat_dispatch.session_full_retry_failed",
			"chat_run_id", rec.ID, "session_id", sessionID, "error", err.Error())
		return "", 0, false
	}
	return sub, gen, true
}

// budgetAccount accumulates what the context budget did during one fire
// (the pre-fire pass and, at most, one session-full retry) so the
// history row reports it: the summarisation cost is added to the run's
// cost and stated on its own (compaction_cost_usd), never dropped.
type budgetAccount struct {
	// attempted: some pass was over budget (or forced) and asked the
	// summariser — the fire's cost line then names it.
	attempted bool
	// compactedLast: the latest pass wrote a summary.
	compactedLast bool
	calls         int
	costUSD       float64
	// err is the latest pass's failure, nil when it succeeded.
	err error
}

func (b *budgetAccount) add(out chat.ScheduledBudgetOutcome, err error) {
	if errors.Is(err, chat.ErrScheduledBudgetUnavailable) {
		b.compactedLast = false
		return
	}
	b.attempted = b.attempted || out.Attempted
	b.compactedLast = out.Compacted
	b.calls += out.Cost.Calls
	b.costUSD += out.Cost.TotalUSD
	b.err = err
}

// apply stamps the account onto a history row: CompactionCostUSD set
// (possibly 0, a price the catalog does not know) whenever a summariser
// call was made, and included in CostUSD.
//
// Over budget with no call made (nothing aged out yet) adds nothing.
func (b *budgetAccount) apply(r *scheduler.ChatRunHistoryRecord) {
	if b.calls == 0 {
		return
	}
	c := b.costUSD
	r.CompactionCostUSD = &c
	r.CostUSD += c
}

// failedRecord builds a ChatRunHistoryRecord for a failure that occurred
// before a session existed.
func failedRecord(now time.Time, reason string) scheduler.ChatRunHistoryRecord {
	ended := time.Now().UTC()
	return scheduler.ChatRunHistoryRecord{
		Status:    "failed",
		StartedAt: now,
		EndedAt:   &ended,
		Error:     reason,
	}
}

// skippedRecord is the history row of a fire that did not run because its
// conversation already had a turn in flight. sessionID links the row to
// that conversation ("" when the schedule has none yet).
func skippedRecord(sessionID string, now time.Time, reason string) scheduler.ChatRunHistoryRecord {
	r := failedRecord(now, reason)
	r.Status = scheduler.ChatRunStatusSkipped
	r.SessionID = sessionID
	return r
}

// failedRecord2 is failedRecord plus a session id, for failures that
// occurred after the session was created.
func failedRecord2(sessionID string, now time.Time, reason string) scheduler.ChatRunHistoryRecord {
	r := failedRecord(now, reason)
	r.SessionID = sessionID
	return r
}

var _ scheduler.ChatRunDispatcher = (*LiveChatRunDispatcher)(nil)

// scheduledCompactor converts a possibly-nil *chat.ScheduledBudgetCompactor
// into the dispatcher's interface without producing a typed-nil value
// (which would read as wired and fail every call).
func scheduledCompactor(c *chat.ScheduledBudgetCompactor) ScheduledConversationCompactor {
	if c == nil {
		return nil
	}
	return c
}
