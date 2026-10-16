// Package fleet — sync.go
//
// Per-category settings sync (push-up / pull-down) between harness devices.
// Wire contract: §2b of kenaz-fleet/docs/contract-harness-sync.md, served by
// kenaz-fleet service/handlers_sync.go (wire types PutSyncRequest and
// SyncStateResponse) over table user_sync_state (migration 0062):
//
//	PUT /api/v1/sync/{category}                 body {"lww_ts": RFC3339, "payload": {...}}
//	GET /api/v1/sync/{category}?since=<RFC3339> 200 SyncStateResponse | 304
//
// Until provider-credentials-client-01PCCLI01 A2 this file sent POST with an
// int64 lww_ts and pulled with ?since=<int>. Fleet answers the first with 405
// and the others with 400, so settings sync never round-tripped
// (sync_wire_test.go pins the fix).
//
// Categories Fleet accepts: provider_profiles, model_prefs, mcp_recipes,
// installed_mcp, ui_theme (fleetAcceptedSyncCategories). Anything else
// registered here (slash_commands) stays on this device.
//
// Uploads are gated (SetUploadGate): until the user has confirmed once that
// their settings may leave the machine, Push holds every category. Pulls are
// not gated; see Pull.
//
// Fork-removal recipe: delete this file + sync_mcp.go + core/rpc/views/sync/ +
// frontend/src/views/settings/SyncPanel.vue + the "Sync" tab in settings.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// SyncCategory identifies one of the five per-user sync surfaces.
type SyncCategory string

const (
	SyncCategoryProviderProfiles SyncCategory = "provider_profiles"
	SyncCategoryModelPrefs       SyncCategory = "model_prefs"
	SyncCategoryMCPRecipes       SyncCategory = "mcp_recipes"
	SyncCategoryInstalledMCP     SyncCategory = "installed_mcp"
	SyncCategoryUITheme          SyncCategory = "ui_theme"
)

// AllSyncCategories returns every known SyncCategory in declaration order.
func AllSyncCategories() []SyncCategory {
	return []SyncCategory{
		SyncCategoryProviderProfiles,
		SyncCategoryModelPrefs,
		SyncCategoryMCPRecipes,
		SyncCategoryInstalledMCP,
		SyncCategoryUITheme,
	}
}

// fleetAcceptedSyncCategories is the set of categories Fleet's settings-sync
// endpoint accepts. It mirrors kenaz-fleet service/sync_schema.go's
// syncSchemas map (ParseSyncCategory rejects anything else: 422
// unknown_category on PUT, 400 on GET) and the CHECK constraint in migration
// 0062_user_sync_state. A category registered with the Syncer but absent from
// this list (slash_commands today) is never pushed or pulled; it stays on
// this device. The contract says Fleet ships a category's schema before the
// harness sends it (§2b), so add an entry here only once sync_schema.go has
// it.
var fleetAcceptedSyncCategories = map[SyncCategory]bool{
	SyncCategoryProviderProfiles: true,
	SyncCategoryModelPrefs:       true,
	SyncCategoryMCPRecipes:       true,
	SyncCategoryInstalledMCP:     true,
	SyncCategoryUITheme:          true,
}

// FleetAcceptsSyncCategory reports whether Fleet's settings-sync endpoint
// accepts cat (see fleetAcceptedSyncCategories).
func FleetAcceptsSyncCategory(cat SyncCategory) bool {
	return fleetAcceptedSyncCategories[cat]
}

// ErrSyncUploadNotConfirmed is returned by Push while the upload gate is
// closed, because the user has not yet confirmed that settings may leave
// this device. Nothing was sent.
var ErrSyncUploadNotConfirmed = errors.New("fleet/sync: uploads are paused until you confirm settings sync")

// ErrSyncCategoryLocalOnly is returned by Push and Pull for a category Fleet
// does not accept. Nothing was sent.
var ErrSyncCategoryLocalOnly = errors.New("fleet/sync: this category stays on this device; Fleet does not sync it yet")

// ErrSyncClockSkew is returned by Push when Fleet rejects the write with 422
// lww_ts_in_future: this device's clock is more than 5 minutes ahead.
var ErrSyncClockSkew = errors.New("fleet/sync: this device's clock is more than 5 minutes ahead of Fleet; correct the clock and sync will resume")

