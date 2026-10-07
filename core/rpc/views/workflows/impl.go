// Concrete WorkflowsAPI implementation backed by core/workflows.
package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
	wfcatalog "github.com/kameas-ai/kenaz-harness/core/workflows/catalog"
	wfsched "github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
)

// ErrEngineUnavailable is returned when the chassis booted without
// the workflows engine wired (e.g. test harness rpc.New(nil)).
var ErrEngineUnavailable = errors.New("workflows: engine unavailable")

// ErrFeatureDisabled is returned when HARNESS_WORKFLOWS=off.
var ErrFeatureDisabled = errors.New("workflows: feature disabled")

// ErrStorageUnavailable is returned by Save/Delete when the chassis
// booted without a Store wired (e.g. test harness without a DB).
var ErrStorageUnavailable = errors.New("workflows: storage unavailable")

// ErrInvalidSaveInput is returned by Save when neither YAML nor
// Workflow is populated on the SaveInput envelope.
var ErrInvalidSaveInput = errors.New("workflows: save requires yaml or workflow")

// ErrCedarDenied is returned when a cedar gate explicitly denies a
// workflow run / save / delete. The wrapped *cedar.PolicyDeniedError
// from the cedar gate is preserved via errors.Unwrap so callers can
// inspect Decision details.
var ErrCedarDenied = errors.New("workflows: denied by cedar policy")

// ErrSchedulerUnavailable is returned by ScheduleSet / ScheduleClear /
// ScheduleList / RunNow when no scheduler is wired into the chassis.
var ErrSchedulerUnavailable = errors.New("workflows: scheduler unavailable")

// ErrCatalogUnavailable is returned by Catalog_* methods when no
// catalog backend is wired into the chassis.
var ErrCatalogUnavailable = errors.New("workflows: catalog unavailable")

// ProgressPublisher is the interface the API uses to fan progress
// events onto the broker. Decoupled from rpc.StreamBroker so the
// view stays import-clean.
type ProgressPublisher interface {
	Publish(topic string, payload any)
}

// FrontendProgressEvent is the discriminated-union envelope the
// frontend workflowRunsStore expects on the `workflows:run-progress`
// broker topic. It mirrors the TypeScript RunProgressEvent interface
// in frontend/src/lib/workflowRunsStore.ts.
//
// The Go engine emits flat corewf.ProgressEvent values with a
// `status` field; translateProgressEvent maps those onto `phase`
// before the event reaches the broker so the frontend reducer can
// switch on `evt.phase` without needing a translation shim.
type FrontendProgressEvent struct {
	RunID        string `json:"runId"`
	WorkflowID   string `json:"workflowId"`
	WorkflowName string `json:"workflowName,omitempty"`
	// Phase is the discriminated union key the frontend reducer switches on.
	// Values: run_started | step_started | step_completed | step_failed |
	//         step_skipped | run_completed | run_failed.
	Phase    string `json:"phase"`
	StepName string `json:"stepName,omitempty"`
	StepKind string `json:"stepKind,omitempty"`
	Error    string `json:"error,omitempty"`
	// Ts is the ISO-8601 timestamp the frontend uses for startedAt / finishedAt.
	Ts     string `json:"ts"`
	Inline bool   `json:"inline,omitempty"`
}

// translateProgressEvent maps a flat corewf.ProgressEvent (emitted by
// the engine's step runner) onto a FrontendProgressEvent that matches
// the phase-discriminated shape the frontend reducer consumes.
//
// Status → Phase mapping:
//
//	"running"   → "step_started"  (step has started)
//	"succeeded" → "step_completed"
//	"failed"    → "step_failed"
//	"skipped"   → "step_skipped"
//
// Lifecycle events (run_started / run_completed / run_failed) are
// inferred from Step == "":
//
//	status "running"   + Step==""  → "run_started"
//	status "succeeded" + Step==""  → "run_completed"
//	status "failed"    + Step==""  → "run_failed"
func translateProgressEvent(ev corewf.ProgressEvent) FrontendProgressEvent {
	fe := FrontendProgressEvent{
		RunID:      ev.RunID,
		WorkflowID: ev.WorkflowID,
		StepName:   ev.Step,
		StepKind:   string(ev.Kind),
		Error:      ev.Err,
		Ts:         ev.At.UTC().Format(time.RFC3339Nano),
		Inline:     ev.Inline,
	}
	// Determine phase from the step name + status combination.
	isLifecycle := ev.Step == ""
	switch ev.Status {
	case "running":
		if isLifecycle {
			fe.Phase = "run_started"
		} else {
			fe.Phase = "step_started"
		}
	case "succeeded", "completed":
		if isLifecycle {
			fe.Phase = "run_completed"
		} else {
			fe.Phase = "step_completed"
		}
	case "failed":
		if isLifecycle {
			fe.Phase = "run_failed"
		} else {
			fe.Phase = "step_failed"
		}
	case "skipped":
		fe.Phase = "step_skipped"
	default:
		// Unknown status: emit as step_started so the sidebar at least
		// shows the step as active rather than silently dropping it.
		fe.Phase = "step_started"
	}
	return fe
}

