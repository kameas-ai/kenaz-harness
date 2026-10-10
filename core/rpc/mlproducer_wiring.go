package rpc

// mlproducer_wiring.go — the harness ML producer's composition
// (ml-producer-01MLPRD01 WP03; the fleet_usage_wiring.go pattern).
//
// core/mlproducer is fleet-free (scripts/ci/check-no-fleet-imports.sh):
// consent reaches it through mlproducer.ConsentSource and batches leave
// through mlproducer.Poster. This file implements both over core/fleet and
// the settings-owned fleet session, builds the one Recorder + ConsentGate
// + Shipper the process has, and hands their observer halves to the
// kernel (EnvDeps.ToolCalls), the chat runner (Config.TurnUsage), the
// session manager (delete observer), the subagent spawner (MLParent) and
// the bash tool (EnvProvider: the KENAZ_ACTOR / KENAZ_SESSION markers).
//
// Lifecycle:
//   - New:        construct; register observers + invalidation hooks; write
//                 <dataDir>/fleet/agent_pids.
//   - SetContext: start the gate's refresher; the shipper runs whenever the
//                 gate is open (gate OnChange → reconcile).
//   - sign-in / sign-out / node_removed (settings OnFleetSessionReset):
//                 purge the outbox (records never cross a fleet session —
//                 contract "do not cache across sessions"; a new session may
//                 be another account), bump the enrolment generation,
//                 re-read the gate. A closed gate pauses the shipper.
//   - org pause change / unpause / capability change: re-read the gate.
//   - Shutdown:   flush the recorder, stop the shipper, one final ship
//                 attempt bounded to 5 s, close the recorder, remove
//                 agent_pids.
//
// Served mode does the same as the desktop: the audit archiver is
// constructed whenever a non-nop fleet client exists and is started from
// SetContext / stopped from Shutdown, which cmd/harness-served calls too;
// this producer follows it. It records and ships only when a fleet
// identity is enrolled in that process and every gate condition holds.
//
// MCP server classification (spec §3.2): a server whose recipe is
// Source user / imported is user-named and ships as custom__h(server). An
// org-provisioned recipe (Source org) is NOT custom: its name is chosen
// and published by the org, not a person's private naming (WP03 decision).
// A server with no recipe in the merged catalog is treated as custom —
// failing private.

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// Shutdown bounds.
const (
	mlShutdownFlush = 2 * time.Second
	mlShutdownShip  = 5 * time.Second
)

// mlProducerWiring owns the process's ML producer.
type mlProducerWiring struct {
	dataDir  string
	settings *settings.API
	catalog  *recipes.MergedCatalog

	store   *mlstore.Store
	rec     *mlproducer.Recorder
	gate    *mlproducer.ConsentGate
	shipper *mlproducer.Shipper
	source  *fleetMLConsentSource

	// enrollGen moves whenever enrolment may have changed (a session
	// reset, a successful re-enroll after ml_node_not_enrolled).
	enrollGen atomic.Uint64

	lifeMu sync.Mutex
	appCtx context.Context
	shut   bool
}

// newMLProducerWiring builds the producer over c's database and data dir.
// nil (every accessor nil-safe) when there is no real DataDir, database or
// settings API — the rpc.New(nil) chassis.
func newMLProducerWiring(c *core.Core, settingsImpl *settings.API, catalog *recipes.MergedCatalog) *mlProducerWiring {
	if c == nil || settingsImpl == nil || c.DataDir() == "" {
		return nil
	}
	db := c.Storage()
	if db == nil {
		return nil
	}
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		logging.L().Warn("mlproducer.wiring.no_sql_handle")
		return nil
	}
	w := &mlProducerWiring{dataDir: c.DataDir(), settings: settingsImpl, catalog: catalog}
	w.store = mlstore.New(h.SQL())
	w.source = &fleetMLConsentSource{settings: settingsImpl}
	w.gate = mlproducer.NewConsentGate(mlproducer.GateConfig{
		Source: w.source,
		Purge:  w.purge,
		// Never block the evaluating goroutine (it may be the shipper's
		// own loop): reconcile runs on its own goroutine and re-reads the
		// gate's latest decision under a lock.
		OnChange: func(mlproducer.Decision) { go w.reconcile() },
		// WP05: every /me/ml read hands the org's newest exclusions to the
		// recorder before the decision is stored (DPA §4, on-device
		// matching). w.rec is assigned below, before anything can start
		// a gate read (Start runs from SetContext).
		OnExclusions: func(s *mlproducer.ExclusionSet) { w.rec.SetExclusions(s) },
	})
	w.source.invalidate = w.gate.Invalidate
	w.rec = mlproducer.NewRecorder(mlproducer.Config{
		Store:     w.store,
		Hasher:    mlproducer.NewHasher(c.DataDir()),
		Gate:      w.gate,
		Servers:   mlproducer.ServerClassifierFunc(w.isCustomServer),
		Workspace: c.WorkspaceDir,
	})
	w.shipper = mlproducer.NewShipper(mlproducer.ShipperConfig{
		Store:     w.store,
		Poster:    fleetMLPoster{settings: settingsImpl},
		Gate:      w.gate,
		ReEnroll:  w.reEnroll,
		EnrollGen: w.enrollGen.Load,
	})

	settingsImpl.OnFleetSessionReset(w.onSessionReset)
	settingsImpl.OnOrgUnpaused(w.gate.Invalidate)
	if cl := settingsImpl.FleetClientForBootstrap(); cl != nil {
		cl.OnOrgPauseChange(func(corefleet.OrgPauseStatus) { w.gate.Invalidate() })
	}
	if sm := c.SessionManager(); sm != nil {
		sm.AddDeleteObserver(w.rec.SessionDeleted)
	}
	settingsImpl.SetMLShippingStatusProvider(settings.MLShippingStatusFunc(w.shippingStatus))

	if err := mlproducer.WriteAgentPIDs(w.dataDir, os.Getpid()); err != nil {
		logging.L().Warn("mlproducer.agent_pids.write_failed", "err", err.Error())
	}
	return w
}

