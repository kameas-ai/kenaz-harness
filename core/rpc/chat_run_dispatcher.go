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
	// Timeout overrides defaultDispatchTimeout. Zero uses the default.
	Timeout time.Duration
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

	// Step 5: append the rendered prompt as the user turn BEFORE calling
	// the LLM view's StartStream. Required: llmview.API.StartStream
	// derives its userMessage by scanning session history backwards for
	// the last user row (core/rpc/views/llm/impl.go) — the same contract
	// the interactive chat surface relies on (Sessions_AppendMessage then
	// LLM_StartStream). Without this append, StartStream sends an empty
	// prompt.
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
			if payload.SubID != subID {
				continue // another run's terminal event; keep waiting.
			}
			return d.buildRecord(ctx, sess.ID, now, payload), nil
		case <-deadline.C:
			return failedRecord2(sess.ID, now, fmt.Sprintf("timed out after %s waiting for the run to finish", d.deps.Timeout)), nil
		case <-ctx.Done():
			return failedRecord2(sess.ID, now, fmt.Sprintf("context cancelled while awaiting completion: %v", ctx.Err())), nil
		}
	}
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