// syncPutRequest is exactly kenaz-fleet's PutSyncRequest
// (service/handlers_sync.go): lww_ts is an RFC 3339 string and payload is the
// category blob. The body has no category field; the category is the path.
type syncPutRequest struct {
	LWWTs   string          `json:"lww_ts"`
	Payload json.RawMessage `json:"payload"`
}

// syncStateResponse is kenaz-fleet's SyncStateResponse, returned by a
// successful PUT and by GET.
type syncStateResponse struct {
	Category  string          `json:"category"`
	LWWTs     lwwStamp        `json:"lww_ts"`
	Payload   json.RawMessage `json:"payload"`
	UpdatedAt string          `json:"updated_at"`
}

// fleetErrorBody is kenaz-fleet's httpcore.ErrorResponse envelope. Fleet
// never echoes a payload value in it (contract §2b, "422 error shape").
type fleetErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// lwwStamp is an LWW timestamp in Unix nanoseconds, the harness's internal
// form. It is monotonic and directly comparable, and 0 means "nothing seen".
//
// On the wire it is RFC 3339 only, because Fleet's user_sync_state.lww_ts
// column is TIMESTAMPTZ. Fleet main routes only PUT and parses lww_ts as
// RFC 3339 only: it has no POST route and no int64 form, in this or any
// release, so a pre-A2 harness build cannot upload at all (which is what
// lets the H1 confirmation be the only way settings first leave a device).
// A bare JSON integer on the wire is rejected. Fleet's "nothing stored yet" answer
// carries the zero time ("0001-01-01T00:00:00Z"), which decodes to 0.
//
// No int64 form survives anywhere locally: CategoryState.lwwTS is
// persisted (sync_state.go) as RFC 3339 too, and nothing before that
// persisted it, so there is no cached int64 to migrate and no legacy
// decoder is needed.
type lwwStamp int64

// UnmarshalJSON accepts an RFC 3339 string only.
func (l *lwwStamp) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*l = 0
		return nil
	}
	if b[0] != '"' {
		return fmt.Errorf("fleet/sync: lww_ts must be an RFC 3339 string, got %.32s", b)
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	ns, err := parseLWW(str)
	if err != nil {
		return err
	}
	*l = lwwStamp(ns)
	return nil
}

// parseLWW parses an RFC 3339 timestamp, with or without a fractional
// second (Fleet accepts both), into Unix nanos. The zero time maps to 0.
func parseLWW(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, fmt.Errorf("fleet/sync: lww_ts %q is not RFC 3339: %w", s, err)
	}
	if t.Unix() <= 0 {
		return 0, nil
	}
	return t.UnixNano(), nil
}

// formatLWW renders Unix nanos as the RFC 3339 UTC string Fleet expects.
func formatLWW(ns int64) string {
	return time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
}

// nextLWW returns the timestamp for a new push. It is now truncated to the
// microsecond (Postgres TIMESTAMPTZ resolution, so the value Fleet stores and
// echoes equals the one kept here), and strictly greater than prev, so a
// fast second push never earns a self-inflicted 409 stale_lww_ts.
func nextLWW(prev int64) int64 {
	now := time.Now().UTC().Truncate(time.Microsecond).UnixNano()
	if now <= prev {
		now = prev + int64(time.Microsecond)
	}
	return now
}

// CategoryState holds the per-category runtime state.
type CategoryState struct {
	mu sync.Mutex
	// enabled is the user's opt-in. It is persisted by the rpc layer
	// (Settings.SettingsSyncEnabledCategories, written by Sync_Toggle) and
	// re-applied at boot through RestoreEnabled; this field is the live copy.
	enabled  bool
	lastPush time.Time
	lastPull time.Time
	lastErr  string
	// lwwTS is the stamp of the last state this device and Fleet agreed
	// on, in Unix nanoseconds. Persisted through SyncStateStore
	// (sync_state.go), so it survives a restart.
	lwwTS int64
	// fleetRejected is set when Fleet answers 422 unknown_category although
	// fleetAcceptedSyncCategories lists the category (an older Fleet). It is
	// remembered for the life of the process, so no further request is sent.
	fleetRejected bool
}

