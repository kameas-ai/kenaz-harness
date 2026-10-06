// The real scheduler.ChatRunDispatcher (mission model-scheduled-jobs-
// 01PMSJ01, WP04). Lives on the chassis side of the seam
// (core/scheduler.ChatRunDispatcher) because it needs the sessions
// manager, the LLM view and the in-process event bus; core/scheduler must
// not import core/rpc.
//
// Design (spec.md §5.2): reload the row, render the prompt, create a
// session, append the rendered prompt as the user turn, start the stream
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
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
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
	// Sessions creates the headless session and appends the rendered
	// prompt as its opening user turn.
	Sessions sessionsview.SessionsAPI
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
	// Timeout overrides defaultDispatchTimeout. Zero uses the default.
	Timeout time.Duration
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

	// Step 4: create a headless session.
	if d.deps.Sessions == nil {
		return failedRecord(now, "no sessions API wired"), nil
	}
	sessionName := rec.Name
	if sessionName == "" {
		sessionName = "Scheduled chat run"
	}
	sess, cerr := d.deps.Sessions.Create(ctx, "Scheduled: "+sessionName)
	if cerr != nil {
		return failedRecord(now, fmt.Sprintf("create session: %v", cerr)), nil
	}
	// model-scheduled-jobs-01PMSJ01 WP06: record this session's origin
	// for the lifetime of the dispatch, so a filesystem-permission denial
	// mid-run (core/tools/fs.RecordingPrompter, wired at
	// builtins_wiring.go) can attribute its blocked_permission_requests
	// row to THIS scheduled run rather than defaulting to "interactive".
	// Cleared on every return path via defer — the run is over either
	// way once DispatchChatRun returns.
	if d.deps.Origins != nil {
		d.deps.Origins.Set(sess.ID, id)
		defer d.deps.Origins.Clear(sess.ID)
	}
	// WP02 (H-1): contain the session before its first turn. Released on
	// the terminal event of any stream in the session (below, and
	// releaseOnSessionTerminal after a timeout) — never at the timeout
	// itself, since the stream may still be executing (see
	// ScheduledRunContainmentRegistry's doc).
	if containment.Contained {
		d.deps.Containment.Contain(sess.ID, id, containment.Allow)
	}

	// Step 5: append the rendered prompt as the user turn BEFORE calling
	// the LLM view's StartStream. Required: llmview.API.StartStream
	// resolves the turn by scanning session history backwards for the
	// last user row (core/rpc/views/llm/impl.go) — the same contract the
	// interactive chat surface relies on (Sessions_AppendMessage then
	// LLM_StartStream). This append is the turn's ONLY write: the chat
	// runner never persists a user turn (chat-single-writer-01DOGF0G).
	// Without it, StartStream runs with no user message at all.
	if _, aerr := d.deps.Sessions.AppendMessage(ctx, sess.ID, "user", prompt); aerr != nil {
		return failedRecord(now, fmt.Sprintf("append prompt: %v", aerr)), nil
	}

	// Step 6: resolve the profile. rec.Model is a model OVERRIDE, not a
	// profile — spec.md §8 D-1.
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

	// Step 7: subscribe BEFORE starting the stream so a fast completion
	// cannot race the subscription into existence. Topic-filtered to
	// "llm:stream-closed" ONLY (spec.md §2's load-bearing implementation
	// constraint) — topic filtering happens before the bus's per-
	// subscriber buffer, so the high-volume "llm:stream-chunk" topic
	// cannot fill this subscriber's channel and cause the terminal event
	// to be dropped by the bus's slow-subscriber default: arm.
	subCh, cancel := d.deps.Bus.Subscribe(64, "llm:stream-closed")
	defer cancel()

	subID, serr := d.deps.LLM.StartStream(ctx, profileID, sess.ID, rec.Model)
	if serr != nil {
		// No stream started, so nothing can run under the containment.
		if containment.Contained {
			d.deps.Containment.Release(sess.ID)
		}
		return failedRecord2(sess.ID, now, fmt.Sprintf("start stream: %v", serr)), nil
	}

	log.Info("scheduler.chat_dispatch.started",
		"chat_run_id", id, "session_id", sess.ID, "sub_id", subID,
		"output_sink_kind", sinkKind, "output_sink_path", sinkPath)

	deadline := time.NewTimer(d.deps.Timeout)
	defer deadline.Stop()

	for {
		select {
		case ev, ok := <-subCh:
			if !ok {
				return failedRecord2(sess.ID, now, "event bus subscription closed before a terminal event arrived"), nil
			}
			payload, ok := ev.Payload.(chat.StreamClosedPayload)
			if !ok {
				// Not our shape (should not happen on this topic) — keep
				// waiting rather than misreport.
				continue
			}
			// Security review L4: containment is released on the terminal
			// event of ANY stream in this session, not only ours — a
			// key-rotation redrive (RedriveLastTurn) runs the turn under a
			// NEW sub id in the same session, stays contained while it
			// runs, and its end is the run's end.
			if containment.Contained && payload.SessionID == sess.ID {
				d.deps.Containment.Release(sess.ID)
			}
			if payload.SubID != subID {
				continue // another stream's terminal event; keep waiting.
			}
			histRec := d.buildRecord(ctx, sess.ID, now, payload)
			if sinkKind == "banner" {
				d.deliverBanner(id, rec.Name, sess.ID, histRec)
			}
			return histRec, nil
		case <-deadline.C:
			if containment.Contained {
				d.releaseOnSessionTerminal(sess.ID, subCh)
			}
			histRec := failedRecord2(sess.ID, now, fmt.Sprintf("timed out after %s waiting for the run to finish", d.deps.Timeout))
			if sinkKind == "banner" {
				d.deliverBanner(id, rec.Name, sess.ID, histRec)
			}
			return histRec, nil
		case <-ctx.Done():
			if containment.Contained {
				d.releaseOnSessionTerminal(sess.ID, subCh)
			}
			histRec := failedRecord2(sess.ID, now, fmt.Sprintf("context cancelled while awaiting completion: %v", ctx.Err()))
			if sinkKind == "banner" {
				d.deliverBanner(id, rec.Name, sess.ID, histRec)
			}
			return histRec, nil
		}
	}
}

