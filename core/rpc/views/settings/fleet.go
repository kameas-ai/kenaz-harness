package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// auditEmitter is the minimal interface compositeConfigApplier needs to
// record config-bundle-applied audit events (fleet-org-config-inheritance-
// 01NORGX01 WP05, FR-010). Mirrors the identical small local interface
// declared by core/rpc/views/catalog and core/rpc/views/cedar — each view
// package declares its own copy rather than depending on a shared emitter
// type, so this is consistent with the existing pattern, not a new one.
type auditEmitter interface {
	EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error
}

// fleetState holds the fleet client, dataDir, capability poller, config
// poller, and the emergency-lockdown watcher
// (fleet-emergency-lockdown-01NDFSEX12).
// It is attached to API after construction via SetFleetClient.
type fleetState struct {
	mu              sync.RWMutex
	client          *fleet.Client
	dataDir         string
	poller          *fleet.CapabilityPoller
	configPoller    *fleet.ConfigPoller
	lockdownWatcher *fleet.Watcher
	lockdownBroker  fleet.BrokerSink

	// cedarEngine is the Cedar policy engine wired at SetCedarEngine time.
	// Used by the composite ConfigApplier to apply team policy bundles.
	cedarEngine *cedarpolicy.Engine

	// skillStore + skillRegistry are wired at boot time via SetSkillRefs so the
	// compositeConfigApplier can call fleet.ApplyMandatedSkills when a bundle
	// carries the mandated_skills section (fleet-skills-sync-01NDFSEX18 WP05).
	// Both may be nil when fleet skill sync is not configured.
	skillStore    *slashcmd.SkillStore
	skillRegistry *slashcmd.Registry

	// otlpPipeline is the post-login OTLP export pipeline
	// (harness-fleet-otlp-export-01NTLMEX01). Set via SetFleetOTLPPipeline;
	// nil means OTLP export is not configured (OSS build / fleet disabled).
	otlpPipeline *fleet.FleetOTLPPipeline

	// telemetryRes is the startup OTel resource from telemetry.Init, held
	// so Activate can merge it with identity attrs.
	telemetryRes *resource.Resource

	// consent is the TelemetryConsent from the rpc API, used to gate
	// Activate: if consent == "none" the pipeline is not activated.
	consent *fleet.TelemetryConsent

	// tpFunc is a lazy accessor for the TracerProvider from telemetry.Init.
	// It is stored as a function rather than a pointer because telemetry.Init
	// runs inside c.Start() which fires AFTER rpc.New() constructs the fleet
	// state — a direct pointer captured at rpc.New time is always nil.
	// The function is invoked at Activate time (post-login, post-c.Start) so
	// it returns the real provider.
	// (harness-fleet-otlp-export-01NTLMEX01 tp-nil timing fix)
	tpFunc func() *sdktrace.TracerProvider

	// resFunc lazily resolves the startup OTel resource. telemetryRes is
	// captured at rpc.New time, before c.Start runs telemetry.Init, so in
	// production it is always nil; resFunc is consulted at activation time
	// instead, when the resource exists. Optional.
	resFunc func() *resource.Resource

	// usageTracker turns runtime turns / tool calls into the consent-gated
	// usage lifecycle (fleet.ConversationTracker over a fleet.UsageEmitter).
	// Built in SetFleetOTLPPipeline; nil when the pipeline is not wired.
	usageTracker *fleet.ConversationTracker

	// enrolled* is the identity of the last successful enroll, kept so
	// ReconcileTelemetry can (re)activate export after a consent change
	// without another enroll round trip. Cleared on sign-out.
	enrolledOrgID  string
	enrolledNodeID string
	enrolledTier   string
	enrolled       bool

	// telemetryOptIns is the per-class telemetry opt-in set last fetched from
	// the fleet store (harness-fleet-sync-activation-01NSYNC01 gap #4). The
	// fleet store is authoritative; this is the harness-side cache populated at
	// enroll. nil before the first successful fetch.
	telemetryOptIns []fleet.TelemetryOptInItem

	// optInPusher pushes the per-class opt-in vector a consent tier implies
	// (fleet.TierOptInUpdates) to the fleet store, wired via
	// SetTelemetryOptInPusher. Used here only to retry a push that failed in
	// a previous session — the "next app start" half of its retry contract
	// (see fleetEnroll). The tier-change half is handled directly by
	// core/rpc/views/fleet.Impl.SetTelemetryConsent, which holds its own
	// reference to the same pusher. nil on the test chassis / fleet-disabled
	// path.
	optInPusher *fleet.TelemetryOptInPusher

	// syncKindRegistry is the SyncKind registry wired at SetSyncKindRegistry
	// time (fleet-generic-sync-framework-01NSYNC02 WP02). Used by the
	// composite ConfigApplier to dispatch a bundle's org_config keyed
	// section to each entry's registered kind. nil when sync registration
	// has not run (fleet disabled) — the org_config branch skips cleanly.
	syncKindRegistry *fleet.KindRegistry

	// mcpCatalog is the shared *recipes.MergedCatalog wired at
	// SetMCPCatalog time (fleet-org-config-inheritance-01NORGX01 WP02).
	// Used by the compositeConfigApplier to install/clear the org-
	// provisioned recipe overlay when a bundle carries a provisioned_mcp
	// section. nil in the rpc.New(nil) test harness path — the
	// provisioned_mcp branch turns a missing catalog into a named apply
	// error rather than a silently-discarded org config (same posture as
	// the cedar_delta / mandated_skills "ref not wired" branches below).
	mcpCatalog *recipes.MergedCatalog

	// auditEmitter is wired at SetAuditEmitter time (fleet-org-config-
	// inheritance-01NORGX01 WP05, FR-010). Used by compositeConfigApplier
	// to record a fleet.config.applied event naming the bundle_id, org,
	// and provisioned recipe ids after a fully-clean ApplyBundle. nil
	// means audit events are silently dropped — the rpc.New(nil) test
	// harness path and any build where SetAuditEmitter is never called
	// (mirrors every other optional Set* field on this struct).
	auditEmitter auditEmitter
}

// SetFleetClient wires a fleet.Client into the API and starts the capability
// and config pollers. Called from rpc.New() during chassis boot. When not
// called, fleet methods return fleet.ErrFleetDisabled.
func (a *API) SetFleetClient(c *fleet.Client, dataDir string) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.client = c
	a.fleet.dataDir = dataDir
	// Wire the session-expired broker into the client if already set.
	if a.fleet.lockdownBroker != nil && c != nil {
		c.SetSessionBroker(a.fleet.lockdownBroker)
	}
	// Start the capability poller lazily. When c is a nop client the poller
	// will degrade gracefully on every Refresh call.
	//
	// Background network workers do not run under `go test`.
	//
	// The CapabilityPoller and the ConfigPoller both call fleet.LoadTokens ->
	// keyring.Get() — the CapabilityPoller does an immediate Refresh on
	// Start when its cache is empty/stale (which it always is for a
	// freshly-constructed test poller), so it hits the keychain even
	// sooner than the ConfigPoller's first ticker fire. go-keyring's mock
	// backend is a package-level global whose provider pointer AND whose
	// internal map are both unsynchronised, so a poller alive during a
	// test races any test touching the keyring. That surfaced for weeks
	// as "TestKeychainDelete/Set...: race detected", which reads like a
	// keychain flake and is not one — and blocked PR #342 on a real CI
	// run because only the ConfigPoller half of this had been guarded
	// (keyring-poller-race, WARNING: DATA RACE between
	// CapabilityPoller.Start -> fetch -> LoadTokens -> keyring.Get and any
	// sibling test's keyring.Set/keychainSet).
	//
	// Three earlier attempts fixed real but insufficient things:
	//   - t.Cleanup(api.Shutdown) at all 13 construction sites (v0.66.0)
	//   - making fleet.Syncer.Stop idempotent (a genuine latent panic)
	//   - hoisting keyring.MockInit() into TestMain
	// None could work. Cleanup runs when a test ENDS, but tests run in
	// PARALLEL — one test's poller is alive exactly while another test
	// touches the keyring. And MockInit hoisting removed the pointer race
	// only to expose the map race beneath it. The ConfigPoller below got
	// the right fix (not starting it in tests) but the CapabilityPoller a
	// few lines up did not, leaving this exact class of leak alive under
	// its own name.
	//
	// Neither poller has any business running in a unit test at all: both
	// are network workers on a ticker (or an immediate-fetch-then-ticker,
	// for the CapabilityPoller). Not starting them removes the whole class
	// rather than another instance of it. Production is unaffected —
	// testing.Testing() is false in the shipped binary.
	if a.fleet.poller == nil {
		p := fleet.NewCapabilityPoller(c, dataDir)
		a.fleet.poller = p
		if !testing.Testing() {
			p.Start(context.Background())
		}
	}
	if a.fleet.configPoller == nil && !testing.Testing() {
		applier := &compositeConfigApplier{state: a.fleet}
		cp := fleet.NewConfigPoller(c, dataDir, applier)
		a.fleet.configPoller = cp
		cp.Start(context.Background())
	}
	// Start the emergency-lockdown watcher. The watcher self-gates on
	// CapEmergencyLockdown so it exits immediately when the capability
	// is absent (fleet-emergency-lockdown-01NDFSEX12 WP02).
	if a.fleet.lockdownWatcher == nil && c != nil {
		w := fleet.NewWatcher(c, a.fleet.poller, a.fleet.lockdownBroker)
		a.fleet.lockdownWatcher = w
		w.Start(context.Background())
	}
}