// Config bundles the dependencies the impl needs.
type Config struct {
	Engine    *corewf.Engine
	Catalog   []corewf.Workflow
	Publisher ProgressPublisher
	Disabled  bool
	// Store is the WP06 persistence layer for user-defined workflows.
	// When nil, Save/Delete return ErrStorageUnavailable but the
	// in-memory builtin catalog still works for List/Get/Run.
	Store corewf.Store
	// Cedar is the policy gate consulted before Run / Save / Delete.
	// nil short-circuits to allow (default-allow posture).
	Cedar cedar.Gate
	// CedarMode is the mode context attribute the gate helpers pass to
	// the policy bundle ("permissive" | "strict"). "" defaults to
	// "permissive" inside the gate helpers.
	//
	// Prefer CedarModeFn in production wiring: a static string is read
	// once at construction, so flipping the dial would need an app
	// restart. This field stays for tests that pin one mode.
	CedarMode string
	// CedarModeFn resolves the mode context attribute per gate call, so
	// the strictness dial takes effect without re-creating the API.
	// When non-nil it wins over CedarMode; when it returns "" the gate
	// helpers coerce to "permissive".
	//
	// This is the producer for the `context.mode` attribute the shipped
	// Workflow-family bundle branches on. Before it existed the
	// bundle's strict arm was unreachable: nothing anywhere in the repo
	// assigned CedarMode outside tests.
	CedarModeFn func() string
	// Audit is the append-only event log emitter (workflows-01KQ8TDG
	// WP11). nil drops every event silently — acceptable in test
	// harnesses without an emitter wired.
	Audit audit.Emitter
	// Scheduler is the cron-scheduler surface (workflows-agentic-01KW2D3X
	// WP02). nil causes ScheduleSet / ScheduleClear / ScheduleList /
	// RunNow to return ErrSchedulerUnavailable.
	Scheduler wfsched.Scheduler
	// WorkflowCatalog is the browsable catalog backend (WP03). nil
	// causes Catalog_* methods to return ErrCatalogUnavailable.
	WorkflowCatalog wfcatalog.Catalog
	// Provenance records where each framework-installed workflow came from
	// (install-framework-01DOGF0B WP05 review H1/H2/H4) — the SAME store
	// the workflow catalog writes template installs to. InstallDocument
	// reads it to refuse collisions; Delete clears a deleted workflow's
	// record. nil: every existing row is a collision for InstallDocument
	// (no record can prove ownership — fail closed).
	Provenance corewf.ProvenanceStore
}

// API is the concrete WorkflowsAPI.
type API struct {
	cfg Config
	// installMu serialises InstallDocument's resolve → ownership check →
	// save → provenance write, so two concurrent installs of the same id
	// cannot both pass as "create" (re-review low 2).
	installMu sync.Mutex
	mu        sync.RWMutex
	byID map[string]corewf.Workflow
	// source tracks per-id provenance ("builtin" | "user") so List
	// surfaces the right tag in the catalog after a Save round-trip.
	source    map[string]string
	scheduler wfsched.Scheduler
	catalog   wfcatalog.Catalog
}

// cedarMode resolves the `mode` context attribute handed to the
// Workflow-family policy bundle. CedarModeFn (the live dial) wins over
// the static CedarMode; "" is left alone so the gate helpers apply
// their own "permissive" default in one place.
func (a *API) cedarMode() string {
	if a.cfg.CedarModeFn != nil {
		if m := a.cfg.CedarModeFn(); m != "" {
			return m
		}
	}
	return a.cfg.CedarMode
}

// New returns a real-engine-backed API. A nil engine returns a
// graceful-empty surface (List returns the catalog, Get/Run return
// ErrEngineUnavailable).
func New(cfg Config) *API {
	a := &API{
		cfg:       cfg,
		byID:      make(map[string]corewf.Workflow, len(cfg.Catalog)),
		source:    make(map[string]string, len(cfg.Catalog)),
		scheduler: cfg.Scheduler,
		catalog:   cfg.WorkflowCatalog,
	}
	for _, w := range cfg.Catalog {
		a.byID[w.ID] = w
		a.source[w.ID] = "builtin"
	}
	// Hydrate any user workflows that were persisted in prior sessions
	// so the catalog is complete on first List without a manual reload.
	// (FR-006) Load failures are now WARN-logged so a corrupt or missing
	// workflow store is diagnosable rather than silently swallowed.
	if cfg.Store != nil {
		if summaries, err := cfg.Store.List(context.Background()); err != nil {
			slog.Warn("workflows: failed to list persisted workflows; catalog may be incomplete",
				"error", err.Error(),
			)
		} else {
			// A persisted row wins over the builtin of the same id: it is
			// the user's installed (and possibly edited) copy. Before
			// install-framework-01DOGF0B WP05 the builtin shadowed it on
			// every restart — a template installed from the catalog read
			// back as "builtin" (not installed) after the next launch, and
			// edits to it were silently replaced by the shipped version.
			for _, s := range summaries {
				w, err := cfg.Store.Load(context.Background(), s.ID)
				if err != nil {
					slog.Warn("workflows: failed to load persisted workflow",
						"workflow_id", s.ID,
						"error",       err.Error(),
					)
					continue
				}
				a.byID[s.ID] = w
				a.source[s.ID] = "user"
			}
		}
	}
	return a
}

// List implements WorkflowsAPI.
func (a *API) List(_ context.Context) ([]Summary, error) {
	if a == nil || a.cfg.Disabled {
		return nil, ErrFeatureDisabled
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Summary, 0, len(a.byID))
	for _, w := range a.byID {
		src := a.source[w.ID]
		if src == "" {
			src = "builtin"
		}
		out = append(out, Summary{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			Version:     w.Version,
			StepCount:   len(w.Steps),
			Source:      src,
			OrgManaged:  a.isOrgMandated(w.ID),
		})
	}
	return out, nil
}

