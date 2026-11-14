// Chat-run cron engine (mission model-scheduled-jobs-01PMSJ01, WP03).
//
// Before this file, nothing loaded a scheduled_chat_runs row into any
// ticking engine (spec.md §1.2): core/scheduler.Scheduler declared a
// Job/Store-based interface with zero implementations, and the repository's
// only cron.New(...) served workflow_schedules alone
// (core/workflows/scheduler/cron_scheduler.go). ChatCronEngine is a second,
// purpose-built engine — not an implementation of the generic
// scheduler.Scheduler interface, which is Job/Store-shaped in a way that
// does not honestly fit the ChatRunRecord-backed persistence model actually
// in use (no on_missed column, no generic Store implementation anywhere in
// the tree). See docs/unwired-ledger.md for the dated justification of
// scheduler.Scheduler, scheduler.Store, Job.OnMissed and Job.MissedPolicy
// per owner ruling A-0 (the delete lane is frozen; an interface that cannot
// be honestly satisfied is justified, not deleted).
//
// Timezone handling follows core/workflows/scheduler/cron_scheduler.go:
// robfig/cron/v3's WithLocation is process-wide, so per-row timezone is
// applied via the "CRON_TZ=<IANA>" spec prefix instead.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// ErrChatCronInvalidExpr is returned when Sync encounters an unparseable
// cron expression. The row is not removed from the store — only the cron
// registration is skipped — so the user's next Update can fix the
// expression without losing the record.
var ErrChatCronInvalidExpr = errors.New("scheduler: invalid chat-run cron expression")

// ErrChatCronInvalidTimezone is returned when Sync encounters an IANA
// timezone name that time.LoadLocation does not recognise.
var ErrChatCronInvalidTimezone = errors.New("scheduler: invalid chat-run timezone")

// chatEntry is the per-row in-memory registration state.
//
// A TriggerKindCron row (kind=="cron") is armed via the underlying
// robfig/cron engine (entry is its cron.EntryID); a TriggerKindOnce row
// (kind=="once", model-scheduled-jobs-01PMSJ01 WP08) is armed via a
// one-shot time.Timer instead — it has no recurring schedule to give
// cron.Cron, only a single fire time.
type chatEntry struct {
	id    string
	kind  string       // TriggerKindCron or TriggerKindOnce
	entry cron.EntryID // valid when kind == TriggerKindCron
	// timer is non-nil once a TriggerKindOnce entry has actually been
	// armed. Registration (registerOnce) always records the entry so
	// Registered(id) reports true immediately, matching how a cron entry
	// added via cron.AddFunc before Start() is already "registered" with
	// the underlying cron.Cron even though it will not tick yet — but the
	// timer itself is created lazily, in Start(), for exactly the same
	// reason cron.Cron does not fire before Start() is called: AC-003's
	// invariant that construction alone must not arm ticks
	// (core/rpc.API.SetContext calls Start explicitly) would otherwise be
	// silently broken for the 'once' kind, since time.AfterFunc has no
	// concept of "added but not started" the way robfig/cron does.
	timer *time.Timer // valid when kind == TriggerKindOnce and the engine has been Start()ed
	runAt time.Time   // valid when kind == TriggerKindOnce
	cron  string
	tz    string
}