// SetLockdownBroker wires the event broker into the fleet state so the
// lockdown Watcher can publish fleet:lockdown:changed events to the frontend.
// Also wires the same broker into the fleet Client for session-expired events
// (fleet-integrity-observability WP05 / FR-005).
// Must be called before SetFleetClient to take effect on first start; if called
// after, the watcher uses the broker on its next reconnect cycle.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (a *API) SetLockdownBroker(sink fleet.BrokerSink) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.lockdownBroker = sink
	// If the watcher is already running, update its broker reference.
	if a.fleet.lockdownWatcher != nil {
		a.fleet.lockdownWatcher.SetBroker(sink)
	}
	// Wire the broker into the fleet client for session-expired events (FR-005).
	if a.fleet.client != nil {
		a.fleet.client.SetSessionBroker(sink)
	}
}

// SetCedarEngine wires the Cedar policy engine into the fleet state so that
// the config poller can apply fleet-distributed policy bundles.
//
// Ordering does NOT matter, despite an earlier version of this comment
// claiming it must be called before SetFleetClient (fleet-enforcement-truth-
// 01PMZ505 WP03 re-verified this per spec §5.2 / §13 claim 2, and the claim
// was wrong): SetFleetClient's compositeConfigApplier holds a pointer to
// the shared fleetState, and ApplyBundle reads a.state.cedarEngine fresh
// (under a.state.mu) on every apply rather than capturing it at
// construction time. Call this whenever the engine becomes available; any
// bundle applied afterward — even the very next poll — sees it.
func (a *API) SetCedarEngine(engine *cedarpolicy.Engine) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.cedarEngine = engine
}

// SetFleetOTLPPipeline wires the post-login OTLP export pipeline into the
// fleet state. Call this after SetFleetClient, before FleetSignIn or
// FleetRefreshIdentity, so the pipeline is ready to Activate on the first
// enroll success.
//
//   - p:          the FleetOTLPPipeline constructed in core.New.
//   - startupRes: the OTel resource from telemetry.Telemetry.Resource; merged
//     with identity attrs at Activate time.
//   - tpFunc:     a lazy accessor for the TracerProvider from telemetry.Init.
//     Stored as a function rather than a direct pointer because telemetry.Init
//     runs inside c.Start() which fires AFTER rpc.New() — a pointer captured
//     at rpc.New time is always nil. The function is called at Activate time
//     (post-login, post-c.Start) so it returns the real provider.
//   - consent:    the TelemetryConsent instance (gates on EffectiveLevel).
//
// (harness-fleet-otlp-export-01NTLMEX01 wiring seam; tp-nil timing fix)
func (a *API) SetFleetOTLPPipeline(
	p *fleet.FleetOTLPPipeline,
	startupRes *resource.Resource,
	tpFunc func() *sdktrace.TracerProvider,
	consent *fleet.TelemetryConsent,
) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.otlpPipeline = p
	a.fleet.telemetryRes = startupRes
	a.fleet.tpFunc = tpFunc
	a.fleet.consent = consent

	// The usage lifecycle: one emitter (consent-routed, closed vocabulary)
	// and one tracker over it. A nil pipeline or consent yields a nil
	// tracker, which every call site treats as a no-op.
	if a.fleet.usageTracker != nil {
		a.fleet.usageTracker.Close()
		a.fleet.usageTracker = nil
	}
	if p != nil && consent != nil {
		tracker := fleet.NewConversationTracker(fleet.NewUsageEmitter(p, consent))
		a.fleet.usageTracker = tracker
		if tracker != nil && !testing.Testing() {
			// The idle janitor is a background ticker; like the pollers
			// above it has no business running inside a unit test.
			tracker.Start(context.Background())
		}
	}
	// A tier change can move EFFECTIVE consent (a downgrade fails closed,
	// an upgrade un-clamps a stored level), so re-evaluate export whenever
	// the capability snapshot changes.
	if a.fleet.poller != nil {
		a.fleet.poller.OnChange(func(fleet.Capabilities) {
			a.ReconcileTelemetry(context.Background())
		})
	}
}

// SetFleetTelemetryResourceFunc supplies a lazy accessor for the startup OTel
// resource (service.name / service.version). See fleetState.resFunc.
func (a *API) SetFleetTelemetryResourceFunc(fn func() *resource.Resource) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	a.fleet.resFunc = fn
	a.fleet.mu.Unlock()
}

// FlushFleetTelemetryForShutdown is the CLEAN-shutdown path: the session is
// still valid, so open conversation segments are ended (their totals are the
// most useful numbers the lifecycle produces) and both usage lanes are flushed
// before StopFleetBackground tears export down. Contrast sign-out, which
// discards. Bounded so a dead network cannot hold up process exit.
func (a *API) FlushFleetTelemetryForShutdown() {
	if a == nil || a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	pipeline := a.fleet.otlpPipeline
	tracker := a.fleet.usageTracker
	a.fleet.mu.RUnlock()
	if pipeline == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tracker.EndAll(ctx)
	tracker.Close()
	pipeline.Flush(ctx)
}

// FleetUsageTracker returns the usage lifecycle tracker, or nil when fleet
// telemetry is not wired. The runtime seams (agentgraph.ToolUsageObserver,
// chat.TurnUsageObserver, the chat UsageHook) resolve it through this
// accessor on every call rather than capturing it, because the pipeline is
// wired after the chat and graph stacks are constructed.
func (a *API) FleetUsageTracker() *fleet.ConversationTracker {
	if a == nil || a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.usageTracker
}

// FleetOrgTier returns the org tier the consent gate should clamp against.
//
// The capability poller is the primary source. It is not sufficient on its
// own: in a workbench the poller's first refresh runs during rpc.New, BEFORE
// the broker token source is installed, so it fails and backs off — and at
// enroll time, seconds later, the poller still reports no tier. Consent then
// clamps to "none" and export was skipped, permanently, with a debug line as
// the only trace. The enroll response carries the same server-asserted tier,
// so it is the fallback until the poller catches up.
func (a *API) FleetOrgTier() string {
	if a == nil || a.fleet == nil {
		return "free"
	}
	a.fleet.mu.RLock()
	poller := a.fleet.poller
	enrolledTier := a.fleet.enrolledTier
	a.fleet.mu.RUnlock()
	if poller != nil {
		if t := poller.Current().Tier; t != "" {
			return t
		}
	}
	if enrolledTier != "" {
		return enrolledTier
	}
	return "free"
}