// ---- observer halves handed to the rest of New (all nil-safe) ----

// toolCallObserver is EnvDeps.ToolCalls (never a typed nil).
func (w *mlProducerWiring) toolCallObserver() coreag.ToolCallObserver {
	if w == nil || w.rec == nil {
		return nil
	}
	return w.rec
}

// turnObserver is the recorder's chat.TurnUsageObserver half.
func (w *mlProducerWiring) turnObserver() chat.TurnUsageObserver {
	if w == nil || w.rec == nil {
		return nil
	}
	return w.rec
}

// parentLinker is SubagentRunSpawnerDeps.MLParent.
func (w *mlProducerWiring) parentLinker() mlproducer.ParentLinker {
	if w == nil || w.rec == nil {
		return nil
	}
	return w.rec
}

// bashEnvProvider is bash.Options.EnvProvider: KENAZ_ACTOR=agent always,
// KENAZ_SESSION=h(root session) when the dispatch ctx names a session.
// The markers are set whatever the consent state: they tell the daemon
// what the AGENT did, which matters most when nothing is shipped from
// here. nil without a producer (no data dir).
func (w *mlProducerWiring) bashEnvProvider(sessionFromCtx func(context.Context) string) func(context.Context) []string {
	if w == nil || w.rec == nil {
		return nil
	}
	return w.rec.EnvProvider(sessionFromCtx)
}

// ---- lifecycle ----

// start runs the gate refresher on the app context; the shipper follows
// the gate. Idempotent.
func (w *mlProducerWiring) start(ctx context.Context) {
	if w == nil {
		return
	}
	w.lifeMu.Lock()
	if w.shut || w.appCtx != nil {
		w.lifeMu.Unlock()
		return
	}
	w.appCtx = ctx
	w.lifeMu.Unlock()
	w.gate.Start(ctx)
	logging.L().Info("mlproducer.started")
}

// reconcile starts the shipper when the gate is open and pauses it (with
// the gate's reason as the stop reason) when it is not.
func (w *mlProducerWiring) reconcile() {
	if w == nil {
		return
	}
	w.lifeMu.Lock()
	defer w.lifeMu.Unlock()
	if w.appCtx == nil || w.shut {
		return
	}
	if d := w.gate.Last(); d.Open {
		w.shipper.Start(w.appCtx)
	} else {
		w.shipper.Pause(d.Reason)
	}
}

// onSessionReset runs after sign-in, sign-out and node_removed.
func (w *mlProducerWiring) onSessionReset() {
	w.enrollGen.Add(1)
	if err := w.purge(context.Background()); err != nil {
		logging.L().Warn("mlproducer.session_reset.purge_failed", "err", err.Error())
	}
	w.gate.Invalidate()
}

func (w *mlProducerWiring) purge(ctx context.Context) error {
	if w == nil || w.rec == nil {
		return nil
	}
	return w.rec.Purge(ctx)
}

// reEnroll answers 403 ml_node_not_enrolled: the normal enroll path
// (POST /api/v1/nodes via FleetRefreshIdentity). Success moves the
// enrolment generation, which releases the shipper's hold.
func (w *mlProducerWiring) reEnroll(ctx context.Context) error {
	if _, err := w.settings.FleetRefreshIdentity(ctx); err != nil {
		return err
	}
	w.enrollGen.Add(1)
	w.gate.Invalidate()
	return nil
}

