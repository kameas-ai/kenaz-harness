// The production graphview.RunSpawner (mission
// subagent-control-and-background-tasks-01PMZB11, UNIT-6). Lives on the
// chassis side of the seam (core/rpc/views/agentgraph.RunSpawner) because
// it needs the LLM connector, the in-process event bus and the
// background-task registry; core/rpc/views/agentgraph must not import
// core/rpc.
//
// Design mirrors chat_run_dispatcher.go (model-scheduled-jobs-01PMSJ01
// WP04/WP05) as directed by this mission's spec.md §5.3: reuse the SAME
// production chat stack (llmview.API.StartStream, not a second one) and
// the SAME unattended posture (runposture.Unattended) so a spawned
// sub-agent's confirm_each / askOnAmbiguity / Cedar interactive-prompt
// gates resolve to deny-and-record instead of parking forever — a
// sub-agent has no UI to answer them, exactly like a scheduled run.
//
// The one structural difference from chat_run_dispatcher.go: by the time
// this spawner is invoked, BranchSeamAdapter.Fork has ALREADY created the
// child session and seeded the handoff prompt as its first user message
// (that's the whole point of routing through Fork rather than
// Sessions.Create + AppendMessage) — so this starts one step later than
// DispatchChatRun's step 4, and never touches SessionsAPI at all.
//
// Containment this spawner inherits and does NOT itself provide: the
// child session Fork created is an ORDINARY session
// (session.Manager.CreateInProject always stamps Kind = "chat"), so it is
// bound by the exact same toolloop.NewMergedResolver(staticPerms,
// sessionArm) every interactive session is bound by
// (harness-self-attach-01PMHS01 UNIT-4, unconditional at
// core/rpc/api.go's perms construction) — the full mcp_servers.json tool
// catalog, minus harness-self's harness_write_* tools. This spawner adds
// no additional narrowing of its own; it relies entirely on that
// pre-existing per-session containment plus the unattended posture below
// to keep a spawned run from parking on (or silently escaping) an
// interactive gate. See the commit body for what this does NOT cover
// (profile-level AllowedTools/DeniedTools — still not enforced, see
// core/agents.Profile's docs).
//
// BudgetTokens/BudgetTimeS ARE enforced as of owner directive
// 2026-09-09 (mission requirement 3), via BudgetOverrides below — see
// chat.SubagentBudgetRegistry and chat.applyProfileBudgetClamp for the
// clamp-not-override precedence against the dispatching session's
// autonomy-tier ceiling.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
)

// defaultSubagentSpawnTimeout bounds how long a spawned sub-agent run may
// execute before WaitForChildRun gives up on it and the background
// await-goroutine exits. Wider than chat_run_dispatcher.go's 10-minute
// scheduled-run timeout — a sub-agent worker may legitimately involve
// many more tool round trips than a single scheduled prompt — but still
// finite: an unbounded wait would leak the awaiting goroutine and the bus
// subscription forever for a run that never closes.
const defaultSubagentSpawnTimeout = 20 * time.Minute

// subagentStopTimeout bounds how long awaitSubagentRun's OWN timeout
// branch (case <-deadline.C below) waits for stopStream to return before
// giving up on the stop call itself and ending the task anyway.
//
// 5 seconds, matching two existing precedents for "how long to wait on a
// stop/cleanup call before treating it as stuck" in this codebase:
// tasks/registry.go's Abort PID-arm (SIGTERM, wait 5s, then SIGKILL —
// registry.go:490-498) and chat_runner.go's persistPartialTimeout (also
// 5s, bounding the checkpoint-flush calls this same stream's driveRun
// makes on its own exit path). Not invented — picked to match what's
// already sitting a few lines away from the two things this call touches.
//
// Scope decision (PR #315 review): this bounds ONLY the timeout branch
// below, which is what this PR introduces. Registry.Abort's OWN call to
// the same stopFunc (tasks/registry.go:505,
// `e.stopFunc(context.Background())`) stays unbounded — that call
// pre-dates this PR and is left asymmetric deliberately, not by
// oversight: Abort is user-initiated with a human watching the Tasks
// panel for the row to update, so an operator can notice a stuck Abort
// and escalate; a spawner timeout has nobody watching. Bounding Abort
// too is a defensible follow-up, not a silent gap — tracked, not done
// here, to keep this PR's blast radius to the path it's actually fixing.
const subagentStopTimeout = 5 * time.Second