// FleetTelemetryStatus returns a payload-free snapshot of the export pipeline
// for Settings and for diagnosis: whether it is active, the effective consent,
// and accepted/dropped/export counters. No identifiers, no bodies.
func (a *API) FleetTelemetryStatus(_ context.Context) (FleetTelemetryStatusView, error) {
	view := FleetTelemetryStatusView{EffectiveConsent: string(fleet.ConsentNone)}
	if a == nil || a.fleet == nil {
		return view, nil
	}
	a.fleet.mu.RLock()
	pipeline := a.fleet.otlpPipeline
	consent := a.fleet.consent
	tracker := a.fleet.usageTracker
	view.Enrolled = a.fleet.enrolled
	a.fleet.mu.RUnlock()
	if consent != nil {
		view.EffectiveConsent = string(consent.EffectiveLevel())
		view.StoredConsent = string(consent.Level())
	}
	view.OrgTier = a.FleetOrgTier()
	if pipeline != nil {
		view.Wired = true
		view.Pipeline = pipeline.Status()
	}
	view.OpenConversations = tracker.OpenSegments()
	return view, nil
}

// FleetTelemetryStatusView is the wire shape of FleetTelemetryStatus.
type FleetTelemetryStatusView struct {
	Wired             bool                 `json:"wired"`
	Enrolled          bool                 `json:"enrolled"`
	StoredConsent     string               `json:"stored_consent"`
	EffectiveConsent  string               `json:"effective_consent"`
	OrgTier           string               `json:"org_tier"`
	OpenConversations int                  `json:"open_conversations"`
	Pipeline          fleet.PipelineStatus `json:"pipeline"`
}

// SetTelemetryOptInPusher wires the tier→opt-ins pusher (fleet telemetry
// tier fix, core/fleet/telemetry_optins_pusher.go) so fleetEnroll can retry,
// at the next app start, a push that failed (or was never attempted, e.g.
// fleet was unreachable) in a previous session. Called from rpc.New()
// alongside the fleetview.Impl construction that holds the same pusher for
// the "next tier change" half of the retry contract.
func (a *API) SetTelemetryOptInPusher(p *fleet.TelemetryOptInPusher) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.optInPusher = p
}

// AdoptTelemetryOptIns caches an already-known per-class opt-in snapshot —
// e.g. one TelemetryOptInPusher just confirmed pushing — and feeds it to the
// OTLP log lane's narrowing snapshot, without a redundant GET round trip to
// the fleet store. Mirrors the tail of refreshTelemetryOptIns (cache +
// pipeline.SetTelemetryOptIns) but skips the fetch. Safe to call with nil
// items (e.g. to clear).
func (a *API) AdoptTelemetryOptIns(items []fleet.TelemetryOptInItem) {
	if a.fleet == nil {
		return
	}
	a.fleet.mu.Lock()
	a.fleet.telemetryOptIns = items
	pipeline := a.fleet.otlpPipeline
	a.fleet.mu.Unlock()
	if pipeline != nil {
		pipeline.SetTelemetryOptIns(items)
	}
}

// retryPendingTelemetryOptInPush re-attempts a tier-implied opt-in push that
// failed (or was skipped, e.g. fleet unreachable) in a previous session —
// the "next app start" half of TelemetryOptInPusher's retry contract. Called
// from fleetEnroll. Best-effort: errors are logged, not returned, so a
// pending sync issue never blocks sign-in/enroll.
func (a *API) retryPendingTelemetryOptInPush(ctx context.Context) {
	if a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	pusher := a.fleet.optInPusher
	consent := a.fleet.consent
	a.fleet.mu.RUnlock()
	if pusher == nil || consent == nil {
		return
	}
	if err := pusher.Reconcile(ctx, consent.Level()); err != nil {
		logging.L().Debug("fleet.telemetry_optins.reconcile.failed", "err", err.Error())
	}
}

// SetSkillRefs wires the fleet-skill store and slash registry into the fleet
// state so that the compositeConfigApplier can call fleet.ApplyMandatedSkills
// when a config bundle carries a mandated_skills section.
// (fleet-skills-sync-01NDFSEX18 WP05)
//
// Called from rpc.New() after both the skillStore and slashRegistry are
// constructed. Safe to skip — when nil, ApplyBundle silently skips the
// mandated_skills section.
func (a *API) SetSkillRefs(store *slashcmd.SkillStore, registry *slashcmd.Registry) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.skillStore = store
	a.fleet.skillRegistry = registry
}

// SetMCPCatalog wires the shared *recipes.MergedCatalog into the fleet
// state so the compositeConfigApplier can install org-provisioned recipes
// when a bundle carries a provisioned_mcp section
// (fleet-org-config-inheritance-01NORGX01 WP02).
//
// Called from rpc.New() after mergedCat is constructed. Safe to skip —
// when nil, ApplyBundle's provisioned_mcp branch turns a non-empty
// section into a named apply error (mirrors SetCedarEngine / SetSkillRefs
// never being called) rather than silently discarding a signed org
// config, per fleet-enforcement-truth-01PMZ505 WP02's "must not ack
// applied:true for a section this device cannot apply" rule.
func (a *API) SetMCPCatalog(cat *recipes.MergedCatalog) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.mcpCatalog = cat
}

// SetAuditEmitter wires the audit emitter into the fleet state so
// compositeConfigApplier can record a fleet.config.applied event on every
// fully-clean ApplyBundle (fleet-org-config-inheritance-01NORGX01 WP05,
// FR-010). Call anywhere relative to SetFleetClient — ApplyBundle reads
// a.state.auditEmitter fresh (under a.state.mu) on every call, same
// posture as SetCedarEngine. Safe to skip: nil means the event is never
// emitted (the pre-WP05 state for every build that predates this wiring).
func (a *API) SetAuditEmitter(em auditEmitter) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.auditEmitter = em
}

// SetSyncKindRegistry wires the SyncKind registry into the fleet state so
// the compositeConfigApplier can dispatch a bundle's org_config keyed
// section to each entry's registered kind
// (fleet-generic-sync-framework-01NSYNC02 WP02).
//
// Called from rpc.New() after registerSyncCategories (and, in whichever
// order, registerSlashCommandsSyncKind — both mutate the same *fleet.
// KindRegistry pointer in place, so call order relative to this setter does
// not matter; SetSyncKindRegistry only needs to run once with that pointer).
// Safe to skip — when nil, ApplyBundle's org_config branch logs and skips
// every entry rather than applying nothing silently as a false "success".
func (a *API) SetSyncKindRegistry(registry *fleet.KindRegistry) {
	if a.fleet == nil {
		a.fleet = &fleetState{}
	}
	a.fleet.mu.Lock()
	defer a.fleet.mu.Unlock()
	a.fleet.syncKindRegistry = registry
}

func (a *API) fleetClient() *fleet.Client {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.client
}

// FleetClientForBootstrap returns the fleet client for use in one-shot
// bootstrap operations (e.g. BootstrapLockdownStatus). Returns nil when
// fleet is not configured or the client is a nop.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (a *API) FleetClientForBootstrap() *fleet.Client {
	return a.fleetClient()
}