// ChatCronEngineConfig holds the wiring for NewChatCronEngine.
type ChatCronEngineConfig struct {
	// Store is the ScheduledChatStore backing scheduled_chat_runs +
	// scheduled_chat_run_history. Required — a nil Store means Start
	// registers nothing and Sync/Unregister return
	// ErrChatCronStoreUnavailable.
	Store ScheduledChatStore
	// Dispatcher fires the actual chat run when a cron entry ticks or
	// RunNow is called through the engine. Per plan.md Rule 2 / this
	// mission's WP05, production wiring leaves this nil at construction
	// time and assigns it later via SetDispatcher, once the unattended
	// posture is in place. A nil Dispatcher at fire time is not a
	// fallback to a fabricated success (FR-002) — see fireSync.
	Dispatcher ChatRunDispatcher
	// Cedar is the policy gate consulted before every cron-triggered
	// fire (model-scheduled-jobs-01PMSJ01 WP09). Before this field
	// existed, fireSync went straight from the store read to the
	// dispatcher with NO Cedar evaluation at all — the RunNow path
	// (core/rpc/views/scheduledchat.API.RunNow) was the only caller of
	// GateScheduledChatExecute, so a schedule that only ever fired on
	// its cron never consulted policy. ActionScheduledRunExecute's own
	// doc claims it "gates background dispatch (both cron-triggered and
	// RunNow paths)" (core/policy/cedar/types.go) — that sentence was
	// false for the cron half until this field was wired. nil is
	// allowed (matches GateScheduledChatExecute's nil-gate contract:
	// default-allow for created_by=="user", fail-closed deny for
	// created_by=="model").
	Cedar cedar.Gate
}

// ErrChatCronStoreUnavailable is returned by Sync / Unregister when the
// engine was constructed without a Store.
var ErrChatCronStoreUnavailable = errors.New("scheduler: chat cron engine has no store")

// ChatCronEngine is the production cron engine for scheduled_chat_runs.
//
// It reacts to Create / Update / Delete / SetEnabled on the
// scheduledchat.API surface (via Sync / Unregister) rather than owning a
// separate write path — scheduled_chat_runs remains a ScheduledChatStore
// responsibility. All exported methods are safe for concurrent use; the
// cron engine fires ticks from a pool goroutine.
type ChatCronEngine struct {
	mu       sync.RWMutex
	c        *cron.Cron
	entries  map[string]*chatEntry // chat run id -> registration
	store    ScheduledChatStore
	dispatch ChatRunDispatcher
	cedar    cedar.Gate
	started  bool

	// fireCtx is the context every fire goroutine runs under. Start
	// creates it and Stop cancels it, so a dispatch still waiting on its
	// run's terminal event returns promptly instead of holding Stop for
	// the dispatcher's own (minutes-long) timeout. Guarded by mu.
	fireCtx    context.Context
	fireCancel context.CancelFunc

	// fires counts the fire goroutines (cron ticks and one-shot timers)
	// that may still touch the store. Stop waits for it so no fire can
	// write after the caller closes storage — before this, Stop returned
	// while a tick's AppendHistory was still running and the owner's
	// Close raced it (TestChatCronEngine_RegisterAndFire under -race).
	fires fireTracker
}

// DefaultChatCronStopTimeout bounds how long Stop waits for in-flight
// fires to finish. StopContext takes an explicit bound instead.
const DefaultChatCronStopTimeout = 30 * time.Second

// fireTracker counts in-flight fire goroutines. Not a sync.WaitGroup: a
// later Start can begin a new fire while an earlier Stop is still
// waiting, and a WaitGroup forbids an Add from zero concurrent with Wait
// (same reasoning as core/rpc/views/agentgraph/chat/run_drain.go). The
// zero value is ready to use.
type fireTracker struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed when n returns to 0; nil while n == 0
}

func (t *fireTracker) add() {
	t.mu.Lock()
	if t.n == 0 {
		t.idle = make(chan struct{})
	}
	t.n++
	t.mu.Unlock()
}

func (t *fireTracker) done() {
	t.mu.Lock()
	t.n--
	if t.n == 0 {
		close(t.idle)
		t.idle = nil
	}
	t.mu.Unlock()
}