// SubagentRunSpawnerDeps bundles the production seams
// NewSubagentRunSpawner needs. LLM and Bus are required; Tasks and
// DefaultProfile are nil-tolerant (best-effort visibility / a hard
// construction-time failure respectively — see NewSubagentRunSpawner's
// returned closure).
type SubagentRunSpawnerDeps struct {
	// LLM is the SAME production LLM connector the interactive chat
	// surface and the scheduled-run dispatcher use — StartStream is
	// dispatched through the one chat stack that exists, not a second
	// one built for sub-agent dispatch.
	LLM llmview.LLMConnectorAPI
	// Bus is the in-process EventBus the run's terminal
	// "llm:stream-closed" event lands on.
	Bus *EventBus
	// Tasks is the background-task registry
	// (subagent-control-and-background-tasks-01PMZB11 UNIT-3/UNIT-4).
	// Optional: nil means a spawned run gets no Tasks-panel visibility
	// and no background_task_complete hook fire, but still executes —
	// completion detection is the Bus subscription below, not the task
	// registry. When non-nil, every spawned run is registered as
	// tasks.KindSubagent and ended on completion, which is also what
	// fires background_task_complete for it (UNIT-4's HookFirer, wired
	// once on the registry, covers every kind including this one — no
	// second hook-firing path needed here).
	Tasks *coretasks.Registry
	// DefaultProfile resolves the LLM connector profile a spawned
	// sub-agent authenticates as. Lazy (called once per spawn, not
	// cached) so a profile added after boot is picked up without a
	// restart — same shape as ChatRunDispatcherDeps.DefaultProfile.
	// Returns "" when no profile is configured, which fails the spawn
	// (see below) rather than dispatching against an unspecified model.
	DefaultProfile func() string
	// Timeout overrides defaultSubagentSpawnTimeout. Zero uses the default.
	Timeout time.Duration

	// BudgetOverrides is the SAME registry instance wired into
	// chat.Config.SubagentBudgets (chat.ChatRunner.SubagentBudgets()) --
	// owner directive 2026-09-09, mission requirement 3. Set here, once
	// per spawn, before StartStream; StartStream reads it back keyed by
	// childSessionID. nil disables the clamp entirely (profile budgets
	// stay documented-but-unenforced, today's pre-existing behaviour).
	BudgetOverrides *chat.SubagentBudgetRegistry
}