// SyncStatus is the read-only view of a category's sync state,
// returned by Syncer.Status.
type SyncStatus struct {
	Category   SyncCategory `json:"category"`
	Enabled    bool         `json:"enabled"`
	LastPushAt time.Time    `json:"last_push_at,omitempty"`
	LastPullAt time.Time    `json:"last_pull_at,omitempty"`
	LastError  string       `json:"last_error,omitempty"`
	LWWTS      int64        `json:"lww_ts"`
	// AwaitingConfirmation is true when the category is enabled, Fleet
	// accepts it, and the upload gate is closed: its first upload waits for
	// the user to confirm.
	AwaitingConfirmation bool `json:"awaiting_confirmation,omitempty"`
	// LocalOnly is true when Fleet does not accept this category, so it never
	// leaves the device.
	LocalOnly bool `json:"local_only,omitempty"`
}

// CategoryCollector is called by the Syncer to gather the current local state
// for a category just before pushing. Must return a JSON-marshalable value.
// HARD RULE: returned data must never contain credential bytes.
type CategoryCollector func(ctx context.Context) (json.RawMessage, error)

// CategoryApplier is called by the Syncer when a pull-down produces newer
// remote state. Receives the raw JSON from fleet; applies it to local state.
type CategoryApplier func(ctx context.Context, payload json.RawMessage) error

// CategoryConfig holds the wiring for one sync category.
type CategoryConfig struct {
	Collector CategoryCollector
	Applier   CategoryApplier
}

// Syncer manages per-category sync (push-up / pull-down) against
// the fleet /api/v1/sync/{category} endpoints.
type Syncer struct {
	client     *Client
	mu         sync.RWMutex
	categories map[SyncCategory]*CategoryState
	configs    map[SyncCategory]CategoryConfig
	// debounce channels — one per category; closed when a push is triggered.
	debounceMu sync.Mutex
	debounce   map[SyncCategory]chan struct{}
	stopCh     chan struct{}
	once       sync.Once
	// uploadGate, when set, must return true before Push sends anything.
	// Owner ruling H1 (provider-credentials-client-01PCCLI01 §7a) asks for a
	// one-time confirmation before any category first uploads. Production
	// wires it to Settings.SettingsSyncUploadsConfirmed
	// (core/rpc/sync_upload_consent.go). nil means CLOSED: a Syncer with no
	// gate never uploads, so no path (boot order, the first poll cycle, a
	// debounced push fired before wiring finishes) can upload before the
	// acknowledgement. Fleet has no other consent path: Fleet main has no
	// POST or int64 route a pre-A2 build could upload through.
	gateMu     sync.RWMutex
	uploadGate func() bool
	// stateStore persists lwwTS per category (sync_state.go). nil keeps
	// it in memory only. persistedLate holds loaded entries for categories
	// not registered yet; persistMu serialises saves.
	stateStore    SyncStateStore
	persistedLate map[SyncCategory]PersistedSyncState
	persistMu     sync.Mutex
	// stopOnce guards close(stopCh). Stop is reachable from more than one
	// teardown path (rpc.API.Shutdown, and any caller that stops the syncer
	// directly), and a bare close() panics on the second call with
	// "close of closed channel" — taking the whole process down during
	// shutdown, which is the worst possible moment for it.
	stopOnce sync.Once
}

// NewSyncer constructs a Syncer backed by the given fleet Client.
func NewSyncer(c *Client) *Syncer {
	s := &Syncer{
		client:     c,
		categories: make(map[SyncCategory]*CategoryState),
		configs:    make(map[SyncCategory]CategoryConfig),
		debounce:   make(map[SyncCategory]chan struct{}),
		stopCh:     make(chan struct{}),
	}
	for _, cat := range AllSyncCategories() {
		s.categories[cat] = &CategoryState{}
	}
	return s
}

// SetUploadGate installs the upload gate. Push sends nothing while gate
// returns false. nil closes uploads again (the default).
func (s *Syncer) SetUploadGate(gate func() bool) {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	s.uploadGate = gate
}

// uploadsAllowed reports whether the upload gate is installed and open.
// Fail closed: no gate, no upload.
func (s *Syncer) uploadsAllowed() bool {
	s.gateMu.RLock()
	gate := s.uploadGate
	s.gateMu.RUnlock()
	return gate != nil && gate()
}