// Get implements WorkflowsAPI.
func (a *API) Get(_ context.Context, id string) (Workflow, error) {
	if a == nil || a.cfg.Disabled {
		return Workflow{}, ErrFeatureDisabled
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	w, ok := a.byID[id]
	if !ok {
		return Workflow{}, fmt.Errorf("%w: %s", corewf.ErrWorkflowNotFound, id)
	}
	return projectWorkflow(w), nil
}

// Run implements WorkflowsAPI. Thin shim over RunWithOptions for the
// pre-WP08 callers that don't need inline / skip-cache flags.
func (a *API) Run(ctx context.Context, id string, inputs map[string]string) (RunResult, error) {
	return a.RunWithOptions(ctx, RunRequest{ID: id, Inputs: inputs})
}

// RunWithOptions implements WorkflowsAPI. Honours the inline +
// skip-cache envelope flags introduced by WP08.
//
// Inline path:
//   - Resolves the workflow from the catalog.
//   - Calls workflows.InlineRun, which validates inline_run:true and
//     spins a goroutine that streams ProgressEvents (tagged Inline=true)
//     into the broker on the same `workflows:run-progress` topic.
//   - Returns synchronously after the inline goroutine drains; the
//     RunResult shape mirrors the spawned-session path so the chat
//     renderer doesn't have to fork.
//
// Spawned path (the default): unchanged from the WP07 baseline.
func (a *API) RunWithOptions(ctx context.Context, req RunRequest) (RunResult, error) {
	if a == nil || a.cfg.Disabled {
		return RunResult{}, ErrFeatureDisabled
	}
	if a.cfg.Engine == nil {
		return RunResult{}, ErrEngineUnavailable
	}
	a.mu.RLock()
	w, ok := a.byID[req.ID]
	a.mu.RUnlock()
	if !ok {
		return RunResult{}, fmt.Errorf("%w: %s", corewf.ErrWorkflowNotFound, req.ID)
	}

	// Cedar gate. Deny → typed ErrCedarDenied (wrapped policy error
	// available via errors.Unwrap). NotApplicable / Allow proceed.
	// Per WP11 spec, NotApplicable in strict mode SHOULD fire an
	// interactive prompt; the prompt registry is wired in a follow-up
	// task — for now we proceed (default-allow with audit) so the
	// chassis stays usable.
	if _, gerr := cedar.GateWorkflowRun(ctx, a.cfg.Cedar, w.ID, a.cedarMode(), corewf.CollectStepKinds(w)); gerr != nil {
		return RunResult{}, fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}

	typed := make(map[string]corewf.TypedValue, len(req.Inputs))
	for k, v := range req.Inputs {
		typed[k] = corewf.TypedValue{Type: corewf.ValueTypeText, Text: v}
	}

	pub := a.cfg.Publisher

	if req.Inline {
		// Inline dispatch: hand the workflow + loose-typed inputs to
		// InlineRun, drain the channel into the broker (so the chat
		// renderer sees the same event stream the spawned path emits),
		// and project the terminal events into a RunResult.
		loose := make(map[string]any, len(req.Inputs))
		for k, v := range req.Inputs {
			loose[k] = v
		}
		ch, err := corewf.InlineRun(ctx, a.cfg.Engine, w, loose, req.SessionID)
		if err != nil {
			return RunResult{}, err
		}
		res := RunResult{WorkflowID: w.ID, Status: "running"}
		stepIdx := make(map[string]int)
		for ev := range ch {
			if pub != nil {
				pub.Publish("workflows:run-progress", translateProgressEvent(ev))
			}
			res.RunID = ev.RunID
			i, ok := stepIdx[ev.Step]
			if !ok {
				i = len(res.Steps)
				res.Steps = append(res.Steps, StepRun{Name: ev.Step, Kind: string(ev.Kind)})
				stepIdx[ev.Step] = i
			}
			res.Steps[i].Status = ev.Status
			if ev.Output != "" {
				res.Steps[i].Output = ev.Output
			}
			if ev.Err != "" {
				res.Steps[i].Err = ev.Err
				res.Err = ev.Err
				res.Status = "failed"
			}
		}
		if res.Status == "running" {
			res.Status = "completed"
		}
		// Emit run-level lifecycle completion event for the inline path so
		// the Runs sidebar flips out of "running" after the inline run ends.
		if pub != nil && res.RunID != "" {
			finishPhase := "run_completed"
			if res.Status == "failed" {
				finishPhase = "run_failed"
			}
			pub.Publish("workflows:run-progress", FrontendProgressEvent{
				RunID:      res.RunID,
				WorkflowID: w.ID,
				Phase:      finishPhase,
				Error:      res.Err,
				Ts:         time.Now().UTC().Format(time.RFC3339Nano),
				Inline:     true,
			})
		}
		a.emitInlineAudit(ctx, w.ID, res)
		return res, nil
	}

	opts := corewf.RunOptions{SkipCache: req.SkipCache, ParentSessionID: req.SessionID}
	if pub != nil {
		opts.ProgressSink = func(ev corewf.ProgressEvent) {
			pub.Publish("workflows:run-progress", translateProgressEvent(ev))
		}
	}
	run, err := a.cfg.Engine.Run(ctx, w, typed, opts)
	res := RunResult{
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		Status:     run.Status,
		Steps:      make([]StepRun, 0, len(run.Steps)),
	}
	for _, s := range run.Steps {
		res.Steps = append(res.Steps, StepRun{
			Name: s.Name, Kind: string(s.Kind), Status: s.Status,
			Output: s.Output, Err: s.Err,
		})
	}
	if err != nil {
		res.Err = err.Error()
	}
	// Emit run-level lifecycle completion event so the Runs sidebar
	// always flips out of "running". The engine does not emit these
	// itself — per-step progress events are emitted via ProgressSink
	// above; we synthesise run_completed / run_failed here, after the
	// engine returns, so the frontend reducer can mark the run terminal.
	if pub != nil && run.ID != "" {
		finishPhase := "run_completed"
		if run.Status == "failed" || err != nil {
			finishPhase = "run_failed"
		}
		pub.Publish("workflows:run-progress", FrontendProgressEvent{
			RunID:      run.ID,
			WorkflowID: run.WorkflowID,
			Phase:      finishPhase,
			Error:      run.Err,
			Ts:         run.EndedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	// Emit completion + per-step failure audits. The engine's *Run
	// already carries both the workflow id and the step-level failure
	// status, so we hand it straight to the helpers.
	corewf.EmitExecuted(ctx, a.cfg.Audit, run)
	corewf.EmitStepFailures(ctx, a.cfg.Audit, run)
	return res, nil
}

// emitInlineAudit fires the workflow-executed + step-failed events for
// the inline-dispatch path. The inline path doesn't surface the engine's
// *Run, so we synthesise a minimal Run from the projected RunResult
// just for audit purposes — the audit payload only needs ids, kinds,
// and status, all of which the projection carries.
func (a *API) emitInlineAudit(ctx context.Context, workflowID string, res RunResult) {
	if a.cfg.Audit == nil {
		return
	}
	steps := make([]corewf.StepResult, 0, len(res.Steps))
	for _, s := range res.Steps {
		steps = append(steps, corewf.StepResult{
			Name:   s.Name,
			Kind:   corewf.StepKind(s.Kind),
			Status: s.Status,
			Err:    s.Err,
		})
	}
	r := &corewf.Run{
		ID:         res.RunID,
		WorkflowID: workflowID,
		Status:     res.Status,
		Steps:      steps,
		Err:        res.Err,
	}
	corewf.EmitExecuted(ctx, a.cfg.Audit, r)
	corewf.EmitStepFailures(ctx, a.cfg.Audit, r)
}

// Save implements WorkflowsAPI.
//
// Routing:
//   - in.YAML non-empty → ImportYAML (assigns fresh id) then Store.Save
//   - in.Workflow set    → reproject to corewf.Workflow + Store.Save
//     (caller-controlled id; Store dedupes on hash so repeated saves
//     of the same canonical YAML are no-ops)
//
// Either path validates via the storage layer, which calls Validate
// internally. The catalog cache is updated in-place so a follow-on
// List reflects the new record without a chassis restart.
func (a *API) Save(ctx context.Context, in SaveInput) (SaveOutput, error) {
	if a == nil || a.cfg.Disabled {
		return SaveOutput{}, ErrFeatureDisabled
	}
	if a.cfg.Store == nil {
		return SaveOutput{}, ErrStorageUnavailable
	}
	var (
		w   corewf.Workflow
		err error
	)
	switch {
	case in.YAML != "":
		w, err = corewf.ImportYAML([]byte(in.YAML))
		if err != nil {
			return SaveOutput{}, fmt.Errorf("workflows: import yaml: %w", err)
		}
	case in.Workflow != nil:
		w = unprojectWorkflow(*in.Workflow)
	default:
		return SaveOutput{}, ErrInvalidSaveInput
	}
	// An org-mandated workflow is the org's content (skill-library
	// 01SKLIB01, ledger 2026-10-06 item 4): the next bundle would silently
	// overwrite an edit anyway, so refuse it here instead of losing it.
	if a.isOrgMandated(w.ID) {
		return SaveOutput{}, fmt.Errorf("%w: %q", ErrWorkflowOrgManaged, w.ID)
	}
	// Cedar gate. Strict mode + shell-bearing workflows → deny here.
	if _, gerr := cedar.GateWorkflowSave(ctx, a.cfg.Cedar, w.ID, a.cedarMode(), corewf.CollectStepKinds(w)); gerr != nil {
		return SaveOutput{}, fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}
	saved, err := a.cfg.Store.Save(ctx, w)
	if err != nil {
		return SaveOutput{}, err
	}
	a.mu.Lock()
	a.byID[saved.ID] = saved
	a.source[saved.ID] = "user"
	a.mu.Unlock()
	corewf.EmitSaved(ctx, a.cfg.Audit, saved)
	return SaveOutput{
		ID:        saved.ID,
		Name:      saved.Name,
		Version:   saved.Version,
		Hash:      saved.Hash(),
		YAML:      saved.YAMLSource(),
		CreatedAt: saved.CreatedAt().Format("2006-01-02T15:04:05.000Z07:00"),
		UpdatedAt: saved.UpdatedAt().Format("2006-01-02T15:04:05.000Z07:00"),
	}, nil
}

// Delete implements WorkflowsAPI. Returns corewf.ErrWorkflowNotFound
// when the id is unknown so the frontend can render the gone-state
// without an error toast.
func (a *API) Delete(ctx context.Context, id string) error {
	if a == nil || a.cfg.Disabled {
		return ErrFeatureDisabled
	}
	if a.cfg.Store == nil {
		return ErrStorageUnavailable
	}
	// Review F7: an org-mandated workflow is not the user's to delete —
	// the org's next bundle (withdrawing the mandate) removes it. Mirrors
	// slashcmd.ErrSkillOrgManaged for mandated skills.
	if a.isOrgMandated(id) {
		return fmt.Errorf("%w: %q", ErrWorkflowOrgManaged, id)
	}
	return a.deleteWorkflow(ctx, id)
}

// ErrWorkflowOrgManaged: the workflow is required by the user's org
// (installed from the bundle's mandated_items). It cannot be deleted,
// edited, rescheduled or unscheduled here — the org's bundle owns it
// (review F7; skill-library-01SKLIB01 extended the guard from delete to
// save + schedule, ledger 2026-10-06 conformance residual item 4).
var ErrWorkflowOrgManaged = errors.New("workflows: this workflow is required by your org and is managed by its config, so it cannot be changed here")

// isOrgMandated reports whether id's install provenance is an org mandate.
// An unreadable provenance file reads as "not mandated" here, matching
// Delete's existing posture (the collision checks fail closed on their own).
func (a *API) isOrgMandated(id string) bool {
	if a == nil || a.cfg.Provenance == nil || id == "" {
		return false
	}
	p, ok, err := a.cfg.Provenance.Get(id)
	return err == nil && ok && p.Source == corewf.ProvenanceMandated
}

// deleteWorkflow is Delete without the org-managed guard — the mandate
// reconciliation path (RemoveMandatedDocument) is the one caller allowed
// to remove a mandated workflow.
func (a *API) deleteWorkflow(ctx context.Context, id string) error {
	// Probe the store first so the user-facing surface returns a typed
	// not-found rather than a silent ok. The storage Delete is idempotent
	// on missing rows, which is the wrong contract for the RPC.
	if _, err := a.cfg.Store.Load(ctx, id); err != nil {
		return err
	}
	// Cedar gate. The default policy permits delete unconditionally so
	// the chassis can always emit `workflow.deleted` audit; strict
	// overrides layer a forbid rule on top.
	if _, gerr := cedar.GateWorkflowDelete(ctx, a.cfg.Cedar, id); gerr != nil {
		return fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}
	if err := a.cfg.Store.Delete(ctx, id); err != nil {
		return err
	}
	if a.cfg.Provenance != nil {
		// A deleted workflow's install record goes with it, so a later
		// workflow reusing the id is never mistaken for the old install.
		_ = a.cfg.Provenance.Remove(id)
	}
	a.mu.Lock()
	delete(a.byID, id)
	delete(a.source, id)
	a.mu.Unlock()
	corewf.EmitDeleted(ctx, a.cfg.Audit, id)
	return nil
}

// unprojectWorkflow lifts the wire Workflow back into a corewf.Workflow
// the storage layer can persist. It only copies the fields the wire
// shape carries; the wider corewf.Workflow surface (cwd, env, etc.) is
// left zero so the canonical re-marshal in Store.Save produces a
// minimal YAML payload.
func unprojectWorkflow(w Workflow) corewf.Workflow {
	out := corewf.Workflow{
		ID:          w.ID,
		Name:        w.Name,
		Description: w.Description,
		Version:     w.Version,
		Inputs:      make([]corewf.Input, 0, len(w.Inputs)),
		Steps:       make([]corewf.Step, 0, len(w.Steps)),
	}
	for _, in := range w.Inputs {
		out.Inputs = append(out.Inputs, corewf.Input{
			Name: in.Name, Kind: corewf.InputKind(in.Kind),
			Required: in.Required, Default: in.Default, Options: in.Options,
		})
	}
	for _, st := range w.Steps {
		out.Steps = append(out.Steps, corewf.Step{
			Name: st.Name, Kind: corewf.StepKind(st.Kind), InputsFrom: st.InputsFrom,
			UserPrompt: st.UserPrompt, Cmd: st.Cmd, Args: st.Args,
			Method: st.Method, URL: st.URL, Mode: st.Mode,
		})
	}
	return out
}

// ScheduleSet implements WorkflowsAPI.
func (a *API) ScheduleSet(ctx context.Context, in ScheduleSetInput) error {
	if a == nil || a.cfg.Disabled {
		return ErrFeatureDisabled
	}
	if a.scheduler == nil {
		return ErrSchedulerUnavailable
	}
	if a.isOrgMandated(in.WorkflowID) {
		return fmt.Errorf("%w: %q", ErrWorkflowOrgManaged, in.WorkflowID)
	}
	return a.scheduler.Register(ctx, in.WorkflowID, in.Cron, in.Timezone)
}

// ScheduleClear implements WorkflowsAPI.
func (a *API) ScheduleClear(ctx context.Context, workflowID string) error {
	if a == nil || a.cfg.Disabled {
		return ErrFeatureDisabled
	}
	if a.scheduler == nil {
		return ErrSchedulerUnavailable
	}
	if a.isOrgMandated(workflowID) {
		return fmt.Errorf("%w: %q", ErrWorkflowOrgManaged, workflowID)
	}
	return a.scheduler.Unregister(ctx, workflowID)
}

// ScheduleList implements WorkflowsAPI.
func (a *API) ScheduleList(ctx context.Context) ([]ScheduleEntry, error) {
	if a == nil || a.cfg.Disabled {
		return nil, ErrFeatureDisabled
	}
	if a.scheduler == nil {
		return nil, ErrSchedulerUnavailable
	}
	internal, err := a.scheduler.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduleEntry, 0, len(internal))
	for _, e := range internal {
		out = append(out, ScheduleEntry{
			WorkflowID: e.WorkflowID,
			Cron:       e.Cron,
			Timezone:   e.Timezone,
			Enabled:    e.Enabled,
		})
	}
	return out, nil
}

// RunNow implements WorkflowsAPI.
func (a *API) RunNow(ctx context.Context, workflowID string) (RunSummary, error) {
	if a == nil || a.cfg.Disabled {
		return RunSummary{}, ErrFeatureDisabled
	}
	if a.scheduler == nil {
		return RunSummary{}, ErrSchedulerUnavailable
	}
	internal, err := a.scheduler.RunNow(ctx, workflowID)
	if err != nil {
		return RunSummary{}, err
	}
	return RunSummary{
		RunID:      internal.RunID,
		WorkflowID: internal.WorkflowID,
		Status:     internal.Status,
		StartedAt:  internal.StartedAt,
		EndedAt:    internal.EndedAt,
		Err:        internal.Err,
		Scheduled:  internal.Scheduled,
	}, nil
}

// --- Scheduled-inbox methods (workflow-extensions-01KW2D3Y WP01) ---

// ScheduleRunHistory implements WorkflowsAPI.
func (a *API) ScheduleRunHistory(ctx context.Context, workflowID string, limit int) ([]RunSummary, error) {
	if a == nil || a.cfg.Disabled {
		return nil, ErrFeatureDisabled
	}
	if a.scheduler == nil {
		return nil, ErrSchedulerUnavailable
	}
	internal, err := a.scheduler.History(ctx, workflowID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RunSummary, 0, len(internal))
	for _, s := range internal {
		out = append(out, RunSummary{
			RunID:      s.RunID,
			WorkflowID: s.WorkflowID,
			Status:     s.Status,
			StartedAt:  s.StartedAt,
			EndedAt:    s.EndedAt,
			Err:        s.Err,
			Scheduled:  s.Scheduled,
		})
	}
	return out, nil
}

// ScheduleNextFire implements WorkflowsAPI.
func (a *API) ScheduleNextFire(ctx context.Context, workflowID string) (time.Time, error) {
	if a == nil || a.cfg.Disabled {
		return time.Time{}, ErrFeatureDisabled
	}
	if a.scheduler == nil {
		return time.Time{}, ErrSchedulerUnavailable
	}
	return a.scheduler.NextFire(ctx, workflowID)
}

// CancelRun implements WorkflowsAPI.
func (a *API) CancelRun(_ context.Context, runID string) error {
	if a == nil || a.cfg.Disabled {
		return ErrFeatureDisabled
	}
	if a.cfg.Engine == nil {
		return ErrEngineUnavailable
	}
	return a.cfg.Engine.Cancel(runID)
}

// --- Catalog methods (workflows-agentic-01KW2D3X WP03) ---

// Catalog_List implements WorkflowsAPI.
func (a *API) Catalog_List(ctx context.Context) ([]CatalogEntry, error) {
	if a == nil || a.cfg.Disabled {
		return nil, ErrFeatureDisabled
	}
	if a.catalog == nil {
		return nil, ErrCatalogUnavailable
	}
	entries, err := a.catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, projectCatalogEntry(e))
	}
	return out, nil
}

// Catalog_Get implements WorkflowsAPI.
func (a *API) Catalog_Get(ctx context.Context, id string) (CatalogPreview, error) {
	if a == nil || a.cfg.Disabled {
		return CatalogPreview{}, ErrFeatureDisabled
	}
	if a.catalog == nil {
		return CatalogPreview{}, ErrCatalogUnavailable
	}
	doc, err := a.catalog.Get(ctx, id)
	if err != nil {
		return CatalogPreview{}, err
	}
	return CatalogPreview{
		Entry:      projectCatalogEntry(doc.Entry),
		YAMLSource: doc.YAMLSource,
	}, nil
}

// Catalog_Install implements WorkflowsAPI.
func (a *API) Catalog_Install(ctx context.Context, id string) (CatalogInstallResult, error) {
	if a == nil || a.cfg.Disabled {
		return CatalogInstallResult{}, ErrFeatureDisabled
	}
	if a.catalog == nil {
		return CatalogInstallResult{}, ErrCatalogUnavailable
	}
	ref, err := a.catalog.Install(ctx, id)
	if err != nil {
		return CatalogInstallResult{}, err
	}
	// After install, refresh the in-memory byID cache so List/Get picks
	// up the newly persisted workflow without a chassis restart.
	if a.cfg.Store != nil {
		if w, lerr := a.cfg.Store.Load(ctx, ref.WorkflowID); lerr == nil {
			a.mu.Lock()
			a.byID[w.ID] = w
			a.source[w.ID] = "user"
			a.mu.Unlock()
		}
	}
	return CatalogInstallResult{
		WorkflowID:         ref.WorkflowID,
		Scheduled:          ref.Scheduled,
		MissingCredentials: ref.MissingCredentials,
	}, nil
}

// ErrWorkflowPayloadMalformed is returned by InstallDocument when a fleet
// workflow payload is not a workflow document (install-framework-01DOGF0B
// FR-2: an opaque payload is a named install error, never a silent
// success).
var ErrWorkflowPayloadMalformed = errors.New("workflows: payload is not a workflow document")

// ErrWorkflowIDMismatch is returned when a fleet workflow payload's id is not
// the id its catalog item advertised (FR-2: a workflow item's slug is the
// workflow id). Nothing is written.
var ErrWorkflowIDMismatch = errors.New("workflows: the payload's workflow id does not match the catalog item's slug")

// ErrWorkflowIDCollision is returned when a fleet workflow payload's id is
// already taken by something this catalog item does not own — a shipped
// template, a workflow the user authored, or another catalog item's
// workflow. Nothing is written and nothing is deleted
// (install-framework-01DOGF0B WP05 review H1/H2).
var ErrWorkflowIDCollision = errors.New("workflows: the workflow id is already in use by something this catalog item does not own")

// DocumentOrigin names the fleet catalog item a workflow document came from.
type DocumentOrigin struct {
	CatalogID string
	// Slug is the workflow id the catalog item advertised.
	Slug    string
	Version string
	// Mandated marks an org-mandated install (bundle mandated_items, WP02):
	// provenance is recorded as corewf.ProvenanceMandated. A mandated item
	// arrives inside the SIGNED bundle with no separately advertised slug,
	// so an empty Slug means "the document's own id".
	Mandated bool
}

// InstallDocument installs a workflow delivered as a document — the fleet
// catalog's workflow payload (install-framework-01DOGF0B WP05, FR-2) —
// through the same consumer path a catalog builtin takes: Cedar save gate,
// Store.Save, the in-memory catalog Workflows_List reads, the save audit,
// and a cron arm when the document carries schedule + timezone.
//
// Accepted formats (FR-2 workflow format): a YAML document in the corewf
// schema (yaml tags — what ExportYAML writes), or a JSON object in the
// corewf json-tag shape (what the Workflows › Publish dialog sends today).
//
// Ownership (review H1/H2): the document's id must equal origin.Slug
// (ErrWorkflowIDMismatch), and it may only CREATE a new workflow id or
// UPDATE a row whose recorded provenance is this same catalog item. A
// shipped template's id, a user-authored workflow, or another catalog
// item's workflow is refused with ErrWorkflowIDCollision before anything is
// written. The only cleanup path deletes a row this call created.
func (a *API) InstallDocument(ctx context.Context, payload []byte, origin DocumentOrigin) (CatalogInstallResult, error) {
	if a == nil || a.cfg.Disabled {
		return CatalogInstallResult{}, ErrFeatureDisabled
	}
	if a.cfg.Store == nil {
		return CatalogInstallResult{}, ErrStorageUnavailable
	}
	if origin.CatalogID == "" || (origin.Slug == "" && !origin.Mandated) {
		return CatalogInstallResult{}, fmt.Errorf("workflows: install document: the catalog item and its advertised id are required")
	}
	w, err := decodeWorkflowDocument(payload)
	if err != nil {
		return CatalogInstallResult{}, err
	}
	if origin.Mandated && origin.Slug == "" {
		origin.Slug = w.ID
	}
	a.installMu.Lock()
	defer a.installMu.Unlock()
	if w.ID != origin.Slug {
		return CatalogInstallResult{}, fmt.Errorf("%w: catalog %q advertises %q, payload is %q", ErrWorkflowIDMismatch, origin.CatalogID, origin.Slug, w.ID)
	}
	if a.isShippedTemplate(ctx, w.ID) {
		return CatalogInstallResult{}, fmt.Errorf("%w: %q is a shipped template", ErrWorkflowIDCollision, w.ID)
	}
	created := false
	if _, lerr := a.cfg.Store.Load(ctx, w.ID); lerr != nil {
		if !errors.Is(lerr, corewf.ErrWorkflowNotFound) {
			return CatalogInstallResult{}, lerr
		}
		created = true
	} else if !origin.Mandated && a.isOrgMandated(w.ID) {
		// A user catalog (re)install must never relabel the org's mandated
		// copy as the user's own — that would end the org's protection.
		return CatalogInstallResult{}, fmt.Errorf("%w: %q", ErrWorkflowOrgManaged, w.ID)
	} else if err := a.ownedByCatalogItem(w.ID, origin.CatalogID); err != nil {
		return CatalogInstallResult{}, err
	}
	if _, gerr := cedar.GateWorkflowSave(ctx, a.cfg.Cedar, w.ID, a.cedarMode(), corewf.CollectStepKinds(w)); gerr != nil {
		return CatalogInstallResult{}, fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}
	// An update of this item's own earlier install keeps the user's
	// schedule state (rescheduled or cleared), exactly like
	// Catalog_Update (re-review low 4); only a new install arms the
	// document's own schedule.
	var prior *wfsched.ScheduleEntry
	if !created && a.scheduler != nil {
		entries, serr := a.scheduler.List(ctx)
		if serr != nil {
			return CatalogInstallResult{}, serr
		}
		for i := range entries {
			if entries[i].WorkflowID == w.ID {
				prior = &entries[i]
				break
			}
		}
	}
	saved, err := a.cfg.Store.Save(ctx, w)
	if err != nil {
		return CatalogInstallResult{}, err
	}
	if a.cfg.Provenance != nil {
		if perr := a.cfg.Provenance.Put(corewf.InstallProvenance{
			WorkflowID: saved.ID, Source: documentProvenanceSource(origin),
			CatalogID: origin.CatalogID, Slug: origin.Slug, Version: origin.Version,
		}); perr != nil {
			if created {
				// Only a row this call created may be removed.
				_ = a.cfg.Store.Delete(ctx, saved.ID)
			}
			return CatalogInstallResult{}, fmt.Errorf("workflows: install document: record provenance: %w", perr)
		}
	}
	a.mu.Lock()
	a.byID[saved.ID] = saved
	a.source[saved.ID] = "user"
	a.mu.Unlock()
	corewf.EmitSaved(ctx, a.cfg.Audit, saved)
	res := CatalogInstallResult{WorkflowID: saved.ID}
	switch {
	case a.scheduler == nil:
	case !created && prior != nil:
		if err := a.scheduler.Register(ctx, saved.ID, prior.Cron, prior.Timezone); err == nil {
			res.Scheduled = true
		}
	case !created:
		// The user cleared it (or it never had one): leave it cleared.
	case saved.Schedule != "":
		if err := a.scheduler.Register(ctx, saved.ID, saved.Schedule, saved.Timezone); err == nil {
			res.Scheduled = true
		}
	}
	return res, nil
}

// ownedByCatalogItem reports (as a nil error) whether the existing row id
// was installed from catalogID; anything else is ErrWorkflowIDCollision.
func (a *API) ownedByCatalogItem(id, catalogID string) error {
	if a.cfg.Provenance == nil {
		return fmt.Errorf("%w: %q exists and no install provenance is recorded", ErrWorkflowIDCollision, id)
	}
	p, ok, err := a.cfg.Provenance.Get(id)
	if err != nil {
		return err
	}
	switch {
	case !ok:
		return fmt.Errorf("%w: %q is a workflow you created", ErrWorkflowIDCollision, id)
	case p.Source != corewf.ProvenanceCatalog && p.Source != corewf.ProvenanceMandated:
		return fmt.Errorf("%w: %q was installed from a shipped template", ErrWorkflowIDCollision, id)
	case p.CatalogID != catalogID:
		return fmt.Errorf("%w: %q was installed from catalog item %q", ErrWorkflowIDCollision, id, p.CatalogID)
	}
	return nil
}

// documentProvenanceSource is the provenance a document install records.
func documentProvenanceSource(o DocumentOrigin) string {
	if o.Mandated {
		return corewf.ProvenanceMandated
	}
	return corewf.ProvenanceCatalog
}

// InstallMandatedDocument installs an org-mandated workflow document
// (bundle mandated_items, WP02) and reports whether the mandate TOOK OVER
// the user's own earlier catalog install of the same item (review F6), so
// withdrawal can hand it back instead of deleting it.
func (a *API) InstallMandatedDocument(ctx context.Context, payload []byte, catalogID, version string) (string, bool, error) {
	tookOver := false
	if w, err := decodeWorkflowDocument(payload); err == nil && a != nil && a.cfg.Provenance != nil {
		if p, ok, perr := a.cfg.Provenance.Get(w.ID); perr == nil && ok &&
			p.Source == corewf.ProvenanceCatalog && p.CatalogID == catalogID {
			tookOver = true
		}
	}
	res, err := a.InstallDocument(ctx, payload, DocumentOrigin{CatalogID: catalogID, Version: version, Mandated: true})
	if err != nil {
		return "", false, err
	}
	return res.WorkflowID, tookOver, nil
}

// RemoveMandatedDocument ends the org mandate on workflowID — ONLY while its
// recorded provenance is still the mandate of catalogID (a workflow the user
// or another install now owns, or one already gone, is left alone, nil).
// restoreCatalog true (the mandate had taken over the user's own catalog
// install — review F6) hands it back: provenance returns to catalog and the
// workflow stays. Otherwise it is deleted (schedule disarmed first).
func (a *API) RemoveMandatedDocument(ctx context.Context, workflowID, catalogID string, restoreCatalog bool) error {
	if a == nil || a.cfg.Disabled {
		return ErrFeatureDisabled
	}
	if a.cfg.Provenance == nil {
		return fmt.Errorf("workflows: remove mandated %q: no install provenance store wired", workflowID)
	}
	p, ok, err := a.cfg.Provenance.Get(workflowID)
	if err != nil {
		return err
	}
	if !ok || p.Source != corewf.ProvenanceMandated || p.CatalogID != catalogID {
		return nil
	}
	if restoreCatalog {
		p.Source = corewf.ProvenanceCatalog
		return a.cfg.Provenance.Put(p)
	}
	if a.scheduler != nil {
		// Directly, not via ScheduleClear: that public path refuses a
		// mandated workflow, and this IS the mandate's own removal.
		_ = a.scheduler.Unregister(ctx, workflowID) // no schedule is not an error here
	}
	if err := a.deleteWorkflow(ctx, workflowID); err != nil && !errors.Is(err, corewf.ErrWorkflowNotFound) {
		return err
	}
	return nil
}

// isShippedTemplate reports whether id is one of the binary's templates.
func (a *API) isShippedTemplate(ctx context.Context, id string) bool {
	for _, w := range a.cfg.Catalog {
		if w.ID == id {
			return true
		}
	}
	if a.catalog != nil {
		if _, err := a.catalog.Get(ctx, id); err == nil {
			return true
		}
	}
	return false
}

// Catalog_Update re-installs a shipped template's current (newer) body over
// an installed copy — review H4 — while keeping the user's schedule state:
// a schedule the user set (or cleared) survives, instead of being reset to
// the template's own schedule line. User edits to the installed copy are
// overwritten; that is what updating a template means.
func (a *API) Catalog_Update(ctx context.Context, id string) (CatalogInstallResult, error) {
	var prior *wfsched.ScheduleEntry
	if a.scheduler != nil {
		entries, err := a.scheduler.List(ctx)
		if err != nil {
			return CatalogInstallResult{}, err
		}
		for i := range entries {
			if entries[i].WorkflowID == id {
				prior = &entries[i]
				break
			}
		}
	}
	res, err := a.Catalog_Install(ctx, id)
	if err != nil {
		return CatalogInstallResult{}, err
	}
	if a.scheduler != nil {
		if prior != nil {
			if err := a.scheduler.Register(ctx, res.WorkflowID, prior.Cron, prior.Timezone); err != nil {
				return res, fmt.Errorf("workflows: update %s: restore schedule: %w", id, err)
			}
			res.Scheduled = true
		} else {
			if err := a.scheduler.Unregister(ctx, res.WorkflowID); err != nil {
				return res, fmt.Errorf("workflows: update %s: keep schedule cleared: %w", id, err)
			}
			res.Scheduled = false
		}
	}
	return res, nil
}

// decodeWorkflowDocument parses a workflow payload (see InstallDocument)
// and applies the save-time validation.
func decodeWorkflowDocument(payload []byte) (corewf.Workflow, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return corewf.Workflow{}, fmt.Errorf("%w: empty payload", ErrWorkflowPayloadMalformed)
	}
	var (
		w   corewf.Workflow
		err error
	)
	if trimmed[0] == '{' {
		if err = json.Unmarshal(trimmed, &w); err != nil {
			return corewf.Workflow{}, fmt.Errorf("%w: %v", ErrWorkflowPayloadMalformed, err)
		}
	} else if w, err = corewf.LoadYAML(trimmed); err != nil {
		return corewf.Workflow{}, fmt.Errorf("%w: %v", ErrWorkflowPayloadMalformed, err)
	}
	if err := corewf.ValidateForSave(w); err != nil {
		return corewf.Workflow{}, fmt.Errorf("%w: %v", ErrWorkflowPayloadMalformed, err)
	}
	return w, nil
}