// wait blocks until no fire is in flight or ctx ends.
func (t *fireTracker) wait(ctx context.Context) error {
	t.mu.Lock()
	ch := t.idle
	t.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NewChatCronEngine constructs a ChatCronEngine and reloads enabled rows
// from cfg.Store, registering each with the cron engine. It does NOT start
// the cron engine — call Start() before ticks fire. A row with a malformed
// cron expression is logged and skipped, not aborted: one bad row must not
// prevent every other schedule (and the rest of chassis boot) from coming
// up (mirrors core/workflows/scheduler/cron_scheduler.go:83-100).
func NewChatCronEngine(ctx context.Context, cfg ChatCronEngineConfig) (*ChatCronEngine, error) {
	e := &ChatCronEngine{
		c: cron.New(
			cron.WithSeconds(),
			cron.WithParser(cron.NewParser(
				cron.Second|cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor,
			)),
		),
		entries:  make(map[string]*chatEntry),
		store:    cfg.Store,
		dispatch: cfg.Dispatcher,
		cedar:    cfg.Cedar,
	}
	if e.store != nil {
		recs, err := e.store.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("scheduler: chat cron engine: load scheduled_chat_runs: %w", err)
		}
		for _, r := range recs {
			if !r.Enabled {
				continue
			}
			if err := e.registerRecord(r); err != nil {
				slog.WarnContext(ctx, "scheduler.chat_cron.boot_register_failed",
					"chat_run_id", r.ID, "cron", r.Cron, "trigger_kind", r.EffectiveTriggerKind(), "error", err.Error())
				continue
			}
		}
	}
	return e, nil
}

// SetDispatcher assigns the dispatcher used at fire time. Production
// wiring (core/rpc/api.go) calls this once, before Start(), as part of
// WP05's unattended-posture assignment — see plan.md Rule 2. Safe to call
// at any time; a tick concurrent with SetDispatcher observes either the
// old or the new dispatcher, never a torn value.
func (e *ChatCronEngine) SetDispatcher(d ChatRunDispatcher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dispatch = d
}

// buildSpec normalises cronExpr to the 6-field (seconds-first) form this
// engine's parser accepts, then prepends the CRON_TZ prefix when tz is
// non-empty (matching core/workflows/scheduler/cron_scheduler.go's
// buildSpec for the timezone half).
//
// scheduled_chat_runs.cron is user-facing, standard 5-field cron —
// core/rpc/views/scheduledchat.API's Create/Update do not, and must not,
// require a seconds field; that is the same format
// core/workflows/scheduler.CronScheduler's own users type. This engine's
// robfig/cron/v3 instance is configured with a 6-field (cron.Second-
// inclusive) parser instead of that package's 5-field one so tests can
// drive real cron ticks at 1-second granularity instead of waiting on a
// wall-clock minute boundary. normalizeCronFields reconciles the two: a
// standard 5-field expression gets a synthetic leading "0 " (seconds=0),
// so both "0 9 * * *" (what a user types) and "* * * * * *" (what an
// engine-level test writes to fire every second) parse — one register()
// call site, not a public/test-only fork.
func buildChatSpec(cronExpr, tz string) (string, error) {
	spec := normalizeCronFields(cronExpr)
	if tz == "" || tz == "UTC" {
		return spec, nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", fmt.Errorf("%w: %s", ErrChatCronInvalidTimezone, tz)
	}
	return "CRON_TZ=" + tz + " " + spec, nil
}

// normalizeCronFields prepends a "0" seconds field to a standard 5-field
// cron expression (minute hour dom month dow — exactly 4 interior
// spaces). Already-6-field expressions and descriptors ("@every 1h",
// "@daily") are returned unchanged: cron.Descriptor-handled strings
// don't have five space-separated fields to miscount, and an
// already-6-field expression needs no help.
func normalizeCronFields(cronExpr string) string {
	if strings.Count(cronExpr, " ") == 4 {
		return "0 " + cronExpr
	}
	return cronExpr
}

// register adds or replaces the cron entry for id. Caller must not hold e.mu.
func (e *ChatCronEngine) register(id, cronExpr, tz string) error {
	spec, err := buildChatSpec(cronExpr, tz)
	if err != nil {
		return err
	}
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(spec); err != nil {
		return fmt.Errorf("%w: %v", ErrChatCronInvalidExpr, err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.disarmLocked(id)
	entryID, err := e.c.AddFunc(spec, func() { e.fire(id) })
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChatCronInvalidExpr, err)
	}
	e.entries[id] = &chatEntry{id: id, kind: TriggerKindCron, entry: entryID, cron: cronExpr, tz: tz}
	return nil
}