func (a *API) fleetDataDir() string {
	if a.fleet == nil {
		return ""
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.dataDir
}

func (a *API) fleetPoller() *fleet.CapabilityPoller {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.poller
}

// CapabilityPoller returns the fleet capability poller, or nil when fleet is
// not configured. Used by the rpc layer to resolve live capabilities at call
// time (fleet-skills-sync-01NDFSEX18 WP02).
func (a *API) CapabilityPoller() *fleet.CapabilityPoller {
	return a.fleetPoller()
}

// FleetSignIn kicks off the PKCE loopback OAuth flow. On success it
// calls FleetRefreshIdentity to populate the cached identity.
func (a *API) FleetSignIn(ctx context.Context) (FleetIdentity, error) {
	logging.L().Info("fleet.rpc.sign_in.start")
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		logging.L().Warn("fleet.rpc.sign_in.disabled", "client_nil", c == nil, "env_disabled", fleet.Disabled())
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	profile := fleet.ResolveProfile()
	if !profile.Configured() {
		logging.L().Warn("fleet.rpc.sign_in.profile_not_configured",
			"profile", profile.Name,
			"issuer", profile.ZitadelIssuer,
			"has_client_id", profile.NativeClientID != "",
			"fleet_base_url", profile.FleetBaseURL,
		)
		return FleetIdentity{}, fleet.ErrProfileNotConfigured
	}
	ts, err := fleet.DeviceCodeFlow(ctx, profile)
	if err != nil {
		logging.L().Error("fleet.rpc.sign_in.device_code_flow_failed", "err", err.Error())
		return FleetIdentity{}, err
	}
	if err := fleet.SaveTokens(ts); err != nil {
		logging.L().Error("fleet.rpc.sign_in.save_tokens_failed", "err", err.Error())
		return FleetIdentity{}, err
	}
	logging.L().Info("fleet.rpc.sign_in.tokens_saved")
	id, err := a.fleetEnroll(ctx)
	if err != nil {
		logging.L().Error("fleet.rpc.sign_in.enroll_failed", "err", err.Error())
		return id, err
	}
	logging.L().Info("fleet.rpc.sign_in.success",
		"org_id", id.OrgID,
		"team_id", id.TeamID,
		"email", id.Email,
		"tier", id.Tier,
		"roles", id.Roles,
	)
	return id, nil
}

// StopFleetBackground stops all fleet background goroutines (capability
// poller, config poller, lockdown watcher) and clears the in-memory
// lockdown flag + model-pref cache. It is idempotent and safe to call
// from sign-out or app shutdown.
//
// Callers that need to block until goroutines have exited should call
// CapabilityPoller.Stop(), ConfigPoller.Stop(), and Watcher.Stop() directly;
// this method calls them in series.
func (a *API) StopFleetBackground() {
	if a.fleet == nil {
		return
	}
	a.fleet.mu.Lock()
	poller := a.fleet.poller
	configPoller := a.fleet.configPoller
	watcher := a.fleet.lockdownWatcher
	// Nil them out so future Start calls in SetFleetClient create fresh instances.
	a.fleet.poller = nil
	a.fleet.configPoller = nil
	a.fleet.lockdownWatcher = nil
	// Clear in-memory caches that are session-scoped. Fleet model prefs
	// (fleet-enforcement-truth-01PMZ505 WP04) live in
	// core/rpc/views/llm's package-level store now, not here — clear
	// them there too, so a signed-out device does not keep enforcing an
	// allow-list or default model pushed before sign-out.
	llmview.ClearFleetModelPrefs()
	a.fleet.telemetryOptIns = nil
	a.fleet.enrolled = false
	a.fleet.enrolledOrgID = ""
	a.fleet.enrolledNodeID = ""
	a.fleet.enrolledTier = ""
	pipeline := a.fleet.otlpPipeline
	tracker := a.fleet.usageTracker
	mcpCatalog := a.fleet.mcpCatalog
	syncKindRegistry := a.fleet.syncKindRegistry
	a.fleet.mu.Unlock()

	// Clear the org-provisioned recipe overlay (fleet-org-config-
	// inheritance-01NORGX01 WP02 / spec §5's "removing fleet cleanly
	// reverts to local-only" success criterion). Outside the lock, same
	// as the OTLP pipeline clear below: SetOrgRecipes takes its own lock
	// on the catalog, not fleetState's.
	if mcpCatalog != nil {
		mcpCatalog.SetOrgRecipes(nil)
	}

	// Clear the generic org-provenance tracker (fleet-generic-sync-
	// framework-01NSYNC02 WP03) — the kind-agnostic counterpart to
	// SetOrgRecipes(nil) above. Without this, a signed-out device's
	// Settings → Sync surface (WP06) would keep reporting a kind as
	// "provisioned by your org" from a stale timestamp forever, even
	// though FR-008 requires org layers to drop cleanly on sign-out.
	if syncKindRegistry != nil {
		syncKindRegistry.ClearOrgProvenance()
	}

	// Drop the OTLP log lane's narrowing snapshot too: an empty snapshot
	// admits nothing, so a signed-out harness cannot keep exporting against a
	// stale opt-in set.
	if pipeline != nil {
		pipeline.SetTelemetryOptIns(nil)
		// ...and stop exporting. Clearing the opt-in snapshot narrows the
		// lanes to nothing, but the span processor, the providers and their
		// queues were left running under the signed-out identity. Deactivate
		// tears them down and DISCARDS what is queued; open conversation
		// segments are dropped, not reported, for the same reason.
		tracker.DropAll()
		pipeline.Deactivate(context.Background())
	}

	// Stop goroutines outside the lock.
	if poller != nil {
		poller.Stop()
	}
	if configPoller != nil {
		configPoller.Stop()
	}
	if watcher != nil {
		watcher.Stop()
	}
	// Clear the package-level lockdown flag so a re-login starts clean.
	fleet.ForceSetLockdownForTest(false) // production-safe: the symbol is exported for exactly this use
}

// FleetSignOut clears tokens and identity cache, and stops background
// goroutines. Returns an aggregated error when one or more keychain
// operations fail (sign-out is still treated as logically complete).
func (a *API) FleetSignOut(ctx context.Context) error {
	logging.L().Info("fleet.rpc.sign_out.start")
	if fleet.Disabled() {
		logging.L().Warn("fleet.rpc.sign_out.disabled_by_env")
		return fleet.ErrFleetDisabled
	}
	// Stop pollers + watcher + clear caches before removing tokens so
	// in-flight requests have a chance to complete.
	a.StopFleetBackground()

	// Aggregate keyring errors: a missing token is not an error on sign-out.
	var signOutErr error
	if err := fleet.ClearTokens(); err != nil {
		logging.L().Warn("fleet.rpc.sign_out.clear_tokens_partial", "err", err.Error())
		signOutErr = err // surface to caller; sign-out proceeds regardless
	}
	dataDir := a.fleetDataDir()
	if dataDir != "" {
		if err := os.Remove(fleet.IdentityFilePath(dataDir)); err != nil && !os.IsNotExist(err) {
			logging.L().Warn("fleet.rpc.sign_out.remove_identity_failed", "err", err.Error())
			signOutErr = errors.Join(signOutErr, fmt.Errorf("remove identity file: %w", err))
		}
	}
	if signOutErr != nil {
		logging.L().Warn("fleet.rpc.sign_out.partial_success", "err", signOutErr.Error())
	} else {
		logging.L().Info("fleet.rpc.sign_out.success")
	}
	return signOutErr
}

// FleetSignedIn reports whether a valid (non-expired) fleet session exists.
//
// FR-004: honors token expiry — a dead session (expired access + refresh)
// returns false so the UI can show a re-auth prompt rather than a fake
// "signed in" state. Uses Client.SignedIn for consistent expiry semantics.
func (a *API) FleetSignedIn(ctx context.Context) (bool, error) {
	if fleet.Disabled() {
		return false, nil
	}
	c := a.fleetClient()
	if c == nil {
		return false, nil
	}
	return c.SignedIn(ctx)
}

// FleetRefreshIdentity calls the fleet enroll endpoint.
func (a *API) FleetRefreshIdentity(ctx context.Context) (FleetIdentity, error) {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	return a.fleetEnroll(ctx)
}

// FleetProfile returns the active env profile info (no secrets).
func (a *API) FleetProfile(_ context.Context) (FleetProfileInfo, error) {
	if fleet.Disabled() {
		return FleetProfileInfo{}, fleet.ErrFleetDisabled
	}
	p := fleet.ResolveProfile()
	return FleetProfileInfo{
		Name:         p.Name,
		BadgeColor:   p.BadgeColor(),
		FleetBaseURL: p.FleetBaseURL,
		Configured:   p.Configured(),
	}, nil
}

// fleetEnroll is the shared enroll helper used by SignIn and RefreshIdentity.
func (a *API) fleetEnroll(ctx context.Context) (FleetIdentity, error) {
	c := a.fleetClient()
	if c == nil {
		logging.L().Warn("fleet.rpc.enroll.no_client")
		return FleetIdentity{}, fleet.ErrFleetDisabled
	}
	dataDir := a.fleetDataDir()
	nodeID, nodeIDErr := fleet.NodeID(dataDir)
	if nodeIDErr != nil {
		logging.L().Warn("fleet.rpc.enroll.node_id_error", "err", nodeIDErr.Error())
	}
	logging.L().Info("fleet.rpc.enroll.start",
		"node_id", nodeID,
		"platform", runtime.GOOS,
		"fleet_base_url", c.Profile().FleetBaseURL,
	)
	id, err := c.RefreshIdentity(ctx, nodeID, runtime.GOOS, "0.18.0")
	if err != nil {
		logging.L().Error("fleet.rpc.enroll.failed", "err", err.Error())
		return FleetIdentity{}, err
	}
	logging.L().Info("fleet.rpc.enroll.success",
		"org_id", id.OrgID,
		"team_id", id.TeamID,
		"email", id.Email,
		"tier", id.Tier,
	)

	// Remember who we enrolled as. ReconcileTelemetry needs the org + machine
	// ids to (re)activate export later — after a consent change, a tier
	// change, or a token renewal — without another enroll round trip, and
	// FleetOrgTier uses the tier until the capability poller has one.
	if a.fleet != nil {
		a.fleet.mu.Lock()
		a.fleet.enrolledOrgID = id.OrgID
		a.fleet.enrolledNodeID = nodeID
		a.fleet.enrolledTier = id.Tier
		a.fleet.enrolled = true
		a.fleet.mu.Unlock()
	}

	// Reconcile per-class telemetry opt-ins from the fleet store post-enroll
	// (harness-fleet-sync-activation-01NSYNC01 gap #4). The fleet store is the
	// source of truth for the seven classes (replacing local-only JSON). This
	// is best-effort and consent-gated: it caches the fleet-resolved opt-ins
	// but never relaxes the TelemetryConsent.EffectiveLevel export gate.
	//
	// Runs BEFORE activation so the lanes start already narrowed: the span
	// lane's class gate and the usage lanes' admission both read this
	// snapshot, and an Activate that precedes it would open with "nothing
	// admitted" and silently drop the first events of the session.
	a.refreshTelemetryOptIns(ctx)

	// Retry a tier-implied opt-in push that failed (or was never attempted)
	// in a previous session — the "next app start" half of
	// TelemetryOptInPusher's retry contract (fleet telemetry tier fix). Runs
	// after the GET above so a successful push's onPushed callback is the
	// last writer of the local cache, not the (possibly stale) GET.
	a.retryPendingTelemetryOptInPush(ctx)

	// Bring export in line with (identity, effective consent). This is the
	// post-login trigger point (FR-003): identity attrs are now known
	// (user.id = JWT sub, org.id = enroll org_id, machine.id = nodeID).
	a.ReconcileTelemetry(ctx)

	return fleetIdentityToView(id), nil
}

// refreshTelemetryOptIns fetches the per-class telemetry opt-in set from the
// fleet store and caches it on the fleet state. Best-effort: errors are logged
// at debug/warn and never fail the enroll flow. The cached set is exposed via
// FleetTelemetryOptIns for the settings UI.
func (a *API) refreshTelemetryOptIns(ctx context.Context) {
	c := a.fleetClient()
	if c == nil {
		return
	}
	items, err := c.GetTelemetryOptIns(ctx)
	if err != nil {
		// Unentitled / offline / signed-out → keep whatever is cached. Not fatal.
		logging.L().Debug("fleet.telemetry_optins.refresh.skipped", "err", err.Error())
		return
	}
	if a.fleet == nil {
		return
	}
	a.fleet.mu.Lock()
	a.fleet.telemetryOptIns = items
	pipeline := a.fleet.otlpPipeline
	a.fleet.mu.Unlock()

	// Feed the OTLP log lane its narrowing signal. This can only reduce what
	// the lane admits — the compiled ceiling in core/fleet/log_event_kind.go
	// bounds it from above and no server response can widen that.
	if pipeline != nil {
		pipeline.SetTelemetryOptIns(items)
	}
	logging.L().Info("fleet.telemetry_optins.refreshed", "classes", len(items))
}

// ReconcileTelemetry makes the fleet export pipeline match the current
// (signed-in identity, effective consent) — activating, re-activating under a
// new identity, or deactivating as needed. It is THE decision point; nothing
// else calls Activate or Deactivate.
//
// It is idempotent and cheap when nothing changed, so it is called liberally:
// after enroll, after a consent change, when the capability snapshot (tier)
// changes, on sign-out, and — in served mode — by the enroll supervisor on
// every auth-state notification and on a slow tick.
//
// Before this existed, Activate ran exactly once, inside enroll. A user who
// opted in after signing in got nothing until the next app start; a user who
// opted OUT kept exporting spans until then; and sign-out never stopped the
// exporters at all.
//
// Best-effort: failures are logged and leave export off (fail closed).
func (a *API) ReconcileTelemetry(ctx context.Context) {
	if a == nil || a.fleet == nil {
		return
	}
	a.fleet.mu.RLock()
	pipeline := a.fleet.otlpPipeline
	baseRes := a.fleet.telemetryRes
	resFunc := a.fleet.resFunc
	consent := a.fleet.consent
	tpFunc := a.fleet.tpFunc
	client := a.fleet.client
	tracker := a.fleet.usageTracker
	enrolled := a.fleet.enrolled
	orgID := a.fleet.enrolledOrgID
	nodeID := a.fleet.enrolledNodeID
	a.fleet.mu.RUnlock()

	if pipeline == nil {
		logging.L().Debug("fleet.otlp.reconcile.skipped", "reason", "pipeline_not_wired")
		return
	}

	// deactivate is the single "export must be off" path. Open conversation
	// segments are DROPPED, not ended: their totals were gathered under a
	// session or a consent that no longer stands.
	deactivate := func(reason string) {
		if pipeline.Active() {
			logging.L().Info("fleet.otlp.reconcile.deactivating", "reason", reason)
		}
		tracker.DropAll()
		pipeline.Deactivate(ctx)
	}

	// Consent gate: "none" (default) → no OTLP export (NFR-005 / FR-006).
	// EffectiveLevel clamps by org tier, so a downgraded account lands here.
	level := fleet.ConsentNone
	if consent != nil {
		level = consent.EffectiveLevel()
	}
	// The event (log) lane is open only under full consent. Aggregate opts
	// the count classes in so its counters pass; this switch is what keeps
	// those same classes from admitting log records.
	pipeline.SetLogLaneEnabled(level == fleet.ConsentFull)
	if level == fleet.ConsentNone {
		deactivate("consent_none")
		return
	}
	if !enrolled || orgID == "" {
		deactivate("not_enrolled")
		return
	}

	// kameas.user.id MUST equal the Zitadel JWT `sub` — the fleet OTLP
	// receiver (validateResourceAttrs) rejects with 401 otherwise. The
	// enroll response's user_id is the fleet-internal UUID, a DIFFERENT
	// identity namespace, so decode the sub from the access token instead.
	userID, subErr := fleet.SubjectFromAccessToken()
	if subErr != nil || userID == "" {
		// No token ⇒ signed out (or the broker session ended).
		deactivate("no_subject")
		return
	}

	// kameas.org.id likewise MUST equal the token's Zitadel resource-owner
	// claim, not the enroll response's org_id (Fleet's internal UUID — a
	// different namespace). Sending the enroll org_id got every batch refused
	// with 401 "kameas.org.id mismatch". No claim ⇒ Fleet cannot accept the
	// batch, so do not activate.
	zitadelOrgID, orgErr := fleet.ResourceOwnerFromAccessToken()
	if orgErr != nil || zitadelOrgID == "" {
		logging.L().Warn("fleet.otlp.reconcile.no_resource_owner_claim")
		deactivate("no_resource_owner_claim")
		return
	}

	want := fleet.IdentityAttrs{UserID: userID, OrgID: zitadelOrgID, MachineID: nodeID}
	if pipeline.Active() {
		if pipeline.ActiveIdentity() == want {
			return // already exporting as the right account
		}
		// The account changed under us (host signed out and in as someone
		// else). Whatever was gathered belongs to the previous account.
		logging.L().Info("fleet.otlp.reconcile.identity_changed")
		tracker.DropAll()
	}

	// OTLP ingest lives on the API host, which is discovered from
	// /config.json on the dashboard host — NOT on the dashboard host itself.
	// Deriving it from profile.FleetBaseURL pointed telemetry at CloudFront,
	// which returns 200 + index.html for any path, so exports "succeeded"
	// into a void. See fleet.OTLPBaseURL.
	if client == nil {
		logging.L().Debug("fleet.otlp.reconcile.skipped", "reason", "no_fleet_client")
		return
	}
	cfg, cfgErr := client.FleetConfig(ctx)
	if cfgErr != nil {
		logging.L().Warn("fleet.otlp.activate.api_host_unresolved", "err", cfgErr.Error())
		return
	}
	otlpBase := fleet.OTLPBaseURL(cfg)
	if otlpBase == "" {
		logging.L().Debug("fleet.otlp.reconcile.skipped", "reason", "no_api_base_url")
		return
	}

	// Resolve the TracerProvider and startup resource lazily. Both come from
	// telemetry.Init, which runs inside c.Start — after rpc.New captured
	// whatever it could. By activation time they exist.
	// (harness-fleet-otlp-export-01NTLMEX01 tp-nil timing fix)
	var tp *sdktrace.TracerProvider
	if tpFunc != nil {
		tp = tpFunc()
	}
	if baseRes == nil && resFunc != nil {
		baseRes = resFunc()
	}

	if err := pipeline.Activate(ctx, otlpBase, baseRes, want, fleet.DefaultBearerProvider(), tp); err != nil {
		logging.L().Warn("fleet.otlp.activate.failed", "err", err.Error())
	}
}

// fleetIdentityToView converts a fleet.Identity to the view type.
func fleetIdentityToView(id fleet.Identity) FleetIdentity {
	fi := FleetIdentity{
		UserID:      id.UserID,
		OrgID:       id.OrgID,
		TeamID:      id.TeamID,
		Email:       id.Email,
		DisplayName: id.DisplayName,
		Tier:        id.Tier,
		OrgName:     id.OrgName,
		TeamName:    id.TeamName,
	}
	if len(id.Roles) > 0 {
		fi.Roles = make([]string, len(id.Roles))
		copy(fi.Roles, id.Roles)
	}
	return fi
}

// FleetCapabilities returns the in-memory capability snapshot from the poller.
// When the poller is not running (fleet disabled / not wired) it returns an
// empty CapabilitiesView with source "default-deny".
func (a *API) FleetCapabilities(_ context.Context) (CapabilitiesView, error) {
	p := a.fleetPoller()
	if p == nil {
		return capabilitiesToView(fleet.DefaultDenyCapabilities()), nil
	}
	return capabilitiesToView(p.Current()), nil
}

// FleetRefreshCapabilities forces an immediate capability fetch from fleet.
// On success the in-memory snapshot and disk cache are updated. On error
// the last-known snapshot is returned alongside the error.
func (a *API) FleetRefreshCapabilities(ctx context.Context) (CapabilitiesView, error) {
	p := a.fleetPoller()
	if p == nil {
		return capabilitiesToView(fleet.DefaultDenyCapabilities()), fleet.ErrFleetDisabled
	}
	caps, err := p.Refresh(ctx)
	return capabilitiesToView(caps), err
}

// capabilitiesToView converts a fleet.Capabilities snapshot to the wire-safe
// CapabilitiesView, flattening the Capability typed keys to plain strings.
func capabilitiesToView(c fleet.Capabilities) CapabilitiesView {
	enabled := make(map[string]bool, len(c.Enabled))
	for k, v := range c.Enabled {
		enabled[string(k)] = v
	}
	fetchedAt := ""
	if !c.FetchedAt.IsZero() {
		fetchedAt = c.FetchedAt.UTC().Format(time.RFC3339)
	}
	return CapabilitiesView{
		Tier:      c.Tier,
		Enabled:   enabled,
		FetchedAt: fetchedAt,
		Source:    c.Source,
	}
}

// fleetConfigPoller returns the config poller, or nil when fleet is not wired.
func (a *API) fleetConfigPoller() *fleet.ConfigPoller {
	if a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.configPoller
}

// FleetConfigPullStatus returns the current config-pull poller state.
// Returns a zero-value view when fleet is disabled or the poller is not yet wired.
func (a *API) FleetConfigPullStatus(_ context.Context) (FleetConfigPullStatusView, error) {
	enabled := fleet.ConfigDistributionEnabled()
	p := a.fleetConfigPoller()
	if p == nil {
		return FleetConfigPullStatusView{
			Source:                    "default-deny",
			ConfigDistributionEnabled: enabled,
		}, nil
	}
	st := p.Status()
	return FleetConfigPullStatusView{
		LastAppliedID:             st.LastAppliedID,
		LastAppliedAt:             st.LastAppliedAt,
		LastError:                 st.LastError,
		Source:                    st.Source,
		BundleChecksum:            st.BundleChecksum,
		ConfigDistributionEnabled: enabled,
	}, nil
}

// FleetHealth returns a compact fleet-health summary for the global health
// indicator (WP10). It combines the signing-key presence, the config source,
// and the current session state into a single call so the header chip can
// render without multiple sequential RPCs.
func (a *API) FleetHealth(ctx context.Context) (FleetHealthView, error) {
	enabled := fleet.ConfigDistributionEnabled()

	// Config source + last error.
	var configSource, configLastError string
	if !enabled {
		configSource = "no-key"
	} else {
		p := a.fleetConfigPoller()
		if p == nil {
			configSource = "default-deny-degraded"
		} else {
			st := p.Status()
			configSource = st.Source
			configLastError = st.LastError
			if configSource == "default-deny" && configLastError == "" {
				configSource = "default-deny-degraded"
			}
		}
	}

	// Session state — delegate to FleetSignedIn/Client.SignedIn so Health uses
	// the same expiry semantics (tokenExpiryGrace + consistent no-ExpiresAt
	// handling) as the account UI, rather than a divergent inline check.
	signedIn, _ := a.FleetSignedIn(ctx)

	return FleetHealthView{
		ConfigDistributionEnabled: enabled,
		ConfigSource:              configSource,
		ConfigLastError:           configLastError,
		SignedIn:                  signedIn,
	}, nil
}

// ── compositeConfigApplier ───────────────────────────────────────────────────

// compositeConfigApplier implements fleet.ConfigApplier. It fans out each
// section of the bundle to the appropriate sub-system:
//   - cedar_delta     → cedarpolicy.Engine.SetTeamBundle
//   - mcp_allowlist   → recipes.ApplyFleetAllowlist
//   - model_prefs     → llmview.ApplyFleetModelPrefs (core/rpc/views/llm)
//   - provisioned_mcp → recipes.ApplyProvisionedMCP (core/mcp/recipes) —
//     fleet-org-config-inheritance-01NORGX01 WP02
//
// The kameas_ml_weight_urls bundle section is intentionally ignored: the
// fleet-hosted-LLM / kameas-ml surface was removed
// (harness-fleet-sync-activation-01NSYNC01, dead-code cleanup). The wire
// field is retained on Bundle so signature verification of server-signed
// bundles still round-trips, but it is no longer persisted or applied.
type compositeConfigApplier struct {
	state *fleetState
}

func (a *compositeConfigApplier) ApplyBundle(ctx context.Context, b *fleet.Bundle) []error {
	var errs []error
	// provisionedRecipeIDs accumulates the bundle's declared provisioned_mcp
	// recipe_ids for the closing audit event (WP05, FR-010) — populated in
	// the Provisioned MCP block below regardless of whether mcpCatalog is
	// wired, since these are the ids the ORG named in the bundle, not
	// necessarily every one that successfully installed.
	var provisionedRecipeIDs []string

	// Cedar delta.
	//
	// fleet-enforcement-truth-01PMZ505 WP02: a signed bundle carrying a
	// cedar_delta section that this device cannot apply (no engine wired)
	// must NOT ack "applied:true" — that is a silently-discarded team
	// policy, not a no-op (spec §1.1). The else branch is the fix: it
	// turns a skipped section into a named apply error so
	// config_pull.go's "len(applyErrs) == 0" success check goes false,
	// lastAppliedID does not advance, and the bundle is re-attempted on
	// the next poll instead of being reported as enforced.
	if len(b.CedarDelta) > 0 {
		a.state.mu.RLock()
		engine := a.state.cedarEngine
		a.state.mu.RUnlock()
		if engine != nil {
			if err := fleet.ApplyCedarDelta(ctx, engine, b.CedarDelta); err != nil {
				errs = append(errs, err)
			}
		} else {
			errs = append(errs, fmt.Errorf("fleet/config: cedar_delta present but no Cedar engine wired (SetCedarEngine never called)"))
		}
	}

	// MCP allow-list.
	if b.MCPAllowlist != nil {
		recipes.ApplyFleetAllowlist(b.MCPAllowlist)
	}

	// Model prefs.
	//
	// fleet-enforcement-truth-01PMZ505 WP04: DefaultModel and
	// ProviderAllowlist now reach real branches —
	// llmview.ApplyFleetModelPrefs installs them into the package-level
	// store core/rpc/views/llm.ListProviders (filters the list),
	// StartStream (blocks an excluded profile) and
	// profileKindAndModel (seeds DefaultModel, D-3) read. WP02's
	// stored-and-unread-with-an-error state is gone; this is the real
	// consumer, mirroring the mcp_allowlist section immediately above.
	if b.ModelPrefs != nil {
		llmview.ApplyFleetModelPrefs(b.ModelPrefs.DefaultModel, b.ModelPrefs.ProviderAllowlist)
	}

	// Weight URLs (kameas_ml_weight_urls): intentionally ignored — the
	// fleet-hosted-LLM / kameas-ml surface was removed. See the type doc above.

	// Provisioned MCP (fleet-org-config-inheritance-01NORGX01 WP02).
	//
	// Unlike cedar_delta/mandated_skills above, this section is applied
	// UNCONDITIONALLY on every ApplyBundle call, even when b.ProvisionedMCP
	// is empty/nil — Bundle.ProvisionedMCP's own doc records that nil, an
	// empty slice, and an absent key are all equivalent ("no org-
	// provisioned MCP entries"), which means the field carries the org's
	// CURRENT complete set on every successful bundle, not a delta. If we
	// only called ApplyProvisionedMCP when non-empty, an org that
	// de-provisions every entry (bundle N: 1 entry: bundle N+1: 0 entries)
	// would leave the stale bundle-N recipe permanently shadowing the
	// member's own catalog — exactly the "signed but the removal never
	// applies" shape this mission exists to close.
	//
	// A nil mcpCatalog (SetMCPCatalog never called) turns a NON-empty
	// section into a named apply error, same posture as cedar_delta/
	// mandated_skills above — but an EMPTY section with no catalog wired is
	// not an error: there is nothing to apply either way, and erroring on
	// every bundle for a fleet-disabled/test harness that never carries
	// provisioned_mcp would fail every apply for no operational reason.
	for _, e := range b.ProvisionedMCP {
		if e.RecipeID != "" {
			provisionedRecipeIDs = append(provisionedRecipeIDs, e.RecipeID)
		}
	}
	if a.state.mcpCatalog != nil {
		converted := make([]recipes.ProvisionedMCPEntry, 0, len(b.ProvisionedMCP))
		for _, e := range b.ProvisionedMCP {
			entry := recipes.ProvisionedMCPEntry{
				RecipeID:    e.RecipeID,
				Transport:   e.Transport,
				URL:         e.URL,
				PrimaryAuth: e.PrimaryAuth,
				Config:      e.Config,
			}
			if e.OAuth != nil {
				entry.OAuthClientID = e.OAuth.ClientID
				entry.OAuthScopes = e.OAuth.Scopes
			}
			converted = append(converted, entry)
		}
		for _, err := range recipes.ApplyProvisionedMCP(a.state.mcpCatalog, converted) {
			errs = append(errs, fmt.Errorf("fleet/config: provisioned_mcp: %w", err))
		}
	} else if len(b.ProvisionedMCP) > 0 {
		errs = append(errs, fmt.Errorf("fleet/config: provisioned_mcp present but no MCP catalog wired (SetMCPCatalog never called)"))
	}

	// Mandated skills (fleet-skills-sync-01NDFSEX18 WP05).
	// FR-012: all section errors are collected and returned so the ACK
	// carries the full set and the caller can decide not to advance lastAppliedID.
	//
	// fleet-enforcement-truth-01PMZ505 WP02: same fix as cedar_delta — a
	// bundle carrying mandated_skills that this device cannot apply (refs
	// not wired) must not ack clean.
	if len(b.MandatedSkills) > 0 {
		a.state.mu.RLock()
		skillStore := a.state.skillStore
		skillRegistry := a.state.skillRegistry
		a.state.mu.RUnlock()
		if skillStore != nil && skillRegistry != nil {
			skillErrs := fleet.ApplyMandatedSkills(skillStore, skillRegistry, b.MandatedSkills)
			for _, se := range skillErrs {
				logging.L().Warn("fleet.config.mandated_skills.apply_error", "err", se.Error())
				errs = append(errs, se)
			}
		} else {
			errs = append(errs, fmt.Errorf("fleet/config: mandated_skills present but skill refs not wired (SetSkillRefs never called)"))
		}
	}

	// Org config (fleet-generic-sync-framework-01NSYNC02 WP02).
	//
	// Each entry in the bundle's keyed org_config map dispatches to its
	// registered SyncKind's Apply(ctx, ScopeOrg, payload). Three distinct
	// "cannot apply" cases here get deliberately different treatment,
	// spelled out because they look similar and are not:
	//
	//   - registry is nil (sync registration never ran — fleet disabled,
	//     or SetSyncKindRegistry not yet called): every entry is skipped
	//     with a single log line, not an error. This mirrors the offline/
	//     fleet-disabled posture the whole config-pull path preserves
	//     elsewhere (registerSyncCategories itself no-ops the same way).
	//   - kind id has NO registration in this build (registry.Kind returns
	//     ok=false): a per-entry SKIP, not an error — spec §WP02's
	//     "unknown kind → logged skip, not fatal". This is the forward-
	//     compatibility case: a newer fleet server may ship an org_config
	//     kind an older harness build has never heard of, and treating
	//     that as a hard apply failure would block every OTHER section in
	//     the same bundle from advancing lastAppliedID on old binaries.
	//   - kind IS registered but doesn't declare ScopeOrg, or declares it
	//     but has a nil Apply: this is a real wiring gap (the kind
	//     promised org support the code doesn't back), not a forward-
	//     compat gap, so it gets the same error-not-skip treatment as
	//     cedar_delta/mandated_skills above — an ACK must not read
	//     "applied:true" for a section this device cannot actually apply.
	if len(b.OrgConfig) > 0 {
		a.state.mu.RLock()
		registry := a.state.syncKindRegistry
		a.state.mu.RUnlock()
		if registry == nil {
			logging.L().Warn("fleet.config.org_config.registry_unwired",
				"kind_count", len(b.OrgConfig))
		} else {
			// Deterministic order for logging/error-collection readability;
			// sorted map keys are already what json.Marshal produced on
			// the wire (see Bundle.OrgConfig's doc comment), but ranging a
			// Go map directly is not itself ordered, so sort explicitly.
			ids := make([]string, 0, len(b.OrgConfig))
			for id := range b.OrgConfig {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				payload := b.OrgConfig[id]
				kind, ok := registry.Kind(id)
				if !ok {
					logging.L().Warn("fleet.config.org_config.unknown_kind_skipped", "kind", id)
					continue
				}
				if !kind.HasScope(fleet.ScopeOrg) || kind.Apply == nil {
					errs = append(errs, fmt.Errorf(
						"fleet/config: org_config kind %q is registered but cannot apply an org-scope payload (HasScope(org)=%v, Apply nil=%v)",
						id, kind.HasScope(fleet.ScopeOrg), kind.Apply == nil))
					continue
				}
				// fleet-generic-sync-framework-01NSYNC02 WP06 (FR-006): the
				// org_config path dispatches straight to kind.Apply, bypassing
				// CategoryConfig()'s ScopeUser adapter (synckind.go) entirely
				// — so the secret-shape backstop wired there does not cover
				// this path unless it is also applied here. This is the
				// ScopeOrg half of the same central check.
				if kind.SecretPolicy == fleet.SecretPolicyMustNotContainSecrets {
					if reason := fleet.SecretShapeReason(payload); reason != "" {
						logging.L().Warn("fleet.config.org_config.secret_shaped_payload_refused", "kind", id, "reason", reason)
						errs = append(errs, fmt.Errorf("fleet/config: org_config kind %q: refusing a secret-shaped payload (%s)", id, reason))
						continue
					}
				}
				if err := kind.Apply(ctx, fleet.ScopeOrg, payload); err != nil {
					logging.L().Warn("fleet.config.org_config.apply_error", "kind", id, "err", err.Error())
					errs = append(errs, fmt.Errorf("fleet/config: org_config kind %q apply: %w", id, err))
					continue
				}
				// fleet-generic-sync-framework-01NSYNC02 WP03: record generic
				// org provenance ONLY on a successful apply — mirrors this
				// loop's own "an apply error must not read applied:true"
				// posture (see the doc comment above this loop). A kind that
				// failed to apply must not claim to be currently
				// org-provisioned.
				registry.MarkOrgApplied(id, time.Now())
			}
		}
	}

	// Audit (fleet-org-config-inheritance-01NORGX01 WP05, FR-010): record a
	// fleet.config.applied event naming the bundle_id, org, sections
	// present, and any provisioned recipe ids — but only on a FULLY clean
	// apply. KindFleetConfigApplied's own doc is explicit that it "fires
	// after a fleet config bundle has been fully verified and all sections
	// applied successfully"; a partial-failure bundle does not get to claim
	// that here (KindFleetConfigPartialFailure is the correct kind for that
	// case, and remains unwired — see docs/unwired-ledger.md).
	if len(errs) == 0 {
		a.emitConfigApplied(ctx, b, provisionedRecipeIDs)
	}

	// Return all errors (FR-012). An empty slice means full success.
	return errs
}

// emitConfigApplied records KindFleetConfigApplied for a fully-clean
// ApplyBundle (fleet-org-config-inheritance-01NORGX01 WP05, FR-010). No-op
// when no emitter is wired (nil is the default, safe posture — mirrors
// every other optional dependency on fleetState).
//
// Org identity is read best-effort from the on-disk fleet.Identity cache
// (populated at enroll/sign-in time); a missing or unreadable cache simply
// leaves OrgID/OrgName empty rather than failing the emission — this event
// is a record of what was applied, not a gate on applying it.
func (a *compositeConfigApplier) emitConfigApplied(ctx context.Context, b *fleet.Bundle, provisionedRecipeIDs []string) {
	a.state.mu.RLock()
	emitter := a.state.auditEmitter
	dataDir := a.state.dataDir
	a.state.mu.RUnlock()
	if emitter == nil {
		return
	}

	var sections []string
	if len(b.CedarDelta) > 0 {
		sections = append(sections, "cedar_delta")
	}
	if b.MCPAllowlist != nil {
		sections = append(sections, "mcp_allowlist")
	}
	if b.ModelPrefs != nil {
		sections = append(sections, "model_prefs")
	}
	if len(b.KameasMLWeightURLs) > 0 {
		sections = append(sections, "kameas_ml_weight_urls")
	}
	if len(b.ProvisionedMCP) > 0 {
		sections = append(sections, "provisioned_mcp")
	}
	if len(b.MandatedSkills) > 0 {
		sections = append(sections, "mandated_skills")
	}
	if len(b.OrgConfig) > 0 {
		sections = append(sections, "org_config")
	}

	var orgID, orgName string
	if dataDir != "" {
		if id, err := fleet.LoadIdentity(dataDir); err == nil {
			orgID = id.OrgID
			orgName = id.OrgName
		}
	}

	payload := contextaudit.FleetConfigAppliedPayload{
		BundleID:             b.BundleID,
		IssuedAt:             b.IssuedAt,
		Sections:             sections,
		OrgID:                orgID,
		OrgName:              orgName,
		ProvisionedRecipeIDs: provisionedRecipeIDs,
	}
	if err := emitter.EmitFleetEvent(ctx, contextaudit.KindFleetConfigApplied, payload); err != nil {
		logging.L().Warn("fleet.config.applied.audit_emit_failed", "err", err.Error())
	}
}

// LockdownStatusView is the wire shape returned by FleetLockdownStatus.
// The frontend's LockdownBanner uses this to render the reason text and
// the session composer uses Active to disable the chat input.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
type LockdownStatusView struct {
	// Active is true when a fleet-issued emergency lockdown is in effect.
	Active bool `json:"active"`
	// Reason is the admin-supplied reason string. Empty when Active is false
	// or when the watcher has not received a reason from the fleet server.
	Reason string `json:"reason,omitempty"`
}

// FleetLockdownStatus returns the current emergency lockdown state.
// This RPC is called by the frontend on mount (banner boot) and after
// receiving a fleet:lockdown:changed broker event.
// (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (a *API) FleetLockdownStatus(_ context.Context) (LockdownStatusView, error) {
	active := fleet.LockdownActive()
	return LockdownStatusView{Active: active, Reason: fleet.LockdownReason()}, nil
}