// RegisterCategory wires a collector + applier for a sync category.
// Must be called before Start.
//
// Categories not among AllSyncCategories() (i.e. kinds registered through
// the SyncKind registry — synckind.go) get their CategoryState lazily
// created here, so a new kind works end-to-end (SetEnabled / Push / Pull /
// Status / the poll loop) without editing AllSyncCategories(). This is the
// genericity fix fleet-generic-sync-framework-01NSYNC02 WP01 makes: the
// five built-in categories are pre-created by NewSyncer and behave exactly
// as before; anything registered afterward — built-in or not — now works
// the same way.
func (s *Syncer) RegisterCategory(cat SyncCategory, cfg CategoryConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[cat] = cfg
	if _, ok := s.categories[cat]; !ok {
		st := &CategoryState{}
		s.adoptPersistedLocked(cat, st)
		s.categories[cat] = st
	}
}

// SetEnabled toggles sync on/off for a category. When toggled off, no
// further pushes or polls occur for that category (local state is
// preserved).
//
// When toggled on it PULLS FIRST, then pushes only if the pull applied
// nothing (pullThenPush). Pushing first stamped this device's current state
// with a fresh LWW timestamp, so turning a category on on a new device (or
// after a restart, before lwwTS was persisted) overwrote the newer
// model_prefs / ui_theme every other device had already agreed on.
//
// A push held by the upload gate or skipped because the category is
// local-only is not an error here: the category is enabled and uploads once
// the user confirms (or never, for a local-only category).
func (s *Syncer) SetEnabled(ctx context.Context, cat SyncCategory, enabled bool) error {
	s.mu.Lock()
	st, ok := s.categories[cat]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("fleet/sync: unknown category %q", cat)
	}
	st.mu.Lock()
	wasEnabled := st.enabled
	st.enabled = enabled
	st.mu.Unlock()

	if enabled && !wasEnabled {
		// FR-102 (push current local state on enable), after taking any
		// newer state from Fleet.
		err := s.pullThenPush(ctx, cat)
		if errors.Is(err, ErrSyncUploadNotConfirmed) || errors.Is(err, ErrSyncCategoryLocalOnly) {
			return nil
		}
		return err
	}
	return nil
}

// pullThenPush brings a category up to date before uploading it: it pulls,
// and pushes only when the pull applied nothing (Fleet has nothing newer
// than what this device last agreed on). When the pull applied Fleet's
// state, local now equals it, so there is nothing to upload. A failed pull
// skips the push: uploading without knowing what Fleet holds is exactly
// what overwrote other devices' settings.
func (s *Syncer) pullThenPush(ctx context.Context, cat SyncCategory) error {
	applied, err := s.pull(ctx, cat)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	return s.Push(ctx, cat)
}

// HasCategory reports whether cat has runtime state on this Syncer (a
// built-in category, or one registered through RegisterCategory), i.e.
// whether SetEnabled / RestoreEnabled would accept it.
func (s *Syncer) HasCategory(cat SyncCategory) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.categories[cat]
	return ok
}

// RestoreEnabled re-applies a persisted opt-in at boot (the user's choice
// from a previous launch, Settings.SettingsSyncEnabledCategories). Unlike
// SetEnabled it never pushes: restoring a choice is not a new action by the
// user, and in particular is not upload consent. Uploads for a restored
// category still go through Push, so they stay held by the upload gate
// until Settings.SettingsSyncUploadsConfirmed is true, and otherwise wait
// for the next local change (NotifyMutation) — a boot-time push would stamp
// possibly stale local state with a fresh LWW timestamp and overwrite a
// newer write made from another device. Pulls resume on the next poll.
func (s *Syncer) RestoreEnabled(cat SyncCategory, enabled bool) error {
	s.mu.RLock()
	st, ok := s.categories[cat]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("fleet/sync: unknown category %q", cat)
	}
	st.mu.Lock()
	st.enabled = enabled
	st.mu.Unlock()
	return nil
}

// IsEnabled reports whether the given category is enabled.
func (s *Syncer) IsEnabled(cat SyncCategory) bool {
	s.mu.RLock()
	st, ok := s.categories[cat]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.enabled
}