// NewSubagentRunSpawner constructs the production graphview.RunSpawner.
// Wired into core/rpc/api.go's already-constructed BranchSeamAdapter via
// SetRunSpawner, once the LLM connector, event bus and task registry all
// exist (see the call site's comment for why that's late-bound rather
// than passed at construction).
func NewSubagentRunSpawner(deps SubagentRunSpawnerDeps) graphview.RunSpawner {
	if deps.Timeout <= 0 {
		deps.Timeout = defaultSubagentSpawnTimeout
	}
	return func(ctx context.Context, branchID, childSessionID string, req coreag.ForkRequest) (graphview.SpawnedRun, error) {
		if deps.Bus == nil {
			return graphview.SpawnedRun{}, errors.New("subagent_run_spawner: no event bus wired")
		}
		if deps.LLM == nil {
			return graphview.SpawnedRun{}, errors.New("subagent_run_spawner: no LLM connector wired")
		}
		profileID := ""
		if deps.DefaultProfile != nil {
			profileID = deps.DefaultProfile()
		}
		if profileID == "" {
			return graphview.SpawnedRun{}, errors.New("subagent_run_spawner: no default LLM profile configured")
		}

		log := logging.L()

		var taskID string
		if deps.Tasks != nil {
			id, terr := deps.Tasks.Register(ctx, coretasks.RegisterOpts{
				Kind:           coretasks.KindSubagent,
				OwnerSessionID: req.ParentSessionID,
				Description:    req.Title,
			})
			if terr != nil {
				log.Warn("rpc.subagent_run_spawner.task_register_err",
					"branch_id", branchID, "err", terr.Error())
			} else {
				taskID = id
			}
		}

		// Subscribe BEFORE starting the stream — same load-bearing
		// ordering as chat_run_dispatcher.go: a fast completion must not
		// race the subscription into existence. Topic-filtered to
		// "llm:stream-closed" ONLY so the per-token "llm:stream-chunk"
		// volume cannot fill this subscriber's channel and cause the
		// terminal event to be dropped by the bus's slow-subscriber
		// default: arm (core/rpc/bus.go).
		subCh, cancel := deps.Bus.Subscribe(64, "llm:stream-closed")

		// Record the profile's declared budget (if any) BEFORE
		// StartStream, keyed by the child session id StartStream will
		// use to look it up (owner directive 2026-09-09, mission
		// requirement 3). Must happen before StartStream, not after —
		// StartStream resolves env.Budget synchronously at call time.
		if deps.BudgetOverrides != nil && (req.BudgetTokens > 0 || req.BudgetTimeS > 0) {
			deps.BudgetOverrides.Set(childSessionID, chat.SubagentBudget{
				Tokens:        req.BudgetTokens,
				WallclockSecs: req.BudgetTimeS,
			})
		}

		// WP05's posture, reused here: a sub-agent run is unattended by
		// construction — it has no UI, so confirm_each / askOnAmbiguity /
		// Cedar's RequestInteractive must resolve to deny-and-record
		// instead of parking on a human who will never answer (see the
		// package doc's "Containment" note — this is what keeps an
		// interactive gate from either blocking the worker forever or
		// silently falling through to allow).
		subID, serr := deps.LLM.StartStream(runposture.Unattended(ctx), profileID, childSessionID, req.ModelID)
		if serr != nil {
			cancel()
			if taskID != "" {
				_ = deps.Tasks.End(context.Background(), taskID, -1)
			}
			return graphview.SpawnedRun{}, fmt.Errorf("subagent_run_spawner: start stream: %w", serr)
		}

		log.Info("rpc.subagent_run_spawner.started",
			"branch_id", branchID, "child_session_id", childSessionID,
			"sub_id", subID, "task_id", taskID)

		// stopStream is the ONE place that knows how to actually stop this
		// run's underlying LLM stream. It backs two independent call
		// sites below — the Tasks-panel Abort button (via SetStopFunc)
		// AND awaitSubagentRun's own timeout branch — so both paths that
		// end this run's life go through the same real cancellation, not
		// two divergent implementations of "stop". LLM.StopStream
		// (ChatRunner.StopStream) cancels the stream's context and blocks
		// on <-sub.done, which driveRun closes only after its goroutine
		// has fully unwound — so by the time stopStream returns, the run
		// is actually stopped, not merely asked to stop.
		stopStream := func(stopCtx context.Context) error {
			return deps.LLM.StopStream(stopCtx, subID)
		}

		// Wire the Tasks-panel Abort button to a real stop: without this,
		// tasks.Registry.Abort has no pid to kill for a KindSubagent task
		// (SetPID is never called on this path) and could only mark the
		// row cancelled while the LLM stream — and its token spend — kept
		// running until deps.Timeout (containment review of PR #307,
		// finding B2). SetStopFunc is the SetPID counterpart for a
		// stream-backed task; see its doc in core/tasks/registry.go.
		if taskID != "" {
			deps.Tasks.SetStopFunc(taskID, stopStream)
		}

		// The await goroutine must outlive this call: Fork (the caller)
		// returns to the tool's Call(), which — on the default async
		// dispatch path — returns to the model turn immediately. Detach
		// from ctx here the same way ChatRunner.StartStream re-derives
		// its own streamCtx from context.Background() one layer up, so a
		// cancelled inbound ctx can't cut this await short.
		done := make(chan error, 1)
		go awaitSubagentRun(subCh, cancel, subID, deps.Tasks, taskID, deps.Timeout, done, deps.BudgetOverrides, childSessionID, stopStream)

		return graphview.SpawnedRun{
			TaskID: taskID,
			Wait: func(waitCtx context.Context) error {
				select {
				case err := <-done:
					return err
				case <-waitCtx.Done():
					return waitCtx.Err()
				}
			},
		}, nil
	}
}