// ── Telemetry opt-ins (harness-fleet-sync-activation-01NSYNC01 gap #4) ─────────

// TelemetryOptInView is the wire-safe per-class opt-in record surfaced to the
// settings UI. Mirrors fleet.TelemetryOptInItem with an RFC3339 timestamp.
type TelemetryOptInView struct {
	Class   string `json:"class"`
	OptedIn bool   `json:"optedIn"`
	OptedAt string `json:"optedAt,omitempty"`
	Source  string `json:"source,omitempty"`
}

// FleetTelemetryOptIns returns the per-class telemetry opt-in set. On a cold
// read (cache empty) it fetches from the fleet store; otherwise it returns the
// cache populated at enroll. Returns fleet.ErrFleetDisabled when fleet is not
// wired. The set is the fleet store's view (the source of truth), not the
// local-only JSON.
func (a *API) FleetTelemetryOptIns(ctx context.Context) ([]TelemetryOptInView, error) {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return nil, fleet.ErrFleetDisabled
	}
	a.fleet.mu.RLock()
	cached := a.fleet.telemetryOptIns
	a.fleet.mu.RUnlock()
	if cached == nil {
		// Cold read: pull from the fleet store now.
		a.refreshTelemetryOptIns(ctx)
		a.fleet.mu.RLock()
		cached = a.fleet.telemetryOptIns
		a.fleet.mu.RUnlock()
	}
	return telemetryOptInsToView(cached), nil
}