// Status returns the sync status for all categories.
func (s *Syncer) Status() []SyncStatus {
	allowed := s.uploadsAllowed()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SyncStatus, 0, len(s.categories))
	for cat, st := range s.categories {
		st.mu.Lock()
		localOnly := !FleetAcceptsSyncCategory(cat) || st.fleetRejected
		out = append(out, SyncStatus{
			Category:             cat,
			Enabled:              st.enabled,
			LastPushAt:           st.lastPush,
			LastPullAt:           st.lastPull,
			LastError:            st.lastErr,
			LWWTS:                st.lwwTS,
			AwaitingConfirmation: st.enabled && !localOnly && !allowed,
			LocalOnly:            localOnly,
		})
		st.mu.Unlock()
	}
	return out
}

// PushHeld uploads every enabled, Fleet-accepted category. The rpc layer
// calls it right after the user confirms uploads, so categories enabled
// before the confirmation upload at once instead of waiting for the next
// local change. Each one pulls first (pullThenPush): a held category may
// have been enabled long ago, and stamping its local state "now" would
// overwrite anything another device uploaded since.
// Per-category failures are recorded in Status and joined into the result.
func (s *Syncer) PushHeld(ctx context.Context) error {
	var errs []error
	for _, cat := range s.RegisteredCategories() {
		if !s.IsEnabled(cat) || !FleetAcceptsSyncCategory(cat) {
			continue
		}
		if err := s.pullThenPush(ctx, cat); err != nil && !errors.Is(err, ErrSyncCategoryLocalOnly) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// NotifyMutation should be called by any subsystem that mutates a synced
// category's local state. It triggers a debounced push after 5s (FR-104,
// NFR-002).
func (s *Syncer) NotifyMutation(cat SyncCategory) {
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()
	if _, ok := s.debounce[cat]; ok {
		// Already pending — reset is implicit: the goroutine launched below
		// replaces the prior signal when the channel is replaced.
		return
	}
	ch := make(chan struct{}, 1)
	s.debounce[cat] = ch
	go func() {
		// Recover panics from the debounced-push goroutine (FR-003).
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logging.L().Error("fleet.sync.debounced_push.panic",
					"category", string(cat),
					"panic", fmt.Sprintf("%v", r),
					"stack", string(stack),
				)
			}
		}()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.stopCh:
			return
		}
		s.debounceMu.Lock()
		delete(s.debounce, cat)
		s.debounceMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := s.Push(ctx, cat)
		switch {
		case err == nil, errors.Is(err, ErrFleetDisabled):
		case errors.Is(err, ErrSyncUploadNotConfirmed), errors.Is(err, ErrSyncCategoryLocalOnly):
			logging.L().Debug("fleet.sync.debounced_push.held",
				"category", string(cat),
				"reason", err.Error(),
			)
		default:
			logging.L().Warn("fleet.sync.debounced_push.failed",
				"category", string(cat),
				"err", err.Error(),
			)
		}
	}()
}

