// Concrete ScheduledChatAPI implementation.
package scheduledchat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// ErrStoreUnavailable is returned when no ScheduledChatStore is wired.
var ErrStoreUnavailable = errors.New("scheduledchat: store unavailable")

// ErrNotFound is returned when the requested scheduled chat run does not exist.
var ErrNotFound = errors.New("scheduledchat: not found")

// ErrCedarDenied is returned when a cedar gate explicitly denies an operation.
var ErrCedarDenied = errors.New("scheduledchat: denied by cedar policy")

// ErrDispatcherUnavailable is returned when RunNow is called with no
// Dispatcher wired. A nil dispatcher is a configuration error, not a
// no-op: no history row is appended, so nothing records a fabricated
// "completed" outcome for a run that did not happen (FR-002).
var ErrDispatcherUnavailable = errors.New("scheduledchat: dispatcher unavailable")

// ErrInvalidInput is returned when a required field is missing.
var ErrInvalidInput = errors.New("scheduledchat: invalid input")

// Config bundles the dependencies the impl needs.
type Config struct {
	// Store is the SQLite-backed persistence layer. nil causes Create /
	// Update / Delete / RunNow to return ErrStoreUnavailable; List
	// returns an empty slice.
	Store scheduler.ScheduledChatStore
	// Dispatcher fires actual chat runs. nil causes RunNow to return
	// ErrDispatcherUnavailable and append no history row — a run that did
	// not happen must not be recorded as one that did (FR-002). As of
	// WP04/WP05, this field is also assigned into the chat-run cron
	// engine so scheduled (not just RunNow) firings share one dispatcher.
	Dispatcher scheduler.ChatRunDispatcher
	// Engine is the chat-run cron engine (core/scheduler.ChatCronEngine in
	// production). nil leaves scheduled_chat_runs rows persisted but never
	// armed on a ticking engine until the next process restart's boot
	// reload — Create / Update / Delete / SetEnabled become
	// store-only operations. Mission model-scheduled-jobs-01PMSJ01 WP03.
	Engine Registrar
	// Cedar is the policy gate. nil short-circuits to allow (default-allow).
	Cedar cedar.Gate
}

// API is the concrete ScheduledChatAPI.
type API struct {
	cfg Config
}

// New returns a ScheduledChatAPI backed by cfg.
func New(cfg Config) *API {
	return &API{cfg: cfg}
}