// FleetSetTelemetryOptIn flips a single class opt-in in the fleet store
// (source becomes 'user_self' server-side) and refreshes the local cache.
// Consent-gated by tier server-side; the harness never bypasses the export
// consent gate. Returns fleet.ErrFleetDisabled when fleet is not wired.
func (a *API) FleetSetTelemetryOptIn(ctx context.Context, class string, optedIn bool) error {
	c := a.fleetClient()
	if c == nil || fleet.Disabled() {
		return fleet.ErrFleetDisabled
	}
	if err := c.PutTelemetryOptIns(ctx, []fleet.TelemetryOptInItem{
		{Class: class, OptedIn: optedIn},
	}); err != nil {
		return err
	}
	// Re-pull so the cache reflects the server-applied source/opted_at.
	a.refreshTelemetryOptIns(ctx)
	return nil
}

func telemetryOptInsToView(items []fleet.TelemetryOptInItem) []TelemetryOptInView {
	out := make([]TelemetryOptInView, 0, len(items))
	for _, it := range items {
		v := TelemetryOptInView{
			Class:   it.Class,
			OptedIn: it.OptedIn,
			Source:  it.Source,
		}
		if it.OptedAt != nil && !it.OptedAt.IsZero() {
			v.OptedAt = it.OptedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}
