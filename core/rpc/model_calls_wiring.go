package rpc

// model_calls_wiring.go — llm-call-telemetry-01LLMTEL01 WP02: the local
// model-call store, its recorder, the retention pruner and the OpenRouter
// generation-stats pull, wired to the unified DB, the LLM registry's
// stats seam (FetchProviderStats) and the three Settings dials.
//
// Dials-to-consumer:
//   - ModelCallStatsPullDisabled → StatsPuller.Enabled (read per call and
//     per retry);
//   - ModelCallRetentionDays → Pruner (read on every pass);
//   - ShareModelCallTelemetryWithFleet → the Fleet span lane, applied by
//     the settings API itself (views/settings/fleet.go
//     applyModelCallSharing), not here.

import (
	"context"
	"flag"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/modelcalls"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// modelCallsWiring owns the live model-call pieces for one API.
type modelCallsWiring struct {
	store    *modelcalls.Store
	recorder *modelcalls.Recorder
	puller   *modelcalls.StatsPuller // nil when the registry has no stats seam
	pruner   *modelcalls.Pruner

	// dataDir roots the per-install session-hash key.
	dataDir         string
	restoreRecorder func()
	started         bool
}

// newModelCallsWiring builds the store and its workers over c's storage.
// It returns nil when there is no storage. Nothing is started here: see
// start.
//
// stats is the LLM registry's stats seam (nil = no stats pull).
// settingsImpl supplies the dials (nil = defaults: pull on, 30 days).
func newModelCallsWiring(c *core.Core, stats modelcalls.StatsSource, settingsImpl *settings.API) *modelCallsWiring {
	if c == nil {
		return nil
	}
	db := c.Storage()
	if db == nil {
		return nil
	}
	store, err := modelcalls.NewStore(db)
	if err != nil {
		logging.L().Warn("modelcalls.store_unavailable", "err", err.Error())
		return nil
	}
	w := &modelCallsWiring{
		dataDir:  c.DataDir(),
		store:    store,
		recorder: modelcalls.NewRecorder(store, logging.L()),
		pruner:   modelcalls.NewPruner(store, settingsImpl.ModelCallRetentionDays, logging.L()),
	}
	if stats != nil {
		w.puller = modelcalls.NewStatsPuller(modelcalls.StatsPullerOptions{
			Store:   store,
			Source:  stats,
			Enabled: settingsImpl.ModelCallStatsPullEnabled,
			Logger:  logging.L(),
		})
		w.recorder.OnStored = w.puller.Consider
	}
	return w
}

// start installs the per-install session-hash key (so kenaz.session.hash
// is stable across restarts and the store can find a session's earlier
// calls), launches the workers and installs the recorder as the process's
// genaitrace.Recorder. Idempotent.
func (w *modelCallsWiring) start() {
	if w == nil || w.started {
		return
	}
	w.started = true
	if w.dataDir != "" {
		if err := genaitrace.UseInstallSessionHashKey(w.dataDir); err != nil {
			// The per-process key stays in force: hashes are still keyed,
			// just not stable across a restart.
			logging.L().Warn("modelcalls.session_hash_key_unavailable", "err", err.Error())
		}
	}
	w.recorder.Start()
	w.pruner.Start()
	if w.puller != nil {
		w.puller.Start()
	}
	w.restoreRecorder = genaitrace.SetRecorder(w.recorder)
}

// startUnlessTesting starts the workers in a production process. The
// recorder is process-global (genaitrace.SetRecorder), and every rpc test
// boots its own API, so a test binary does not start it implicitly; tests
// that exercise it call start directly. flag.Lookup("test.v") rather than
// testing.Testing(): see the long note in New() on why core/rpc must not
// import "testing".
func (w *modelCallsWiring) startUnlessTesting() {
	if flag.Lookup("test.v") == nil {
		w.start()
	}
}

// stop uninstalls the recorder, drains it and stops the workers.
// Idempotent and nil-safe.
func (w *modelCallsWiring) stop() {
	if w == nil {
		return
	}
	if w.restoreRecorder != nil {
		w.restoreRecorder()
		w.restoreRecorder = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = w.recorder.Close(ctx)
	if w.puller != nil {
		w.puller.Stop()
	}
	w.pruner.Stop()
}

// ModelCallsForSession lists the session's most recent stored model calls
// (newest first) for the read-only "Model calls" view. The session id is
// hashed with the per-install key — the store never holds the raw id.
func (a *API) ModelCallsForSession(ctx context.Context, sessionID string, limit int) ([]modelcalls.CallView, error) {
	if a == nil || a.modelCalls == nil || sessionID == "" {
		return []modelcalls.CallView{}, nil
	}
	return a.modelCalls.store.ListBySessionHash(ctx, genaitrace.SessionHash(sessionID), limit)
}