// categoryLocalOnly reports whether cat must not be sent to Fleet.
func categoryLocalOnly(cat SyncCategory, st *CategoryState) bool {
	if !FleetAcceptsSyncCategory(cat) {
		return true
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fleetRejected
}

// readFleetError decodes a Fleet error envelope. A body that is not one
// yields a code-less value carrying the raw text, truncated.
func readFleetError(resp *http.Response) fleetErrorBody {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e fleetErrorBody
	if err := json.Unmarshal(raw, &e); err != nil || e.Code == "" {
		msg := string(raw)
		if len(msg) > 256 {
			msg = msg[:256]
		}
		return fleetErrorBody{Message: msg}
	}
	return e
}

// describe renders a Fleet error for Status.LastError. It names the code and
// the JSON path Fleet reports; Fleet never echoes a payload value, so nothing
// from the payload reaches the string.
func (e fleetErrorBody) describe(status int) string {
	msg := fmt.Sprintf("status %d", status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if path, ok := e.Details["path"].(string); ok && path != "" {
		msg += " at " + path
	}
	if reason, ok := e.Details["reason"].(string); ok && reason != "" {
		msg += ": " + reason
	} else if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Push immediately pushes the current local state for a category to fleet.
// Skips if the category is not enabled or has no collector. Returns
// ErrSyncUploadNotConfirmed, sending nothing, while the upload gate is
// closed, and ErrSyncCategoryLocalOnly for a category Fleet does not accept.
//
// Fleet's documented errors (handlers_sync.go) are handled without retrying
// the PUT, so a bad state can never turn into a request loop:
//   - 409 stale_lww_ts: Fleet holds a newer write from another device. By
//     LWW that write wins; one Pull applies it and the push is dropped.
//   - 422 lww_ts_in_future: this clock is ahead. ErrSyncClockSkew.
//   - 422 unknown_category: remembered; the category goes local-only.
//   - 422 schema_violation / secret_field_detected / secret_value_detected,
//     413 payload_too_large: recorded in LastError (code + JSON path, never
//     a value); the next local change tries again.
//   - 403: ErrCapabilityNotInTier.
func (s *Syncer) Push(ctx context.Context, cat SyncCategory) error {
	if s.client == nil || s.client.isNop {
		return ErrFleetDisabled
	}
	s.mu.RLock()
	st, catOk := s.categories[cat]
	cfg, cfgOk := s.configs[cat]
	s.mu.RUnlock()
	if !catOk {
		return fmt.Errorf("fleet/sync: unknown category %q", cat)
	}
	st.mu.Lock()
	enabled := st.enabled
	st.mu.Unlock()
	if !enabled {
		return nil
	}
	if !cfgOk || cfg.Collector == nil {
		return nil
	}
	if categoryLocalOnly(cat, st) {
		return ErrSyncCategoryLocalOnly
	}
	// Consumer of Settings.SettingsSyncUploadsConfirmed (via uploadGate):
	// nothing is collected or sent until the user has confirmed once.
	if !s.uploadsAllowed() {
		return ErrSyncUploadNotConfirmed
	}

	raw, err := cfg.Collector(ctx)
	if err != nil {
		return fmt.Errorf("fleet/sync: collect %s: %w", cat, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	st.mu.Lock()
	ts := nextLWW(st.lwwTS)
	st.mu.Unlock()
	body, err := json.Marshal(syncPutRequest{LWWTs: formatLWW(ts), Payload: raw})
	if err != nil {
		return fmt.Errorf("fleet/sync: push %s: encode: %w", cat, err)
	}
	resp, err := s.client.doWithContentType(ctx, http.MethodPut,
		"/api/v1/sync/"+url.PathEscape(string(cat)), "application/json", bytes.NewReader(body))
	if err != nil {
		st.mu.Lock()
		st.lastErr = err.Error()
		st.mu.Unlock()
		return fmt.Errorf("fleet/sync: push %s: %w", cat, err)
	}
	defer resp.Body.Close()

	setErr := func(msg string) {
		st.mu.Lock()
		st.lastErr = msg
		st.mu.Unlock()
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		// Fleet echoes the stored lww_ts; prefer it when present so the
		// local mark equals what ?since= will compare against.
		var echoed syncStateResponse
		if resp.StatusCode == http.StatusOK {
			if derr := json.NewDecoder(resp.Body).Decode(&echoed); derr == nil && echoed.LWWTs > 0 {
				ts = int64(echoed.LWWTs)
			}
		}
		st.mu.Lock()
		st.lastPush = time.Now()
		st.lastErr = ""
		if ts > st.lwwTS {
			st.lwwTS = ts
		}
		st.mu.Unlock()
		s.persist()
		logging.L().Info("fleet.sync.push.ok", "category", string(cat))
		return nil
	case http.StatusForbidden:
		setErr(ErrCapabilityNotInTier.Error())
		return ErrCapabilityNotInTier
	case http.StatusConflict:
		// stale_lww_ts. Not retried: pull the newer write instead.
		_ = readFleetError(resp)
		logging.L().Info("fleet.sync.push.stale", "category", string(cat))
		if perr := s.Pull(ctx, cat); perr != nil {
			setErr("Fleet has newer settings from another device, and fetching them failed: " + perr.Error())
			return fmt.Errorf("fleet/sync: push %s: stale write, then pull: %w", cat, perr)
		}
		return nil
	case http.StatusUnprocessableEntity:
		e := readFleetError(resp)
		switch e.Code {
		case "lww_ts_in_future":
			setErr(ErrSyncClockSkew.Error())
			return ErrSyncClockSkew
		case "unknown_category":
			st.mu.Lock()
			st.fleetRejected = true
			st.lastErr = ""
			st.mu.Unlock()
			logging.L().Warn("fleet.sync.push.unknown_category", "category", string(cat))
			return ErrSyncCategoryLocalOnly
		}
		msg := e.describe(resp.StatusCode)
		setErr(msg)
		return fmt.Errorf("fleet/sync: push %s: %s", cat, msg)
	default:
		msg := readFleetError(resp).describe(resp.StatusCode)
		setErr(msg)
		return fmt.Errorf("fleet/sync: push %s: %s", cat, msg)
	}
}

// Pull fetches the remote state for a category and applies it when
// remote-newer (LWW). Skips if the category is not enabled.
//
// Pulls are deliberately NOT behind the upload gate (owner ruling H1): a
// pull only brings the user's own data down from Fleet to this device;
// nothing local leaves the machine. The confirmation guards uploads only.
func (s *Syncer) Pull(ctx context.Context, cat SyncCategory) error {
	_, err := s.pull(ctx, cat)
	return err
}

// pull is Pull, also reporting whether Fleet's state was applied.
func (s *Syncer) pull(ctx context.Context, cat SyncCategory) (applied bool, err error) {
	if s.client == nil || s.client.isNop {
		return false, ErrFleetDisabled
	}
	s.mu.RLock()
	st, catOk := s.categories[cat]
	cfg, cfgOk := s.configs[cat]
	s.mu.RUnlock()
	if !catOk {
		return false, fmt.Errorf("fleet/sync: unknown category %q", cat)
	}
	st.mu.Lock()
	enabled := st.enabled
	lwwTS := st.lwwTS
	st.mu.Unlock()
	if !enabled {
		return false, nil
	}
	if categoryLocalOnly(cat, st) {
		return false, ErrSyncCategoryLocalOnly
	}

	// GET /api/v1/sync/<category>?since=<RFC 3339>
	q := url.Values{}
	if lwwTS > 0 {
		q.Set("since", formatLWW(lwwTS))
	}
	path := "/api/v1/sync/" + url.PathEscape(string(cat))
	if len(q) > 0 {
		path = path + "?" + q.Encode()
	}
	resp, err := s.client.Get(ctx, path)
	if err != nil {
		st.mu.Lock()
		st.lastErr = err.Error()
		st.mu.Unlock()
		return false, fmt.Errorf("fleet/sync: pull %s: %w", cat, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		// Remote is not newer — nothing to do.
		return false, nil
	}
	if resp.StatusCode == http.StatusForbidden {
		return false, ErrCapabilityNotInTier
	}
	if resp.StatusCode != http.StatusOK {
		e := readFleetError(resp)
		if resp.StatusCode == http.StatusBadRequest && e.Code == "unknown_category" {
			st.mu.Lock()
			st.fleetRejected = true
			st.mu.Unlock()
			return false, ErrSyncCategoryLocalOnly
		}
		return false, fmt.Errorf("fleet/sync: pull %s: %s", cat, e.describe(resp.StatusCode))
	}

	var remote syncStateResponse
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return false, fmt.Errorf("fleet/sync: pull %s: decode: %w", cat, err)
	}
	remoteTS := int64(remote.LWWTs)

	// LWW: apply only when remote is strictly newer. Fleet's "nothing
	// stored yet" answer has the zero lww_ts, so it never applies.
	st.mu.Lock()
	localLWW := st.lwwTS
	st.mu.Unlock()
	if remoteTS <= localLWW {
		return false, nil
	}

	// Apply.
	if cfgOk && cfg.Applier != nil {
		if err := cfg.Applier(ctx, remote.Payload); err != nil {
			st.mu.Lock()
			st.lastErr = err.Error()
			st.mu.Unlock()
			return false, fmt.Errorf("fleet/sync: apply %s: %w", cat, err)
		}
	}

	st.mu.Lock()
	st.lastPull = time.Now()
	st.lwwTS = remoteTS
	st.lastErr = ""
	st.mu.Unlock()
	s.persist()
	logging.L().Info("fleet.sync.pull.applied",
		"category", string(cat),
		"remote_ts", remoteTS,
	)
	return true, nil
}

// StartPolling begins a background 60s poll loop for all enabled categories.
// Implements FR-103 (60s) and NFR-003 (backoff on failures: 60s → 300s → 1800s).
// Context cancellation stops the loop.
func (s *Syncer) StartPolling(ctx context.Context) {
	s.once.Do(func() {
		go s.pollLoop(ctx)
	})
}

// Stop signals the debounce goroutines and poll loop to exit.
//
// Idempotent. It was not: a second call panicked with "close of closed
// channel" inside rpc.API.Shutdown (api.go:1153). Found on 2026-08-20 when
// two teardown paths both reached Shutdown in a test — but nothing about
// the defect is test-only. Shutdown is a teardown path, and a panic there
// takes the process down at the moment it is least recoverable.
func (s *Syncer) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// CollectCategory invokes the registered collector for the given category and
// returns the raw payload. Returns an error if no collector is wired.
// This method does NOT require the category to be enabled or a fleet client to
// be present — it is intended for diagnostic views and unit tests.
func (s *Syncer) CollectCategory(ctx context.Context, cat SyncCategory) (json.RawMessage, error) {
	s.mu.RLock()
	cfg, ok := s.configs[cat]
	s.mu.RUnlock()
	if !ok || cfg.Collector == nil {
		return nil, fmt.Errorf("fleet/sync: no collector registered for %q", cat)
	}
	return cfg.Collector(ctx)
}

// ApplyCategory invokes the registered applier for the given category with
// the given raw payload. Returns an error if no applier is wired.
// This method does NOT require the category to be enabled or a fleet client
// to be present — it is intended for unit tests and administrative tooling.
func (s *Syncer) ApplyCategory(ctx context.Context, cat SyncCategory, raw json.RawMessage) error {
	s.mu.RLock()
	cfg, ok := s.configs[cat]
	s.mu.RUnlock()
	if !ok || cfg.Applier == nil {
		return fmt.Errorf("fleet/sync: no applier registered for %q", cat)
	}
	return cfg.Applier(ctx, raw)
}

// RegisteredCategories returns the set of categories that have a registered
// configuration (collector or applier). Useful for diagnostics and tests.
func (s *Syncer) RegisteredCategories() []SyncCategory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SyncCategory, 0, len(s.configs))
	for cat := range s.configs {
		out = append(out, cat)
	}
	return out
}

const (
	syncBaseInterval    = 60 * time.Second
	syncBackoff1        = 300 * time.Second  // 5 min
	syncBackoff2        = 1800 * time.Second // 30 min
	syncMaxConsecErrors = 2                  // transitions per-category after 2 consecutive failures
)

func (s *Syncer) pollLoop(ctx context.Context) {
	consecutiveErrors := make(map[SyncCategory]int)
	ticker := time.NewTimer(syncBaseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			// Poll every *registered* category, not just the five built-ins
			// (AllSyncCategories()) — this is what lets a SyncKind-registered
			// category (e.g. slash_commands, WP05) join the background
			// poll loop without a hand-edited list. Behavior for the five
			// built-ins is unchanged: they are always registered by
			// registerSyncCategories before StartPolling is called.
			for _, cat := range s.RegisteredCategories() {
				if !s.IsEnabled(cat) {
					continue
				}
				// A local-only category (Fleet does not accept it) is
				// skipped, not failed: counting it would drive every
				// category's poll into backoff.
				if err := s.Pull(ctx, cat); err != nil && !errors.Is(err, ErrSyncCategoryLocalOnly) {
					consecutiveErrors[cat]++
					logging.L().Warn("fleet.sync.poll.pull_failed",
						"category", string(cat),
						"err", err.Error(),
						"consecutive", consecutiveErrors[cat],
					)
				} else {
					consecutiveErrors[cat] = 0
				}
			}
			// Compute next interval: any category with consecutive errors drives
			// the global backoff up (conservative: worst category wins).
			maxErrs := 0
			for _, n := range consecutiveErrors {
				if n > maxErrs {
					maxErrs = n
				}
			}
			var interval time.Duration
			switch {
			case maxErrs >= syncMaxConsecErrors*2:
				interval = syncBackoff2
			case maxErrs >= syncMaxConsecErrors:
				interval = syncBackoff1
			default:
				interval = syncBaseInterval
			}
			ticker.Reset(interval)
		}
	}
}
