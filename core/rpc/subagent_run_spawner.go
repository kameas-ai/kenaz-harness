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
// (profile-level AllowedTools/BudgetTokens/BudgetTimeS).
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

		// Wire the Tasks-panel Abort button to a real stop: without this,
		// tasks.Registry.Abort has no pid to kill for a KindSubagent task
		// (SetPID is never called on this path) and could only mark the
		// row cancelled while the LLM stream — and its token spend — kept
		// running until deps.Timeout (containment review of PR #307,
		// finding B2). SetStopFunc is the SetPID counterpart for a
		// stream-backed task; see its doc in core/tasks/registry.go.
		if taskID != "" {
			deps.Tasks.SetStopFunc(taskID, func(stopCtx context.Context) error {
				return deps.LLM.StopStream(stopCtx, subID)
			})
		}

		// The await goroutine must outlive this call: Fork (the caller)
		// returns to the tool's Call(), which — on the default async
		// dispatch path — returns to the model turn immediately. Detach
		// from ctx here the same way ChatRunner.StartStream re-derives
		// its own streamCtx from context.Background() one layer up, so a
		// cancelled inbound ctx can't cut this await short.
		done := make(chan error, 1)
		go awaitSubagentRun(subCh, cancel, subID, deps.Tasks, taskID, deps.Timeout, done)

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
func awaitSubagentRun(subCh <-chan BusEvent, cancel context.CancelFunc, subID string, taskReg *coretasks.Registry, taskID string, timeout time.Duration, done chan<- error) {
	defer cancel()
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
			break waitLoop
		}
	}
	if taskReg != nil && taskID != "" {
		_ = taskReg.End(context.Background(), taskID, exitCode)
	}
	done <- outcome
}