// ErrChatCronMissingRunAt is returned when a TriggerKindOnce row has a
// nil RunAt — a malformed row (the API layer requires RunAt for a
// trigger_kind='once' Create/Update, so this should only occur on a
// hand-edited database). Like ErrChatCronInvalidExpr, the row is not
// removed from the store; only the timer registration is skipped.
var ErrChatCronMissingRunAt = errors.New("scheduler: chat-run trigger_kind='once' row has no run_at")

// registerOnce records a one-shot registration for id at runAt (or
// immediately, once armed, if runAt has already passed — a one-shot
// schedule whose time passed while the app was closed still fires on the
// next boot rather than silently vanishing, mirroring the "missed tick
// still counts" posture a user scheduling "run this once at 6pm" would
// expect). The underlying time.Timer is only created if the engine has
// already been Start()ed; otherwise Start() arms it — see chatEntry.timer's
// doc for why. Caller must not hold e.mu.
func (e *ChatCronEngine) registerOnce(id string, runAt time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disarmLocked(id)
	ent := &chatEntry{id: id, kind: TriggerKindOnce, runAt: runAt}
	if e.started {
		ent.timer = e.armOnceLocked(id, runAt)
	}
	e.entries[id] = ent
}

// armOnceLocked creates the actual time.Timer for a one-shot entry.
// Caller must hold e.mu.
func (e *ChatCronEngine) armOnceLocked(id string, runAt time.Time) *time.Timer {
	delay := time.Until(runAt)
	if delay < 0 {
		delay = 0
	}
	return time.AfterFunc(delay, func() { e.fireOnce(id) })
}

// registerRecord arms rec according to its EffectiveTriggerKind. Caller
// must not hold e.mu.
func (e *ChatCronEngine) registerRecord(rec ChatRunRecord) error {
	if rec.EffectiveTriggerKind() == TriggerKindOnce {
		if rec.RunAt == nil {
			return ErrChatCronMissingRunAt
		}
		e.registerOnce(rec.ID, *rec.RunAt)
		return nil
	}
	return e.register(rec.ID, rec.Cron, rec.Timezone)
}

// disarmLocked removes any existing registration (cron entry or one-shot
// timer) for id. Caller must hold e.mu.
func (e *ChatCronEngine) disarmLocked(id string) {
	existing, ok := e.entries[id]
	if !ok {
		return
	}
	switch existing.kind {
	case TriggerKindOnce:
		if existing.timer != nil {
			existing.timer.Stop()
		}
	default:
		e.c.Remove(existing.entry)
	}
	delete(e.entries, id)
}

// unregister removes the cron entry or one-shot timer for id, if any.
// Caller must not hold e.mu.
func (e *ChatCronEngine) unregister(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disarmLocked(id)
}

// Sync re-reads the row for id from the store and arms or disarms its
// registration to match: enabled -> registered with the row's current
// trigger kind (cron entry for 'cron', one-shot timer for 'once',
// replacing any stale registration so inline edits take effect without a
// restart); disabled or not found -> disarmed.
//
// Called by core/rpc/views/scheduledchat.API after Create, Update and
// SetEnabled. A malformed cron expression or a trigger_kind='once' row
// with no run_at is returned as an error (the caller logs it — a bad
// row does not roll back the already-persisted write) rather than
// silently accepted or silently dropped.
func (e *ChatCronEngine) Sync(ctx context.Context, id string) error {
	if e.store == nil {
		return ErrChatCronStoreUnavailable
	}
	rec, err := e.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrChatRunNotFound) {
			e.unregister(id)
			return nil
		}
		return fmt.Errorf("scheduler: chat cron engine: sync get: %w", err)
	}
	if !rec.Enabled {
		e.unregister(id)
		return nil
	}
	return e.registerRecord(rec)
}