// Create implements ScheduledChatAPI.
func (a *API) Create(ctx context.Context, in CreateInput) (ChatRunEntry, error) {
	if in.Cron == "" {
		return ChatRunEntry{}, fmt.Errorf("%w: cron is required", ErrInvalidInput)
	}
	if a.cfg.Store == nil {
		return ChatRunEntry{}, ErrStoreUnavailable
	}

	id := newID()

	// Cedar gate — default-allow; permissive posture.
	if _, gerr := cedar.GateScheduledChatCreate(ctx, a.cfg.Cedar, id); gerr != nil {
		return ChatRunEntry{}, fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}

	sink := in.OutputSink
	if sink == "" {
		sink = "banner"
	}
	now := time.Now().UTC()
	rec := scheduler.ChatRunRecord{
		ID:             id,
		Name:           in.Name,
		PromptTemplate: in.PromptTemplate,
		Cron:           in.Cron,
		Timezone:       in.Timezone,
		Model:          in.Model,
		OutputSink:     sink,
		Enabled:        in.Enabled,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := a.cfg.Store.Create(ctx, rec); err != nil {
		return ChatRunEntry{}, fmt.Errorf("scheduledchat: create: %w", err)
	}
	a.syncEngine(ctx, id)
	return chatRunEntryFromRecord(rec), nil
}

// Update implements ScheduledChatAPI.
func (a *API) Update(ctx context.Context, in UpdateInput) (ChatRunEntry, error) {
	if in.ID == "" {
		return ChatRunEntry{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	if in.Cron == "" {
		return ChatRunEntry{}, fmt.Errorf("%w: cron is required", ErrInvalidInput)
	}
	if a.cfg.Store == nil {
		return ChatRunEntry{}, ErrStoreUnavailable
	}

	// Cedar gate for update (same action as create — manages the run record).
	if _, gerr := cedar.GateScheduledChatCreate(ctx, a.cfg.Cedar, in.ID); gerr != nil {
		return ChatRunEntry{}, fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}

	sink := in.OutputSink
	if sink == "" {
		sink = "banner"
	}
	now := time.Now().UTC()
	rec := scheduler.ChatRunRecord{
		ID:             in.ID,
		Name:           in.Name,
		PromptTemplate: in.PromptTemplate,
		Cron:           in.Cron,
		Timezone:       in.Timezone,
		Model:          in.Model,
		OutputSink:     sink,
		Enabled:        in.Enabled,
		UpdatedAt:      now,
	}
	if err := a.cfg.Store.Update(ctx, rec); err != nil {
		if errors.Is(err, scheduler.ErrChatRunNotFound) {
			return ChatRunEntry{}, ErrNotFound
		}
		return ChatRunEntry{}, fmt.Errorf("scheduledchat: update: %w", err)
	}
	// Re-read to get the original created_at.
	updated, err := a.cfg.Store.Get(ctx, in.ID)
	if err != nil {
		return ChatRunEntry{}, fmt.Errorf("scheduledchat: get after update: %w", err)
	}
	a.syncEngine(ctx, in.ID)
	return chatRunEntryFromRecord(updated), nil
}

// Delete implements ScheduledChatAPI.
func (a *API) Delete(ctx context.Context, id string) error {
	if a.cfg.Store == nil {
		return ErrStoreUnavailable
	}
	if _, gerr := cedar.GateScheduledChatDelete(ctx, a.cfg.Cedar, id); gerr != nil {
		return fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}
	if err := a.cfg.Store.Delete(ctx, id); err != nil {
		return err
	}
	if a.cfg.Engine != nil {
		if err := a.cfg.Engine.Unregister(ctx, id); err != nil {
			slog.WarnContext(ctx, "scheduledchat: cron unregister failed after delete",
				"chat_run_id", id, "error", err.Error())
		}
	}
	return nil
}

// List implements ScheduledChatAPI.
func (a *API) List(ctx context.Context) ([]ChatRunEntry, error) {
	if a.cfg.Store == nil {
		return nil, nil
	}
	recs, err := a.cfg.Store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("scheduledchat: list: %w", err)
	}
	out := make([]ChatRunEntry, 0, len(recs))
	for _, r := range recs {
		out = append(out, chatRunEntryFromRecord(r))
	}
	return out, nil
}

// Get implements ScheduledChatAPI.
func (a *API) Get(ctx context.Context, id string) (ChatRunEntry, error) {
	if a.cfg.Store == nil {
		return ChatRunEntry{}, ErrStoreUnavailable
	}
	rec, err := a.cfg.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, scheduler.ErrChatRunNotFound) {
			return ChatRunEntry{}, ErrNotFound
		}
		return ChatRunEntry{}, fmt.Errorf("scheduledchat: get: %w", err)
	}
	return chatRunEntryFromRecord(rec), nil
}