// shutdown drains: flush the recorder, stop the shipper, one bounded final
// ship, close the recorder, remove agent_pids. Idempotent.
func (w *mlProducerWiring) shutdown() {
	if w == nil {
		return
	}
	w.lifeMu.Lock()
	if w.shut {
		w.lifeMu.Unlock()
		return
	}
	w.shut = true
	w.lifeMu.Unlock()

	w.gate.Stop()
	fctx, fcancel := context.WithTimeout(context.Background(), mlShutdownFlush)
	_ = w.rec.Flush(fctx)
	fcancel()
	w.shipper.Stop()
	if w.gate.Last().Open {
		sctx, scancel := context.WithTimeout(context.Background(), mlShutdownShip)
		if err := w.shipper.ShipNow(sctx); err != nil {
			logging.L().Info("mlproducer.shutdown.final_ship_incomplete", "err", err.Error())
		}
		scancel()
	}
	w.rec.Close()
	if err := mlproducer.RemoveAgentPIDs(w.dataDir); err != nil {
		logging.L().Warn("mlproducer.agent_pids.remove_failed", "err", err.Error())
	}
}

// shippingStatus feeds Settings → Sync → Cloud ML. Never blocks on I/O.
func (w *mlProducerWiring) shippingStatus() settings.MLShippingStatusView {
	st := w.shipper.Status()
	v := settings.MLShippingStatusView{
		Accepted:   st.Accepted,
		Duplicates: st.Duplicates,
		Rejected:   st.Rejected,
		StopReason: st.StopReason,
	}
	if !st.LastBatchAt.IsZero() {
		v.LastBatchAt = st.LastBatchAt.UTC().Format(time.RFC3339)
	}
	if v.StopReason == "" && !st.Running {
		if d := w.gate.Last(); !d.Open && d.Reason != "" {
			v.StopReason = d.Reason
		}
	}
	return v
}

// isCustomServer: user / imported recipes and servers with no recipe are
// user-named (hashed on the wire); shipped, registry and org recipes are
// not.
func (w *mlProducerWiring) isCustomServer(server string) bool {
	if w == nil || w.catalog == nil {
		return true
	}
	r, ok := w.catalog.Get(server)
	if !ok {
		return true
	}
	return r.Source == recipes.SourceUser || r.Source == recipes.SourceImported
}

// ---- ConsentSource over core/fleet ----

// fleetMLConsentSource implements mlproducer.ConsentSource.
type fleetMLConsentSource struct {
	settings   *settings.API
	invalidate func()

	mu     sync.Mutex
	hooked *corefleet.CapabilityPoller
}

var _ mlproducer.ConsentSource = (*fleetMLConsentSource)(nil)

func (s *fleetMLConsentSource) client() *corefleet.Client {
	if s == nil || s.settings == nil || corefleet.Disabled() {
		return nil
	}
	c := s.settings.FleetClientForBootstrap()
	if c == nil || c.IsNop() {
		return nil
	}
	return c
}

// Identity: the enrolled identity first (in memory — a not-enrolled
// install never reaches the keychain here), then the token.
func (s *fleetMLConsentSource) Identity(ctx context.Context) (mlproducer.Identity, error) {
	c := s.client()
	if c == nil {
		return mlproducer.Identity{}, nil
	}
	_, nodeID, enrolled := s.settings.FleetEnrolledIdentity()
	if !enrolled {
		// Reported as signed in but not enrolled: before the boot enroll
		// lands we cannot tell signed-out from not-yet-enrolled without
		// the keychain, and both are transient closures.
		return mlproducer.Identity{SignedIn: true}, nil
	}
	signedIn, err := c.SignedIn(ctx)
	if err != nil {
		return mlproducer.Identity{}, err
	}
	id := mlproducer.Identity{SignedIn: signedIn, Enrolled: true, NodeID: nodeID}
	if signedIn {
		if tid, err := corefleet.TokenIdentityFromAccessToken(); err == nil {
			id.ResourceOrgID = tid.OrgID
		}
	}
	return id, nil
}

// capabilityFresh mirrors fleet.Capabilities.Has's 24 h TTL.
const capabilityFresh = 24 * time.Hour

// Capabilities reads the live poller's snapshot (no network) and the
// client's observed org pause. A missing or stale snapshot is unknown.
func (s *fleetMLConsentSource) Capabilities(context.Context) (mlproducer.CapabilityState, error) {
	p := s.settings.CapabilityPoller()
	if p == nil {
		return mlproducer.CapabilityState{}, mlproducer.ErrCapabilitiesUnknown
	}
	s.hookPoller(p)
	caps := p.Current()
	paused := caps.Paused
	if c := s.client(); c != nil && c.OrgPause().Paused {
		paused = true
	}
	if paused {
		return mlproducer.CapabilityState{Paused: true}, nil
	}
	if caps.FetchedAt.IsZero() || time.Since(caps.FetchedAt) >= capabilityFresh {
		return mlproducer.CapabilityState{}, mlproducer.ErrCapabilitiesUnknown
	}
	return mlproducer.CapabilityState{HostedInference: caps.Has(corefleet.CapHostedInference)}, nil
}