// Unregister disarms the cron entry for id. Called by
// core/rpc/views/scheduledchat.API after Delete, when the row (and
// therefore Sync's read) no longer exists.
func (e *ChatCronEngine) Unregister(ctx context.Context, id string) error {
	if e.store == nil {
		return ErrChatCronStoreUnavailable
	}
	e.unregister(id)
	return nil
}

// Start starts the underlying cron engine AND arms every registered
// TriggerKindOnce entry's timer (which registerOnce deliberately left
// unarmed until now — see chatEntry.timer's doc). Idempotent.
func (e *ChatCronEngine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return
	}
	e.c.Start()
	e.started = true
	e.fireCtx, e.fireCancel = context.WithCancel(context.Background())
	for id, ent := range e.entries {
		if ent.kind == TriggerKindOnce && ent.timer == nil {
			ent.timer = e.armOnceLocked(id, ent.runAt)
		}
	}
}

// Stop halts cron dispatch, stops every armed one-shot timer, refuses
// any fire that has not yet begun, cancels the context of fires already
// running, and waits — bounded by DefaultChatCronStopTimeout — for those
// fires to finish. When Stop returns (without a timeout) no fire
// goroutine can touch the store, so the caller may close storage.
// Entries survive Stop with timer reset to nil, so a later Start()
// re-arms them (mirrors robfig/cron's own Stop-then-Start resumption for
// entries added via AddFunc).
func (e *ChatCronEngine) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultChatCronStopTimeout)
	defer cancel()
	if err := e.StopContext(ctx); err != nil {
		slog.Warn("scheduler.chat_cron.stop_drain_incomplete", "error", err.Error())
	}
}

// StopContext is Stop with an explicit bound on the wait for in-flight
// fires. It returns ctx.Err() if fires were still running when ctx
// ended. Idempotent: calling it on a stopped engine still waits for any
// fire left over from an earlier, timed-out stop.
func (e *ChatCronEngine) StopContext(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.c.Stop()
		e.started = false
		if e.fireCancel != nil {
			e.fireCancel()
		}
		for _, ent := range e.entries {
			if ent.kind == TriggerKindOnce && ent.timer != nil {
				ent.timer.Stop()
				ent.timer = nil
			}
		}
	}
	e.mu.Unlock()
	return e.fires.wait(ctx)
}

// beginFire registers one fire goroutine and returns its context, or
// ok=false when the engine is stopped (a cron tick or timer callback
// that raced Stop must not start). Registration happens under e.mu, so
// it is atomic with Stop's refusal: once Stop has released the lock,
// every fire it must wait for is already counted.
func (e *ChatCronEngine) beginFire() (context.Context, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.started {
		return nil, false
	}
	e.fires.add()
	return e.fireCtx, true
}

// Started reports whether Start has been called (and Stop has not
// subsequently been called). Used to assert that construction alone does
// not arm ticks — core/rpc.API.SetContext must call Start explicitly
// (mission model-scheduled-jobs-01PMSJ01 WP03's AC-003, first half: "an
// engine that is never Start()ed passes every unit test").
func (e *ChatCronEngine) Started() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.started
}

// Registered reports whether id currently has an armed cron entry. Used
// by tests to assert boot registration, SetEnabled arming/disarming and
// Delete's cron-side effect directly, instead of inferring state from a
// fire-timing window.
func (e *ChatCronEngine) Registered(id string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.entries[id]
	return ok
}

// dispatcher returns the current dispatcher under the read lock.
func (e *ChatCronEngine) dispatcherSnapshot() ChatRunDispatcher {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dispatch
}

// fire is the cron callback — runs in the cron goroutine, so it must not
// block the cron engine. Dispatches asynchronously.
func (e *ChatCronEngine) fire(id string) {
	ctx, ok := e.beginFire()
	if !ok {
		return
	}
	go func() {
		defer e.fires.done()
		_, _, _ = e.fireSync(ctx, id)
	}()
}