// awaitSubagentRun blocks on the bus's terminal event (or the timeout,
// whichever comes first), ends the task if one was registered — which is
// also what fires background_task_complete for it, via the HookFirer
// UNIT-4 wired onto taskReg once for every task kind — and reports the
// outcome on done. Runs in its own goroutine so Fork / the async dispatch
// path never blocks on it; CLAUDE.md's race-safe-fakes discipline doesn't
// apply here (no shared mutable state — done is the only cross-goroutine
// channel and is buffered 1), but the goroutine itself is exactly the
// kind -race is watching for, so keep the only shared value one buffered
// send.
//
// stopStream is called on the timeout branch (case <-deadline.C below).
// Before this, WaitForChildRun giving up on a stalled run only stopped
// WAITING for it — the LLM stream, its driver goroutine and its token
// spend all kept running past deps.Timeout, and taskReg.End then reported
// a terminal task status that was not true (containment finding: the
// timeout path never routed through the SAME stop mechanism the
// Tasks-panel Abort button already used — see SetStopFunc above).
// stopStream blocks until the run has actually stopped (see its doc at
// the call site), so by the time taskReg.End runs below, the reported
// terminal state is real.
func awaitSubagentRun(subCh <-chan BusEvent, cancel context.CancelFunc, subID string, taskReg *coretasks.Registry, taskID string, timeout time.Duration, done chan<- error, budgetOverrides *chat.SubagentBudgetRegistry, childSessionID string, stopStream func(context.Context) error) {
	defer cancel()
	// Clear the recorded profile budget once this run reaches a
	// terminal state (owner directive 2026-09-09, mission requirement
	// 3) so a long-lived harness process's history of sub-agent
	// dispatches doesn't leak SubagentBudgetRegistry entries forever.
	// Safe on a nil registry (Clear no-ops).
	defer budgetOverrides.Clear(childSessionID)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var outcome error
	exitCode := 0
waitLoop:
	for {
		select {
		case ev, ok := <-subCh:
			if !ok {
				outcome = errors.New("subagent_run_spawner: event bus subscription closed before a terminal event arrived")
				exitCode = -1
				break waitLoop
			}
			payload, ok := ev.Payload.(chat.StreamClosedPayload)
			if !ok || payload.SubID != subID {
				continue // not our shape, or another run's terminal event.
			}
			if payload.Reason != "completed" || payload.FinishReason == "paused" {
				exitCode = -1
				if payload.Message != "" {
					outcome = fmt.Errorf("subagent_run_spawner: run did not complete: %s", payload.Message)
				} else {
					outcome = fmt.Errorf("subagent_run_spawner: run did not complete: %s", payload.Reason)
				}
			}
			break waitLoop
		case <-deadline.C:
			outcome = fmt.Errorf("subagent_run_spawner: timed out after %s waiting for the run to finish", timeout)
			exitCode = -1
			// Actually stop the run — not merely stop waiting for it.
			// Bounded to subagentStopTimeout (PR #315 review): chat
			// runner's StopStream ignores the context it's handed (its
			// ctx parameter is `_`) and blocks unconditionally on
			// <-sub.done, so passing a context.WithTimeout to stopStream
			// alone would NOT bound this call by itself — the bound has
			// to be enforced from THIS side, via the select below. A
			// stopStream call that never returns must not wedge this
			// goroutine forever: that would trade "leaked stream" for
			// "leaked await-goroutine that never reports terminal" —
			// the same class of lie this unit exists to end, one layer
			// down. The task still reaches a terminal state below
			// either way; only the outcome message differs.
			if stopStream != nil {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), subagentStopTimeout)
				stopErrCh := make(chan error, 1)
				go func() { stopErrCh <- stopStream(stopCtx) }()
				select {
				case serr := <-stopErrCh:
					stopCancel()
					if serr != nil {
						logging.L().Warn("rpc.subagent_run_spawner.timeout_stop_err",
							"sub_id", subID, "task_id", taskID, "err", serr.Error())
					}
				case <-stopCtx.Done():
					// The stop call itself did not return within
					// subagentStopTimeout — a DIFFERENT failure than
					// "the run merely took too long to finish", and
					// worth its own log line and outcome message. This
					// costs no new vocabulary: outcome is already a
					// free-form error (every branch above formats its
					// own message), not a fixed enum, so distinguishing
					// here is just another message in the same style.
					// Task.Status/ExitCode are NOT extended with a new
					// value for this — that IS a fixed, cross-mission
					// contract (tasks.go's Task doc), and this PR does
					// not touch it: the task still ends up
					// StatusFailed/-1 below, same as any other
					// non-"completed" outcome. The background goroutine
					// above is intentionally left running past this
					// point (same detached-goroutine shape as
					// registry.go's SIGTERM→SIGKILL escalation) — it
					// will still call the real StopStream eventually if
					// the wedge clears; this call site just refuses to
					// wait on it any longer.
					stopCancel()
					logging.L().Warn("rpc.subagent_run_spawner.timeout_stop_wedged",
						"sub_id", subID, "task_id", taskID,
						"stop_timeout", subagentStopTimeout.String())
					outcome = fmt.Errorf("subagent_run_spawner: timed out after %s waiting for the run to finish, and the stop call itself did not return within %s", timeout, subagentStopTimeout)
				}
			}
			break waitLoop
		}
	}
	if taskReg != nil && taskID != "" {
		_ = taskReg.End(context.Background(), taskID, exitCode)
	}
	done <- outcome
}