// RunNow implements ScheduledChatAPI.
func (a *API) RunNow(ctx context.Context, id string) (RunSummary, error) {
	if a.cfg.Store == nil {
		return RunSummary{}, ErrStoreUnavailable
	}

	rec, err := a.cfg.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, scheduler.ErrChatRunNotFound) {
			return RunSummary{}, ErrNotFound
		}
		return RunSummary{}, fmt.Errorf("scheduledchat: run-now get: %w", err)
	}

	// Cedar gate for execute.
	if _, gerr := cedar.GateScheduledChatExecute(ctx, a.cfg.Cedar, id); gerr != nil {
		return RunSummary{}, fmt.Errorf("%w: %v", ErrCedarDenied, gerr)
	}

	d := a.cfg.Dispatcher
	if d == nil {
		return RunSummary{}, ErrDispatcherUnavailable
	}

	job := scheduler.Job{
		ID:   rec.ID,
		Kind: scheduler.JobKindChatRun,
		ChatRun: &scheduler.ChatRunSpec{
			ID:             rec.ID,
			PromptTemplate: rec.PromptTemplate,
			Model:          rec.Model,
			OutputSink:     rec.OutputSink,
		},
		Trigger: scheduler.Trigger{Cron: rec.Cron, TZ: rec.Timezone},
	}

	histRec, err := d.DispatchChatRun(ctx, job, time.Now().UTC())
	if err != nil {
		return RunSummary{}, fmt.Errorf("scheduledchat: dispatch: %w", err)
	}

	// Assign a fresh history row ID and persist.
	histRec.ID = newID()
	histRec.ChatRunID = id
	// (FR-006) WARN-log on history-write failure so a missing run record is
	// diagnosable. The summary is still returned — the run completed; only
	// the history entry is missing (non-fatal from the user's perspective).
	if perr := a.cfg.Store.AppendHistory(ctx, histRec); perr != nil {
		slog.WarnContext(ctx, "scheduledchat: history write failed; run record not persisted",
			"chat_run_id", id,
			"error",       perr.Error(),
		)
	}

	return runSummaryFromRecord(histRec), nil
}

// History implements ScheduledChatAPI.
func (a *API) History(ctx context.Context, id string, limit int) ([]RunSummary, error) {
	if a.cfg.Store == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	recs, err := a.cfg.Store.History(ctx, id, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduledchat: history: %w", err)
	}
	out := make([]RunSummary, 0, len(recs))
	for _, r := range recs {
		out = append(out, runSummaryFromRecord(r))
	}
	return out, nil
}

// SetEnabled implements ScheduledChatAPI.
func (a *API) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if a.cfg.Store == nil {
		return ErrStoreUnavailable
	}
	if err := a.cfg.Store.SetEnabled(ctx, id, enabled); err != nil {
		if errors.Is(err, scheduler.ErrChatRunNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("scheduledchat: set-enabled: %w", err)
	}
	a.syncEngine(ctx, id)
	return nil
}

// syncEngine re-arms or disarms id's cron entry against the row currently
// in the store. A nil Engine (test chassis, or no DB) is a no-op — the
// row is still persisted; it will not fire until the next process
// restart's boot reload picks it up. A Sync error is logged, not
// propagated: the store write already succeeded, and a malformed cron
// expression must not roll back an otherwise-valid Create/Update/
// SetEnabled (it is surfaced via this log line, and the row can be fixed
// with another Update).
func (a *API) syncEngine(ctx context.Context, id string) {
	if a.cfg.Engine == nil {
		return
	}
	if err := a.cfg.Engine.Sync(ctx, id); err != nil {
		slog.WarnContext(ctx, "scheduledchat: cron sync failed",
			"chat_run_id", id, "error", err.Error())
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func chatRunEntryFromRecord(r scheduler.ChatRunRecord) ChatRunEntry {
	return ChatRunEntry{
		ID:             r.ID,
		Name:           r.Name,
		PromptTemplate: r.PromptTemplate,
		Cron:           r.Cron,
		Timezone:       r.Timezone,
		Model:          r.Model,
		OutputSink:     r.OutputSink,
		Enabled:        r.Enabled,
		CreatedAt:      r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func runSummaryFromRecord(r scheduler.ChatRunHistoryRecord) RunSummary {
	s := RunSummary{
		ID:            r.ID,
		ChatRunID:     r.ChatRunID,
		SessionID:     r.SessionID,
		Status:        r.Status,
		StartedAt:     r.StartedAt,
		OutputSnippet: r.OutputSnippet,
		Error:         r.Error,
	}
	if r.EndedAt != nil {
		t := *r.EndedAt
		s.EndedAt = &t
	}
	return s
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Compile-time interface check.
var _ ScheduledChatAPI = (*API)(nil)