// fireOnce is the time.AfterFunc callback for a TriggerKindOnce row
// (model-scheduled-jobs-01PMSJ01 WP08, AC-010). time.AfterFunc already
// runs its function in its own goroutine, so — unlike fire above — no
// extra `go func` is needed here.
//
// A one-shot gets exactly one attempt: after fireSync returns (success
// OR failure — a failed attempt is still a used attempt, matching a
// cron row's own behaviour of not retrying a failed tick), the row is
// disabled via SetEnabled so a later boot's registerRecord does not
// re-arm it, and its in-memory registration is dropped so Registered(id)
// reports false immediately rather than waiting for the next Sync.
// RunAt is deliberately left untouched — the history row plus
// Enabled=false together are the durable proof it already fired, not a
// cleared RunAt (spec.md §5.7 "sets enabled = 0").
//
// A timer that fires after Stop does nothing: the entry stays registered
// and the next Start re-arms it (a past runAt fires immediately). The
// same holds for a fire that Stop cancelled before the dispatcher
// started the run (fireSync reports notDispatched): it records a
// "skipped" history row but is NOT used up, so the row stays enabled
// and fires on the next Start or boot. Once the run was dispatched, the
// attempt is used up even if Stop cancelled it — re-firing could repeat
// tool side effects.
func (e *ChatCronEngine) fireOnce(id string) {
	ctx, ok := e.beginFire()
	if !ok {
		return
	}
	defer e.fires.done()
	_, notDispatched, _ := e.fireSync(ctx, id)
	if notDispatched {
		return
	}
	if e.store != nil {
		// WithoutCancel: a dispatched attempt is used up even when Stop
		// cancelled it mid-run — otherwise the next boot would fire it
		// again.
		if err := e.store.SetEnabled(context.WithoutCancel(ctx), id, false); err != nil && !errors.Is(err, ErrChatRunNotFound) {
			slog.WarnContext(ctx, "scheduler.chat_cron.once_disable_failed",
				"chat_run_id", id, "error", err.Error())
		}
	}
	e.unregister(id)
}

// skippedAtStopReason is the history text for a fire that engine Stop
// (app quit) cancelled before its run started. A one-shot stays enabled
// and fires on the next start; a cron schedule simply waits for its next
// tick — it is not re-fired at start.
func skippedAtStopReason(kind string) string {
	if kind == TriggerKindOnce {
		return "The app was closing before this run started. It will run the next time the app starts."
	}
	return "The app was closing before this run started. It will run at its next scheduled time."
}