// projectCatalogEntry converts a catalog.Entry to the wire CatalogEntry.
func projectCatalogEntry(e wfcatalog.Entry) CatalogEntry {
	return CatalogEntry{
		ID:                  e.ID,
		Name:                e.Name,
		Description:         e.Description,
		Source:              e.Source,
		Version:             e.Version,
		Icon:                e.Icon,
		RequiresCedarGrants: e.RequiresCedarGrants,
		RequiresCredentials: e.RequiresCredentials,
		EstimatedCostUSD:    e.EstimatedCostUSD,
		InstallStatus:       e.InstallStatus,
	}
}

func projectWorkflow(w corewf.Workflow) Workflow {
	out := Workflow{
		ID: w.ID, Name: w.Name, Description: w.Description, Version: w.Version,
		Inputs: make([]Input, 0, len(w.Inputs)),
		Steps:  make([]Step, 0, len(w.Steps)),
	}
	for _, in := range w.Inputs {
		out.Inputs = append(out.Inputs, Input{
			Name: in.Name, Kind: string(in.Kind),
			Required: in.Required, Default: in.Default, Options: in.Options,
		})
	}
	for _, st := range w.Steps {
		out.Steps = append(out.Steps, Step{
			Name: st.Name, Kind: string(st.Kind), InputsFrom: st.InputsFrom,
			UserPrompt: st.UserPrompt, Cmd: st.Cmd, Args: st.Args,
			Method: st.Method, URL: st.URL, Mode: st.Mode,
		})
	}
	return out
}