// hookPoller registers the gate's invalidation on the CURRENT poller. The
// settings API replaces its poller on sign-in, so this re-registers on
// each new instance (once per instance).
func (s *fleetMLConsentSource) hookPoller(p *corefleet.CapabilityPoller) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hooked == p || s.invalidate == nil {
		return
	}
	s.hooked = p
	inv := s.invalidate
	p.OnChange(func(corefleet.Capabilities) { inv() })
}

// MLConsent is a fresh GET /api/v1/me/ml folded through IsEffective, plus
// the org's typed exclusions from the same read (WP05). A malformed
// exclusions value fails GetMeML's strict decode, so the gate closes.
// legacy_exclusion_notes are deliberately not passed on: free text, never
// patterns (contract "Producers ignore them").
func (s *fleetMLConsentSource) MLConsent(ctx context.Context) (mlproducer.MLConsent, error) {
	c := s.client()
	if c == nil {
		return mlproducer.MLConsent{}, corefleet.ErrFleetDisabled
	}
	m, err := c.GetMeML(ctx)
	if err != nil {
		return mlproducer.MLConsent{}, err
	}
	return mlproducer.MLConsent{
		Effective: m.IsEffective(),
		Exclusions: mlproducer.Exclusions{
			Paths:          m.Exclusions.Paths,
			Commands:       m.Exclusions.Commands,
			ExcludeBrowser: m.Exclusions.ExcludeBrowser,
			Version:        m.ExclusionsVersion,
		},
	}, nil
}

// ---- Poster over fleet.Client.Post ----

// fleetMLPoster implements mlproducer.Poster. fleet.Client.Post brings
// token injection, the one-refresh 401 retry, 5xx retries and org_paused
// conversion.
type fleetMLPoster struct {
	settings *settings.API
}

var _ mlproducer.Poster = fleetMLPoster{}

func (p fleetMLPoster) PostLogs(ctx context.Context, body []byte) (mlproducer.PostResult, error) {
	if p.settings == nil || corefleet.Disabled() {
		return mlproducer.PostResult{}, corefleet.ErrFleetDisabled
	}
	c := p.settings.FleetClientForBootstrap()
	if c == nil || c.IsNop() {
		return mlproducer.PostResult{}, corefleet.ErrFleetDisabled
	}
	resp, err := c.Post(ctx, mlproducer.OTLPLogsPath, mlproducer.OTLPContentType, bytes.NewReader(body))
	if err != nil {
		if corefleet.IsOrgPaused(err) {
			return mlproducer.PostResult{}, fmt.Errorf("%w: %w", mlproducer.ErrOrgPaused, err)
		}
		return mlproducer.PostResult{}, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return mlproducer.PostResult{}, fmt.Errorf("mlproducer: read ingest response: %w", err)
	}
	return mlproducer.PostResult{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Body: raw}, nil
}

// ---- chat.TurnUsageObserver fan-out ----

// fanOutTurnUsage composes turn observers (fleet usage telemetry + the ML
// recorder). nil entries are dropped; nil when none remain, so the chat
// runner's `!= nil` guard holds (never a typed nil).
func fanOutTurnUsage(obs ...chat.TurnUsageObserver) chat.TurnUsageObserver {
	var live turnUsageFanOut
	for _, o := range obs {
		if o != nil {
			live = append(live, o)
		}
	}
	switch len(live) {
	case 0:
		return nil
	case 1:
		return live[0]
	}
	return live
}

type turnUsageFanOut []chat.TurnUsageObserver

func (f turnUsageFanOut) TurnStarted(ctx context.Context, sessionID, providerKind string) {
	for _, o := range f {
		o.TurnStarted(ctx, sessionID, providerKind)
	}
}

func (f turnUsageFanOut) TurnFailed(ctx context.Context, sessionID, failureKind string, recoverable bool) {
	for _, o := range f {
		o.TurnFailed(ctx, sessionID, failureKind, recoverable)
	}
}

func (f turnUsageFanOut) TurnEnded(ctx context.Context, sessionID, outcome string, modelCalls, toolCalls int, dur time.Duration) {
	for _, o := range f {
		o.TurnEnded(ctx, sessionID, outcome, modelCalls, toolCalls, dur)
	}
}