// fireSync dispatches one chat run and appends the outcome to history.
// Exported at package scope (lower-case: test-only via the scheduler_test
// package, which is in-package for this file's tests) so tests can await
// deterministic completion instead of racing the cron goroutine.
//
// A nil dispatcher — the state production wiring is in between WP03
// landing and WP05 arming it — records a "failed" history row naming the
// missing dispatcher. It never fabricates "completed" (FR-002): this is
// the honest-failure behaviour plan.md's WP03 section requires of a cron
// tick with no dispatcher wired.
//
// A Cedar denial (model-scheduled-jobs-01PMSJ01 WP09) is checked BEFORE
// the dispatcher is ever consulted — see the Cedar field's doc on
// ChatCronEngineConfig for why this call was missing entirely before
// WP09. For a created_by=="model" row this is fail-closed: no shipped
// or operator policy naming tool.scheduled_run.execute means deny, not
// default-allow (owner ruling B-3).
//
// ctx is the engine's fire context, which Stop cancels. The row reload,
// the Cedar gate and the history write run under WithoutCancel(ctx), so
// a fire that has begun always leaves a history row even when Stop lands
// mid-fire. notDispatched is true when Stop's cancel arrived before the
// dispatcher started the run (see RunStartReporter): nothing ran, the
// row records "skipped", and a one-shot is not used up.
func (e *ChatCronEngine) fireSync(ctx context.Context, id string) (hist ChatRunHistoryRecord, notDispatched bool, err error) {
	now := time.Now().UTC()
	if e.store == nil {
		return ChatRunHistoryRecord{}, false, ErrChatCronStoreUnavailable
	}
	bg := context.WithoutCancel(ctx)
	rec, err := e.store.Get(bg, id)
	if err != nil {
		// The row was deleted between the tick being scheduled and firing.
		// Nothing to record against — Delete already disarmed the entry;
		// this is a benign race, not a defect.
		return ChatRunHistoryRecord{}, false, err
	}

	createdBy := rec.CreatedBy
	if createdBy == "" {
		createdBy = ScheduledRunCreatedByUser
	}
	hasAllowlist := len(rec.ToolAllowlist) > 0

	if _, gerr := cedar.GateScheduledChatExecute(bg, e.cedar, id, createdBy, hasAllowlist); gerr != nil {
		ended := time.Now().UTC()
		hist = ChatRunHistoryRecord{
			Status:    "failed",
			StartedAt: now,
			EndedAt:   &ended,
			Error:     "denied by cedar policy: " + gerr.Error(),
		}
	} else if d := e.dispatcherSnapshot(); d != nil {
		job := Job{
			ID:   rec.ID,
			Kind: JobKindChatRun,
			ChatRun: &ChatRunSpec{
				ID:             rec.ID,
				PromptTemplate: rec.PromptTemplate,
				Model:          rec.Model,
				OutputSink:     rec.OutputSink,
				// model-harness-toolset-01MHTS001 WP02 (H-1): carry the
				// provenance and allowlist the gate above just evaluated,
				// so the dispatcher contains the run to it rather than
				// to nothing.
				CreatedBy:     createdBy,
				ToolAllowlist: append([]string(nil), rec.ToolAllowlist...),
			},
			Trigger: Trigger{Cron: rec.Cron, TZ: rec.Timezone},
		}
		if ctx.Err() != nil {
			// Stop landed before the dispatcher was called.
			notDispatched = true
		} else {
			dctx, marked := WithDispatchMark(ctx)
			hist, err = d.DispatchChatRun(dctx, job, now)
			if err != nil {
				ended := time.Now().UTC()
				hist = ChatRunHistoryRecord{
					Status:    "failed",
					StartedAt: now,
					EndedAt:   &ended,
					Error:     err.Error(),
				}
			}
			// Only a dispatcher that reports run starts can prove nothing
			// ran; for any other, having been called counts as dispatched.
			if _, reports := d.(RunStartReporter); reports && ctx.Err() != nil && !marked() {
				notDispatched = true
			}
		}
		if notDispatched {
			ended := time.Now().UTC()
			hist = ChatRunHistoryRecord{
				SessionID: hist.SessionID,
				Status:    ChatRunStatusSkipped,
				StartedAt: now,
				EndedAt:   &ended,
				Error:     skippedAtStopReason(rec.EffectiveTriggerKind()),
			}
		}
	} else {
		ended := now
		hist = ChatRunHistoryRecord{
			Status:    "failed",
			StartedAt: now,
			EndedAt:   &ended,
			Error:     "scheduler: no chat-run dispatcher wired",
		}
	}
	hist.ID = newChatHistoryID()
	hist.ChatRunID = id

	// bg (WithoutCancel): a fire that Stop cancelled still records its
	// outcome; Stop waits for this write.
	if perr := e.store.AppendHistory(bg, hist); perr != nil {
		slog.WarnContext(ctx, "scheduler.chat_cron.append_history_failed",
			"chat_run_id", id, "error", perr.Error())
	}
	return hist, notDispatched, nil
}

func newChatHistoryID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Compile-time witness that ChatCronEngine satisfies the Registrar
// interface core/rpc/views/scheduledchat expects.
var _ interface {
	Sync(ctx context.Context, id string) error
	Unregister(ctx context.Context, id string) error
} = (*ChatCronEngine)(nil)