// containmentWatchMax bounds how long releaseOnSessionTerminal waits. A
// session whose stream never terminates (an auth-paused turn nobody
// redrives) stays contained when the watch gives up — the fail-safe
// direction — and the watcher goroutine does not leak.
const containmentWatchMax = 24 * time.Hour

// releaseOnSessionTerminal is security review L4's fix for a run that
// timed out or was cancelled while its stream may still be executing: the
// containment stays in place NOW, and is released when ANY stream in the
// session terminates — the original stream finishing late, or a
// key-rotation redrive finishing. A user who later opens a timed-out
// "Scheduled:" session is therefore not left contained once the run is
// over, while anything still running as the scheduled run stays bound.
//
// The new subscription is taken BEFORE pending is drained, so a terminal
// event delivered between the caller's last read and this subscription is
// seen on pending, and one delivered afterwards is seen on the watcher.
func (d *LiveChatRunDispatcher) releaseOnSessionTerminal(sessionID string, pending <-chan BusEvent) {
	ch, cancel := d.deps.Bus.Subscribe(64, "llm:stream-closed")
	isTerminal := func(ev BusEvent) bool {
		p, ok := ev.Payload.(chat.StreamClosedPayload)
		return ok && p.SessionID == sessionID
	}
	for drained := false; !drained; {
		select {
		case ev, ok := <-pending:
			if !ok {
				drained = true
			} else if isTerminal(ev) {
				cancel()
				d.deps.Containment.Release(sessionID)
				return
			}
		default:
			drained = true
		}
	}
	go func() {
		defer cancel()
		timer := time.NewTimer(containmentWatchMax)
		defer timer.Stop()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if isTerminal(ev) {
					d.deps.Containment.Release(sessionID)
					return
				}
			case <-timer.C:
				return
			}
		}
	}()
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
// stream is drop-tolerant, history is not).
func (d *LiveChatRunDispatcher) buildRecord(ctx context.Context, sessionID string, startedAt time.Time, payload chat.StreamClosedPayload) scheduler.ChatRunHistoryRecord {
	ended := time.Now().UTC()
	rec := scheduler.ChatRunHistoryRecord{
		SessionID: sessionID,
		StartedAt: startedAt,
		EndedAt:   &ended,
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
		if snippet, ok := lastAssistantSnippet(ctx, d.deps.Sessions, sessionID); ok {
			rec.OutputSnippet = snippet
		}
	}
	return rec
}

// lastAssistantSnippet reads the persisted assistant reply back from
// session history (never from the stream — spec.md D-4), truncated to a
// reasonable snippet length.
func lastAssistantSnippet(ctx context.Context, sessionsAPI sessionsview.SessionsAPI, sessionID string) (string, bool) {
	msgs, err := sessionsAPI.ListMessages(ctx, sessionID)
	if err != nil {
		return "", false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" {
			const maxSnippet = 2000
			c := msgs[i].Content
			if len(c) > maxSnippet {
				c = c[:maxSnippet]
			}
			return c, true
		}
	}
	return "", false
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

// failedRecord2 is failedRecord plus a session id, for failures that
// occurred after the session was created.
func failedRecord2(sessionID string, now time.Time, reason string) scheduler.ChatRunHistoryRecord {
	r := failedRecord(now, reason)
	r.SessionID = sessionID
	return r
}

var _ scheduler.ChatRunDispatcher = (*LiveChatRunDispatcher)(nil)
