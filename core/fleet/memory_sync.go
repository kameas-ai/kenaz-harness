// Package fleet — memory_sync.go
//
// Learned-memory sync client (memory-sync-01MEMSY01 WP07; Fleet contract
// docs/contract-harness-memory.md, live on fleet main since #183 / 0113).
// A user's global + long_term memory follows them across their own
// enrolled devices: opt-in, user-private, erasable.
//
// One cycle = pull (to exhaustion, bounded) → push (forgets first, then
// dirty chunks, ≤100 items per request). Pull before push so a push's
// base_cursor is current and a pulled tombstone is applied before this
// device re-sends the record.
//
// What never happens here:
//   - no request while the capability is absent or this device has not
//     opted in (AC-7) — the lane is "off" and idle;
//   - no embedding, no session chunk, no project chunk (project is
//     hard-disabled: memory.ProjectScopeSyncEnabled);
//   - no forget for an automatic prune (prune is device-local) and none for
//     an id Fleet cannot know (outbox coalescing);
//   - no hot loop: 429 honours Retry-After, 403 / errors back off.
//
// State: <dataDir>/fleet/memory_sync.json — the local opt-in mirror
// (enabled, scopes, consent_version), the exclusive pull cursor (persisted
// only after its page is applied), and a pending reset (persisted so a
// crash mid-reset restarts it).
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/memory"
)

// LaneMemorySync is the memory sync lane on the SyncLanes board.
const LaneMemorySync LaneName = "memory_sync"

// Memory-sync wire limits (contract §3).
const (
	memoryPushMaxItems = 100
	// memoryPushBudget keeps a push body well under Fleet's 2 MiB cap.
	memoryPushBudget = 1536 << 10
	memoryPullLimit  = 500
	// memoryPullPagesPerCycle bounds one cycle's pull; the rest follows
	// next cycle (the cursor is durable).
	memoryPullPagesPerCycle = 40
	memoryMaxFiles          = 64
	memoryMaxFileBytes      = 1024
)

// MemoryConsentVersion is the disclosure version the settings UI shows and
// sends when enabling (Fleet requires one; it records it with opted_in_at).
const MemoryConsentVersion = "2026-10-07"

// ErrMemorySyncUnavailable is returned by the settings operations when the
// lane is not wired (no store / no fleet client).
var ErrMemorySyncUnavailable = errors.New("fleet: memory sync unavailable")

// ErrMemoryConfirmRequired is returned by ForgetAll without the exact
// confirmation string.
var ErrMemoryConfirmRequired = errors.New(`fleet: forget-all requires confirm "forget-all"`)

// ── wire types (mirror kenaz-fleet service/memory/types.go) ─────────────────

type memFieldValue struct {
	V   any    `json:"v"`
	HLC string `json:"hlc"`
}

type memPushItem struct {
	Op              string                   `json:"op"`
	ID              string                   `json:"id"`
	Content         *string                  `json:"content,omitempty"`
	ContentHash     string                   `json:"content_hash,omitempty"`
	Kind            string                   `json:"kind,omitempty"`
	RetrievalWeight *float64                 `json:"retrieval_weight,omitempty"`
	TurnID          string                   `json:"turn_id,omitempty"`
	Source          string                   `json:"source,omitempty"`
	SourceTurn      string                   `json:"source_turn,omitempty"`
	ToolName        string                   `json:"tool_name,omitempty"`
	FilesRead       []string                 `json:"files_read,omitempty"`
	FilesModified   []string                 `json:"files_modified,omitempty"`
	CreatedAt       string                   `json:"created_at,omitempty"`
	Fields          map[string]memFieldValue `json:"fields,omitempty"`
	RecallOwn       *int64                   `json:"recall_own,omitempty"`
	LastAccessed    string                   `json:"last_accessed,omitempty"`
	HLC             string                   `json:"hlc,omitempty"`
}

type memPushRequest struct {
	DeviceID   string        `json:"device_id"`
	BaseCursor string        `json:"base_cursor"`
	Items      []memPushItem `json:"items"`
}

type memPushResult struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Seq          string `json:"seq,omitempty"`
	CanonicalID  string `json:"canonical_id,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
	Code         string `json:"code,omitempty"`
	Field        string `json:"field,omitempty"`
	Retry        *bool  `json:"retry,omitempty"`
}

type memPushResponse struct {
	Results     []memPushResult `json:"results"`
	CursorFloor string          `json:"cursor_floor"`
}

// memPullRecord decodes both pull shapes (live and tombstone).
type memPullRecord struct {
	ID                string   `json:"id"`
	Seq               string   `json:"seq"`
	State             string   `json:"state"`
	Reason            string   `json:"reason,omitempty"`
	SupersededBy      string   `json:"superseded_by,omitempty"`
	Content           string   `json:"content"`
	ContentHash       string   `json:"content_hash"`
	Kind              string   `json:"kind"`
	RetrievalWeight   float64  `json:"retrieval_weight"`
	TurnID            string   `json:"turn_id"`
	Source            string   `json:"source"`
	SourceTurn        string   `json:"source_turn"`
	ToolName          string   `json:"tool_name"`
	FilesRead         []string `json:"files_read"`
	FilesModified     []string `json:"files_modified"`
	CreatedAt         string   `json:"created_at"`
	Title             string   `json:"title"`
	TitleHLC          string   `json:"title_hlc"`
	Pinned            bool     `json:"pinned"`
	PinnedHLC         string   `json:"pinned_hlc"`
	ScopeKind         string   `json:"scope_kind"`
	ScopeHLC          string   `json:"scope_hlc"`
	RecallCount       int64    `json:"recall_count"`
	RecallCountOthers int64    `json:"recall_count_others"`
	LastAccessed      string   `json:"last_accessed,omitempty"`
	Aliases           []string `json:"aliases"`
}

type memPullResponse struct {
	Enabled      bool            `json:"enabled"`
	Reset        bool            `json:"reset"`
	ResetReason  string          `json:"reset_reason"`
	ErasedBefore string          `json:"erased_before"`
	Records      []memPullRecord `json:"records"`
	Cursor       string          `json:"cursor"`
	HasMore      bool            `json:"has_more"`
}

// MemorySyncUsage is Fleet's quota usage block.
type MemorySyncUsage struct {
	LiveRecords int   `json:"live_records"`
	LiveBytes   int64 `json:"live_bytes"`
	MaxRecords  int   `json:"max_records"`
	MaxBytes    int64 `json:"max_bytes"`
}

// MemorySyncSettings is GET/PUT /api/v1/memory/settings.
type MemorySyncSettings struct {
	Enabled        bool            `json:"enabled"`
	Scopes         []string        `json:"scopes"`
	ConsentVersion string          `json:"consent_version"`
	OptedInAt      string          `json:"opted_in_at"`
	Usage          MemorySyncUsage `json:"usage"`
}

type memSettingsUpdate struct {
	Enabled        *bool     `json:"enabled,omitempty"`
	Scopes         *[]string `json:"scopes,omitempty"`
	ConsentVersion string    `json:"consent_version,omitempty"`
}

type memForgetAllResponse struct {
	Erased       int    `json:"erased"`
	ErasedBefore string `json:"erased_before"`
}

type memErrEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MemorySyncError is a non-2xx answer from a memory route.
type MemorySyncError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *MemorySyncError) Error() string {
	return fmt.Sprintf("fleet: memory sync: HTTP %d %s", e.Status, e.Code)
}

// ── persisted state ─────────────────────────────────────────────────────────

type memSyncState struct {
	// Account is who the embedded sync state belongs to: "<org>:<user>", ""
	// until an account is first seen on this device (legal D164).
	Account string `json:"account,omitempty"`
	// LegacyOwner is the first account seen on this device: the only one
	// that may upload unstamped notes. Set once.
	LegacyOwner string `json:"legacy_owner,omitempty"`
	// OlderNotesAck: the legacy owner continued past the one-time line, so
	// unstamped notes may upload.
	OlderNotesAck bool `json:"older_notes_ack,omitempty"`
	// InheritedErase, InheritedOptOut: an earlier version left this request
	// unfinished when the first account was recorded. Settings shows each, in
	// legal's wording, until it's dismissed or no longer true.
	InheritedErase  bool `json:"inherited_erase,omitempty"`
	InheritedOptOut bool `json:"inherited_opt_out,omitempty"`
	// Parked holds other accounts' sync state, so an account that returns
	// resumes its own opt-in, cursor and pending erase or forget-all.
	Parked map[string]memSyncAccountState `json:"parked,omitempty"`

	memSyncAccountState
}

// memSyncAccountState is the sync state of one account on this device.
type memSyncAccountState struct {
	Enabled        bool     `json:"enabled"`
	Scopes         []string `json:"scopes,omitempty"`
	ConsentVersion string   `json:"consent_version,omitempty"`
	Cursor         string   `json:"cursor"`
	// ResetPending survives a crash mid-reset: "" | cursor_expired | erased.
	ResetPending string `json:"reset_pending,omitempty"`
	ErasedBefore string `json:"erased_before,omitempty"`
	// ResetEpoch: rows a reset page touches are stamped SyncedAt >=
	// ResetEpoch (a LOCAL time — Fleet's epoch lives inside the opaque
	// cursor); the sweep deletes synced rows before it. ResetCursor is the
	// opaque mid-reset snapshot cursor, persisted so a snapshot of any size
	// completes across cycles. ResetRestarts counts consecutive restarts.
	ResetEpoch    time.Time `json:"reset_epoch,omitempty"`
	ResetCursor   string    `json:"reset_cursor,omitempty"`
	ResetRestarts int       `json:"reset_restarts,omitempty"`
	// DisablePending: the user turned sync off but Fleet has not confirmed
	// (the PUT failed). Local sync stays off; the lane retries the PUT and
	// nothing re-enables this device until Fleet answers.
	DisablePending bool `json:"disable_pending,omitempty"`
	// ForgetAllPending: the unconfirmed opt-out also asked to delete
	// everything from Fleet. The lane's retry finishes BOTH the PUT and the
	// forget-all; Enable abandons both (the user changed their mind).
	ForgetAllPending bool `json:"forget_all_pending,omitempty"`
	// EraseCutoff: this device's clock when the person asked for forget-all. A
	// local cutoff, because Fleet's clock can sit behind notes made here, and a
	// late retry must not take notes made after the request.
	EraseCutoff string `json:"erase_cutoff,omitempty"`
	// LocalErasePending: Fleet has erased, this device hasn't finished. Holds
	// the cutoff. Persisted first, and nothing syncs while it's set, so erased
	// notes can't upload again. LocalEraseFleet is Fleet's erased_before for
	// the same erase, to recognise it when Fleet offers it back.
	LocalErasePending string `json:"local_erase_pending,omitempty"`
	LocalEraseFleet   string `json:"local_erase_fleet,omitempty"`
	// AppliedErase: the erased_before this device last finished, so an erase
	// offered on every empty-cursor pull runs once.
	AppliedErase string `json:"applied_erase,omitempty"`
}

// MemorySyncStatePath is the canonical state-file location under dataDir.
func MemorySyncStatePath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "memory_sync.json")
}

// ── the client ──────────────────────────────────────────────────────────────

// MemorySyncConfig wires a MemorySync.
type MemorySyncConfig struct {
	Client  *Client
	Store   memory.SyncStore
	Clock   *memory.HLC
	Outbox  *memory.ForgetOutbox
	DataDir string
	// Caps returns the live capability snapshot (nil = deny).
	Caps  func() *Capabilities
	Lanes *SyncLanes
	// AfterPull runs after every cycle that applied pulled records — the
	// embed_pending drain (WP06). nil = none.
	AfterPull func(ctx context.Context)
	// HomeDir enables the optional H11 hygiene: files_* paths under it are
	// sent as "~/…". "" disables it.
	HomeDir  string
	Interval time.Duration
	Now      func() time.Time
}

// MemorySync is the memory sync lane. Safe for concurrent use; one cycle
// runs at a time.
type MemorySync struct {
	cfg  MemorySyncConfig
	now  func() time.Time
	path string

	cycleMu sync.Mutex // one cycle (and one settings mutation) at a time

	mu          sync.Mutex
	st          memSyncState
	tokenKey    string // the token ensureAccount last confirmed st.Account with
	nextAttempt time.Time
	failures    int
	// pausedHold: the current backoff was set by a 403 org_paused, so the
	// OnOrgUnpaused fan-out may clear it (ResumeAfterOrgUnpause).
	pausedHold bool
	settings   *MemorySyncSettings
	settingsAt time.Time
	// serverDate is Fleet's last Date header — the reference for naming
	// how far ahead a fast OS clock is (clock_in_future).
	serverDate time.Time
	// serverOffset: how far this device's clock was from Fleet's at that answer.
	serverOffset time.Duration
	kick         chan struct{}
	// skipped holds chunks Fleet refused for a reason other than a
	// credential (or that rode a dropped 400/413 batch), keyed to the
	// SyncGen they were refused at: they are not resent until they change
	// locally (or the process restarts). Deliberately NOT persisted — only
	// secret_detected earns a durable SyncBlocked (spec: drop and log).
	skipped map[string]int64

	// runMu guards the loop lifecycle (Start / Stop). cancel and done are
	// non-nil exactly while the loop goroutine runs; stopped is set by
	// Stop (sign-out / shutdown) and cleared by the next Start, so Status
	// can say "signed_out" for a lane that is deliberately not running
	// instead of whatever its last cycle recorded.
	runMu   sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	stopped bool
}

// NewMemorySync builds the lane and loads its persisted state. A corrupt
// state file is an error (the cursor and opt-in would otherwise silently
// reset).
func NewMemorySync(cfg MemorySyncConfig) (*MemorySync, error) {
	if cfg.Store == nil || cfg.Clock == nil {
		return nil, ErrMemorySyncUnavailable
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Minute
	}
	m := &MemorySync{cfg: cfg, now: cfg.Now, kick: make(chan struct{}, 1), skipped: map[string]int64{}}
	if m.now == nil {
		m.now = time.Now
	}
	if cfg.DataDir != "" {
		m.path = MemorySyncStatePath(cfg.DataDir)
		raw, err := os.ReadFile(m.path)
		switch {
		case err == nil:
			if err := json.Unmarshal(raw, &m.st); err != nil {
				return nil, fmt.Errorf("fleet: decode %s: %w", m.path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("fleet: read %s: %w", m.path, err)
		}
	}
	return m, nil
}

// Start runs the cycle loop until ctx is cancelled or Stop is called.
// Idempotent: a Start while the loop already runs is a no-op, so the
// sign-in restart path (settings startFleetBackgroundLocked) may call it
// unconditionally. A Start after Stop starts a fresh loop.
func (m *MemorySync) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done, m.stopped = cancel, done, false
	go func() {
		defer close(done)
		t := time.NewTicker(m.cfg.Interval)
		defer t.Stop()
		for {
			m.RunOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-m.kick:
			}
		}
	}()
}

// Stop cancels the cycle loop and waits for its goroutine to exit (an
// in-flight cycle sees its context cancelled). Nil-safe and idempotent.
// Called from API.Shutdown and from settings StopFleetBackground (sign-out
// and node_removed); before it existed the loop ran — and recorded
// not_entitled against a signed-out client every cycle — until process
// exit (unwired-ledger 2026-10-07). Must not be called from the loop's own
// goroutine (it would wait on itself); no RunOnce path reaches it.
func (m *MemorySync) Stop() {
	if m == nil {
		return
	}
	m.runMu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done, m.stopped = nil, nil, true
	m.runMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Running reports whether the cycle loop is running (Start called and
// not yet stopped).
func (m *MemorySync) Running() bool {
	if m == nil {
		return false
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	return m.cancel != nil
}

// isStopped reports whether Stop was called and no Start has followed.
func (m *MemorySync) isStopped() bool {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	return m.stopped
}

// Kick requests a cycle soon (non-blocking).
func (m *MemorySync) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *MemorySync) saveStateLocked() error {
	if m.path == "" {
		return nil
	}
	raw, err := json.Marshal(m.st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // durable before the rename
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *MemorySync) state() memSyncState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *MemorySync) update(fn func(*memSyncState)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.st)
	return m.saveStateLocked()
}

func (m *MemorySync) entitled() bool {
	if m.cfg.Caps == nil {
		return false
	}
	return m.cfg.Caps().Has(CapMemorySync)
}

func (m *MemorySync) lanes() *SyncLanes { return m.cfg.Lanes }

// orgPaused reports a staff pause hold (kenaz-fleet #206) from either the
// capability snapshot or the client's last org_paused refusal. While paused
// every capability reads false; without this check the lane would say
// "not_entitled" — a tier answer — for what is a reversible hold.
func (m *MemorySync) orgPaused() (bool, string) {
	if m.cfg.Caps != nil {
		if c := m.cfg.Caps(); c != nil && c.Paused {
			return true, c.PausedCategory
		}
	}
	if m.cfg.Client != nil {
		if st := m.cfg.Client.OrgPause(); st.Paused {
			return true, st.PausedCategory
		}
	}
	return false, ""
}

// ResumeAfterOrgUnpause is the OnOrgUnpaused hook: drop any org_paused
// backoff and run a cycle now, so sync resumes without sign-in or restart.
func (m *MemorySync) ResumeAfterOrgUnpause() {
	if m == nil {
		return
	}
	m.mu.Lock()
	held := m.pausedHold
	m.pausedHold = false
	if held {
		m.failures, m.nextAttempt = 0, time.Time{}
	}
	m.mu.Unlock()
	logging.L().Info("fleet.memory_sync.org_unpaused_resume", "backoff_cleared", held)
	m.Kick()
}

// RunOnce runs one cycle if the lane is entitled, opted in and not backing
// off. It never returns an error: outcomes land on the lane board.
func (m *MemorySync) RunOnce(ctx context.Context) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	if m.cfg.Client == nil || m.cfg.Client.IsNop() {
		m.lanes().RecordOff(LaneMemorySync, "fleet_disabled")
		return
	}
	// Before anything else, including the data-rights requests below: every
	// request from here on must act for the account the state belongs to.
	if _, err := m.ensureAccount(); err != nil {
		if errors.Is(err, errNoAccount) {
			m.lanes().RecordOff(LaneMemorySync, "signed_out")
		} else {
			logging.L().Warn("fleet.memory_sync.account_state_failed", "err", err.Error())
			m.lanes().RecordFailure(LaneMemorySync, "account_state", err, 0, time.Time{})
		}
		return
	}
	if err := m.finishLocalErase(ctx); err != nil {
		// Nothing syncs until it's done: a pull would bring back what's being
		// erased, and a push would upload it.
		m.lanes().RecordFailure(LaneMemorySync, "local_erase", err, 0, time.Time{})
		return
	}
	if paused, _ := m.orgPaused(); paused {
		// A reversible staff hold, not a tier answer. Fleet keeps the
		// data-rights half of the lane open while paused (kenaz-fleet
		// #206): forget-only push batches, narrowing settings PUTs and
		// forget-all. So queued forgets are sent (forget-only batches) and
		// an unconfirmed opt-out / delete-from-Fleet is finished now, not
		// after the unpause. Nothing else runs; ResumeAfterOrgUnpause kicks
		// the lane when the hold lifts.
		m.flushForgets(ctx)
		if st := m.state(); !st.Enabled && (st.DisablePending || st.ForgetAllPending) {
			if _, err := m.finishDisable(ctx); err != nil {
				logging.L().Warn("fleet.memory_sync.disable_retry_failed", "err", err.Error())
			}
		}
		m.lanes().RecordOff(LaneMemorySync, ReasonOrgPaused)
		return
	}
	if !m.entitled() {
		m.lanes().RecordOff(LaneMemorySync, "not_entitled")
		return
	}
	if st := m.state(); !st.Enabled {
		if st.DisablePending || st.ForgetAllPending {
			// Finish the user's opt-out (and delete-from-Fleet) that Fleet
			// has not confirmed. These are the only requests a disabled
			// device makes, and they can only turn sync OFF / erase.
			if _, err := m.finishDisable(ctx); err != nil {
				logging.L().Warn("fleet.memory_sync.disable_retry_failed", "err", err.Error())
			}
		}
		m.lanes().RecordOff(LaneMemorySync, "not_opted_in")
		return
	}
	m.mu.Lock()
	wait := m.nextAttempt
	m.mu.Unlock()
	if m.now().Before(wait) {
		return
	}
	if err := m.cycle(ctx); err != nil {
		m.fail(err)
		return
	}
	m.mu.Lock()
	m.failures = 0
	m.nextAttempt = time.Time{}
	m.mu.Unlock()
	// The cycle worked, but the lane is only "ok" if nothing it depends on
	// is quietly broken: the clock must persist (a clock that cannot save
	// can hand out regressed stamps after a restart), and refused memories
	// are counted, not hidden.
	if err := m.cfg.Clock.HealthErr(); err != nil {
		m.lanes().RecordFailure(LaneMemorySync, "hlc_state", err, 0, time.Time{})
		return
	}
	if n := m.SkippedCount(); n > 0 {
		m.lanes().RecordFailure(LaneMemorySync, "items_refused",
			fmt.Errorf("Fleet refused %d memories; they stay on this device until they change", n), 0, time.Time{})
		return
	}
	m.lanes().RecordSuccess(LaneMemorySync)
}

// errDisabledOnFleet: Fleet says sync is off for this user (another device
// disabled it, or pull answered enabled:false). The local opt-in follows.
var errDisabledOnFleet = errors.New("fleet: memory sync disabled for this user on Fleet")

// errClockInFuture: Fleet refused field HLCs as more than 5 min ahead.
type errClockInFuture struct{ ahead time.Duration }

func (e errClockInFuture) Error() string {
	return fmt.Sprintf("clock_in_future: this device's clock is %s ahead of Fleet (more than %s); fix the system clock", e.ahead.Round(time.Second), memory.HLCMaxSkew)
}

func (m *MemorySync) fail(err error) {
	var me *MemorySyncError
	now := m.now()
	m.mu.Lock()
	m.failures++
	n := m.failures
	backoff := min(time.Duration(1<<min(n-1, 6))*30*time.Second, 30*time.Minute)
	reason := "push_failed"
	switch {
	case errors.Is(err, errDisabledOnFleet):
		m.mu.Unlock()
		_ = m.update(func(s *memSyncState) { s.Enabled = false })
		m.lanes().RecordOff(LaneMemorySync, "disabled_on_fleet")
		return
	case errors.Is(err, ErrNotSignedIn), errors.Is(err, ErrTokenExpired), errors.Is(err, errNoAccount):
		m.mu.Unlock()
		m.lanes().RecordOff(LaneMemorySync, "signed_out")
		return
	case IsOrgPaused(err):
		// 403 org_paused: transient. Normal backoff tiers (not the
		// hour-long tier-lapse wait), lane off with reason org_paused, and
		// the hold is cleared by ResumeAfterOrgUnpause.
		m.pausedHold = true
		m.nextAttempt = now.Add(backoff)
		m.mu.Unlock()
		logging.L().Info("fleet.memory_sync.org_paused", "paused_category", OrgPausedCategoryOf(err), "consecutive", n)
		m.lanes().RecordOff(LaneMemorySync, ReasonOrgPaused)
		return
	case errors.As(err, &me) && me.Status == http.StatusTooManyRequests:
		reason = "rate_limited"
		if me.RetryAfter > 0 {
			backoff = me.RetryAfter
		}
	case errors.As(err, &me) && me.Status == http.StatusForbidden:
		// Tier lapse / permission: Fleet answers 403 until the capability
		// returns. Back off long; the capability poller gates the next try.
		reason = me.Code
		backoff = time.Hour
	case errors.As(err, new(errClockInFuture)):
		reason = "clock_in_future"
		backoff = 5 * time.Minute
	case errors.Is(err, errResetChurn):
		reason = "reset_churn"
	}
	m.nextAttempt = now.Add(backoff)
	next := m.nextAttempt
	m.mu.Unlock()
	logging.L().Warn("fleet.memory_sync.cycle_failed", "reason", reason, "err", err.Error(), "consecutive", n)
	m.lanes().RecordFailure(LaneMemorySync, reason, err, n, next)
}

func (m *MemorySync) cycle(ctx context.Context) error {
	set, err := m.currentSettings(ctx, false)
	if err != nil {
		return err
	}
	if !set.Enabled {
		return errDisabledOnFleet
	}
	pulled, complete, err := m.pull(ctx)
	if err != nil {
		return err
	}
	if pulled && m.cfg.AfterPull != nil {
		m.cfg.AfterPull(ctx)
	}
	if !complete {
		// The feed (or a reset snapshot) has more pages than one cycle
		// pulls. Pushing now would send a mid-snapshot base_cursor — and,
		// mid-reset, could re-upload memory an erase just removed. The
		// contract order is pull to exhaustion, THEN push.
		return nil
	}
	return m.push(ctx, set)
}

// ── HTTP helpers ────────────────────────────────────────────────────────────

func (m *MemorySync) doJSON(ctx context.Context, method, path string, body, out any) error {
	// Checked per request, not per cycle: a sign-in can change the account
	// mid-cycle, and everything queued belongs to the old one.
	id, ok := m.verifiedIdentity()
	if !ok || AccountKey(id) != m.state().Account {
		return errNoAccount
	}
	ctx = requireTokenKey(ctx, id.TokenKey)
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(raw))
	}
	resp, err := m.cfg.Client.do(ctx, method, path, rd)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	m.noteServerDate(resp)
	if resp.StatusCode/100 != 2 {
		e := &MemorySyncError{Status: resp.StatusCode}
		var env memErrEnvelope
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(raw, &env) == nil {
			e.Code = env.Code
		}
		if s := resp.Header.Get("Retry-After"); s != "" {
			if secs, perr := strconv.Atoi(s); perr == nil && secs > 0 {
				e.RetryAfter = time.Duration(secs) * time.Second
			}
		}
		return e
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func (m *MemorySync) noteServerDate(resp *http.Response) {
	if d := resp.Header.Get("Date"); d != "" {
		if t, err := http.ParseTime(d); err == nil {
			m.mu.Lock()
			m.serverDate = t
			m.serverOffset = m.cfg.Clock.OffsetFrom(t)
			m.mu.Unlock()
		}
	}
}

// ── settings ────────────────────────────────────────────────────────────────

const memSettingsTTL = 10 * time.Minute

func (m *MemorySync) currentSettings(ctx context.Context, force bool) (MemorySyncSettings, error) {
	m.mu.Lock()
	if !force && m.settings != nil && m.now().Sub(m.settingsAt) < memSettingsTTL {
		s := *m.settings
		m.mu.Unlock()
		return s, nil
	}
	account := m.st.Account
	m.mu.Unlock()
	var s MemorySyncSettings
	if err := m.doJSON(ctx, http.MethodGet, "/api/v1/memory/settings", nil, &s); err != nil {
		return MemorySyncSettings{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Account != account {
		return MemorySyncSettings{}, errNoAccount // the answer is the previous account's
	}
	m.settings, m.settingsAt = &s, m.now()
	return s, nil
}

func (m *MemorySync) invalidateSettings() {
	m.mu.Lock()
	m.settings = nil
	m.mu.Unlock()
}

// enabledScopes is the push filter: Fleet's enabled scopes ∩ what this
// harness can sync (global, long_term; project only once unblocked).
func enabledScopes(set MemorySyncSettings) map[string]bool {
	out := map[string]bool{}
	for _, sc := range set.Scopes {
		if memory.IsSyncScope(sc) {
			out[sc] = true
		}
	}
	return out
}

// ── pull ────────────────────────────────────────────────────────────────────

// memoryMaxResetRestarts bounds consecutive snapshot restarts (an epoch
// change mid-snapshot — another sweep or forget-all — restarts from "").
// Past it the cycle fails, so the lane degrades and backs off instead of
// paging forever.
const memoryMaxResetRestarts = 5

// errResetChurn: the reset snapshot kept restarting.
var errResetChurn = errors.New("fleet: memory reset snapshot restarted too many times in a row (Fleet epoch keeps changing); backing off")

// pull applies pages until has_more=false (bounded per cycle) and reports
// whether anything was applied and whether the pull is caught up
// (complete). complete is the one signal that gates push: false while
// pages remain AND while a reset snapshot is in flight, so base_cursor is
// never a mid-snapshot cursor (Fleet answers a stale-epoch snapshot cursor
// with resync_required).
//
// Cursors are OPAQUE (fleet #191): a snapshot started from "" returns
// "s<seq>.<floor>.<erase>" on each has_more page — exempt from the plain
// below-floor check, so paging continues through live rows older than the
// floor — and a plain cursor >= floor on its final page. The client never
// parses either; it stores and echoes them.
//
// Reset: on reset:true the client restarts from "" with a fresh local
// epoch. Every row a reset page touches is stamped SyncedAt >= epoch; the
// mid-reset cursor is persisted after each applied page, so a snapshot of
// any size completes across cycles. After the final page, synced rows the
// snapshot never touched are swept (and, for erased, the rows the account's
// erase covers, created before erased_before). The only reset DURING a snapshot is an
// epoch change; it restarts the snapshot from "" (bounded).
func (m *MemorySync) pull(ctx context.Context) (applied, complete bool, err error) {
	device := m.cfg.Clock.NodeID()
	st := m.state()
	resetting := st.ResetPending != ""
	cursor := st.Cursor
	if resetting {
		cursor = st.ResetCursor
	}
	for page := 0; page < memoryPullPagesPerCycle; page++ {
		q := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(memoryPullLimit)}, "device_id": {device}}
		var resp memPullResponse
		if err := m.doJSON(ctx, http.MethodGet, "/api/v1/memory/pull?"+q.Encode(), nil, &resp); err != nil {
			if !isInvalidCursor(err) {
				return applied, false, err
			}
			// Fleet refuses the cursor itself (#191: a snapshot cursor whose
			// epoch is newer than the server's — e.g. a corrupt persisted
			// cursor). Retrying it would fail forever: restart the snapshot
			// from "" (bounded by the churn limit like any restart).
			logging.L().Warn("fleet.memory_sync.invalid_cursor_restart")
			resp = memPullResponse{Enabled: true, Reset: true, ResetReason: "cursor_expired"}
		}
		if !resp.Enabled {
			return applied, false, errDisabledOnFleet
		}
		if resp.ErasedBefore != "" {
			m.cfg.Clock.Observe(resp.ErasedBefore)
		}
		cur := m.state()
		// An erase this device hasn't applied yet, on a pull that didn't call it
		// a reset: a device that has never pulled can't be told any other way.
		// It runs as a reset, so the snapshot's rows are told from older ones,
		// and it upgrades any other reset already running.
		if !resp.Reset && cursor == "" && resp.ErasedBefore != "" && resp.ErasedBefore != cur.AppliedErase &&
			!(cur.ResetPending == "erased" && resp.ErasedBefore == cur.ErasedBefore) {
			resp.Reset, resp.ResetReason = true, "erased"
		}
		// This device already finished that erase. Fleet can still offer it, and
		// its cutoff is Fleet's clock, so running it again could take newer notes.
		if resp.Reset && resp.ResetReason == "erased" && resp.ErasedBefore != "" && resp.ErasedBefore == cur.AppliedErase {
			resp.ResetReason, resp.ErasedBefore = "cursor_expired", ""
		}
		if resp.Reset {
			logging.L().Info("fleet.memory_sync.reset", "reason", resp.ResetReason, "restart", resetting)
			if err := m.beginReset(ctx, orDefault(resp.ResetReason, "cursor_expired"), resp.ErasedBefore); err != nil {
				return applied, false, err
			}
			if m.state().ResetRestarts > memoryMaxResetRestarts {
				return applied, false, errResetChurn
			}
			resetting, cursor = true, ""
			continue
		}
		at := m.now().UTC()
		if resetting {
			if ep := m.state().ResetEpoch; at.Before(ep) {
				at = ep
			}
		}
		n, err := m.applyPage(ctx, resp.Records, at)
		if err != nil {
			return applied, false, err
		}
		applied = applied || n > 0
		cursor = resp.Cursor
		// Persist the (opaque) cursor only after its page is applied.
		if err := m.update(func(s *memSyncState) {
			if resetting {
				s.ResetCursor = cursor
			} else {
				s.Cursor = cursor
			}
		}); err != nil {
			return applied, false, err
		}
		if !resp.HasMore {
			if resetting {
				if err := m.finishReset(ctx, cursor); err != nil {
					return applied, false, err
				}
			}
			return applied, true, nil
		}
	}
	// More pages remain; the durable cursor resumes next cycle. Not
	// complete: no push this cycle.
	return applied, false, nil
}

// isInvalidCursor reports Fleet's 400 invalid_cursor.
func isInvalidCursor(err error) bool {
	var me *MemorySyncError
	return errors.As(err, &me) && me.Status == http.StatusBadRequest && me.Code == "invalid_cursor"
}

// beginReset persists a (re)started reset: reason, erased_before, an epoch
// strictly after every existing SyncedAt, and an empty reset cursor. A
// restart keeps an earlier "erased" (the erase must still be replayed) and
// counts toward memoryMaxResetRestarts.
func (m *MemorySync) beginReset(ctx context.Context, reason, erasedBefore string) error {
	epoch := m.now().UTC()
	if last := m.cfg.Store.LatestSyncedAt(ctx); !last.Before(epoch) {
		epoch = last.Add(time.Microsecond)
	}
	return m.update(func(s *memSyncState) {
		if s.ResetPending != "" {
			s.ResetRestarts++
		}
		if s.ResetPending == "erased" && reason != "erased" {
			reason = "erased"
		} else if erasedBefore != "" {
			s.ErasedBefore = erasedBefore
		}
		s.ResetPending, s.ResetEpoch, s.ResetCursor = reason, epoch, ""
		s.Cursor = ""
	})
}

// finishReset runs after the snapshot's final page. cursor is that page's
// cursor (Fleet guarantees it is >= the floor, #191).
func (m *MemorySync) finishReset(ctx context.Context, cursor string) error {
	st := m.state()
	n, err := m.cfg.Store.DeleteSyncedBefore(ctx, st.ResetEpoch, m.scope())
	if err != nil {
		return err
	}
	erased := 0
	if st.ResetPending == "erased" && st.ErasedBefore != "" {
		if erased, err = m.cfg.Store.DeleteCreatedBefore(ctx, m.localCutoff(st.ErasedBefore), st.ResetEpoch, m.scope()); err != nil {
			return err
		}
	}
	logging.L().Info("fleet.memory_sync.reset_done", "reason", st.ResetPending, "deleted_absent", n, "deleted_erased", erased)
	return m.update(func(s *memSyncState) {
		if s.ResetPending == "erased" && s.ErasedBefore != "" {
			s.AppliedErase = s.ErasedBefore
		}
		s.ResetPending, s.ErasedBefore, s.ResetEpoch, s.ResetCursor, s.ResetRestarts, s.Cursor = "", "", time.Time{}, "", 0, cursor
	})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (m *MemorySync) applyPage(ctx context.Context, recs []memPullRecord, at time.Time) (int, error) {
	out := make([]memory.RemoteRecord, 0, len(recs))
	var acks []string
	for _, r := range recs {
		m.cfg.Clock.Observe(r.TitleHLC)
		m.cfg.Clock.Observe(r.PinnedHLC)
		m.cfg.Clock.Observe(r.ScopeHLC)
		if r.State == "tombstone" {
			// Only an explicit forget answers a queued forget. A
			// left_sync_scope tombstone is revivable (a re-promotion
			// elsewhere brings the record back) and a superseded one is
			// not ours to vouch for: keep our forget queued so it is sent
			// and makes the tombstone absorbing (contract §3 rule 8).
			if r.Reason == memory.TombstoneForgotten && m.cfg.Outbox.Has(r.ID) {
				acks = append(acks, r.ID)
			}
			out = append(out, memory.RemoteRecord{ID: r.ID, Tombstone: true, Reason: r.Reason})
			continue
		}
		if !memory.IsSyncScope(r.ScopeKind) {
			continue // defensive: Fleet only serves synced scopes
		}
		if m.cfg.Outbox.Has(r.ID) {
			continue // deleted here; the queued forget wins — never resurrect
		}
		out = append(out, memory.RemoteRecord{
			ID:        r.ID,
			Chunk:     remoteChunk(r),
			OwnRecall: int(max(r.RecallCount-r.RecallCountOthers, 0)),
			Aliases:   r.Aliases,
		})
	}
	if err := m.cfg.Store.ApplyRemote(ctx, out, at, m.scope()); err != nil {
		return 0, err
	}
	if err := m.cfg.Outbox.Ack(acks...); err != nil {
		return 0, err
	}
	return len(out), nil
}

func remoteChunk(r memPullRecord) memory.Chunk {
	return memory.Chunk{
		ScopeKind:       r.ScopeKind,
		Content:         r.Content,
		ContentHash:     r.ContentHash,
		Kind:            r.Kind,
		RetrievalWeight: float32(r.RetrievalWeight),
		TurnID:          r.TurnID,
		Source:          r.Source,
		SourceTurn:      r.SourceTurn,
		ToolName:        r.ToolName,
		FilesRead:       r.FilesRead,
		FilesModified:   r.FilesModified,
		CreatedAt:       parseRFC3339(r.CreatedAt),
		Title:           r.Title,
		TitleHLC:        r.TitleHLC,
		Pinned:          r.Pinned,
		PinnedHLC:       r.PinnedHLC,
		ScopeHLC:        r.ScopeHLC,
		RecallOthers:    int(r.RecallCountOthers),
		LastAccessed:    parseRFC3339(r.LastAccessed),
	}
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// ── push ────────────────────────────────────────────────────────────────────

type pushEntry struct {
	item   memPushItem
	gen    int64
	forget bool
	demote bool
}

func (m *MemorySync) push(ctx context.Context, set MemorySyncSettings) error {
	scopes := enabledScopes(set)
	scope, olderOK := m.scope(), m.state().OlderNotesAck
	forgets := m.cfg.Outbox.PendingFor(scope)
	all, err := m.cfg.Store.ListAll(ctx)
	if err != nil {
		return err
	}
	var ids []string
	m.mu.Lock()
	for _, c := range all {
		if !mayPush(c, scope, olderOK) {
			continue
		}
		if gen, skip := m.skipped[c.ID]; skip && gen == c.SyncGen {
			continue
		}
		delete(m.skipped, c.ID)
		if pushCandidate(c, scopes) {
			ids = append(ids, c.ID)
		}
	}
	m.mu.Unlock()
	if len(forgets) == 0 && len(ids) == 0 {
		return nil
	}
	sent, err := m.cfg.Store.MarkSyncSent(ctx, ids, m.now().UTC())
	if err != nil {
		return err
	}
	entries := make([]pushEntry, 0, len(forgets)+len(sent))
	for _, f := range forgets {
		entries = append(entries, pushEntry{item: memPushItem{Op: "forget", ID: f.ID, HLC: f.HLC}, forget: true})
	}
	for _, id := range ids {
		c, ok := sent[id]
		if !ok {
			continue // deleted before it could be sent: coalesced
		}
		item, demote := m.buildItem(c, scopes)
		entries = append(entries, pushEntry{item: item, gen: c.SyncGen, demote: demote})
	}
	for start := 0; start < len(entries); {
		end, size := start, 0
		for end < len(entries) && end-start < memoryPushMaxItems {
			raw, _ := json.Marshal(entries[end].item)
			if end > start && size+len(raw) > memoryPushBudget {
				break
			}
			size += len(raw)
			end++
		}
		if err := m.pushBatch(ctx, entries[start:end]); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// olderNoteToUpload: an unstamped note Fleet doesn't hold yet. This is what the
// one-time line is about. An unstamped note already on Fleet can still send its
// changes, and a demotion must always go: leaving sync never waits (D164).
func olderNoteToUpload(c memory.Chunk) bool {
	return c.AccountUnstamped() && memory.IsSyncScope(c.ScopeKind) && c.SyncedAt.IsZero()
}

// mayPush is the account half of the push decision (D164): never another
// account's note, never a note made signed out, and an older note only for
// the legacy owner once they've continued past the one-time line.
func mayPush(c memory.Chunk, scope memory.AccountScope, olderOK bool) bool {
	return scope.Owns(c) && (olderOK || !olderNoteToUpload(c))
}

// pushCandidate: a chunk pushes when it is in an enabled sync scope and is
// new to Fleet or changed (and not blocked); or when it left the sync
// scopes while Fleet may still hold it (the demotion push). A
// credential-blocked chunk still pushes its demotion: an edit can be
// refused while the record itself is live on Fleet, and leaving sync must
// always be possible.
func pushCandidate(c memory.Chunk, scopes map[string]bool) bool {
	if memory.IsSyncScope(c.ScopeKind) {
		return c.SyncBlocked == "" && scopes[c.ScopeKind] && (c.SyncDirty || c.SyncedAt.IsZero())
	}
	return c.SyncDirty && c.FleetMayKnow() // demotion (to session or project)
}

// skip records a refused chunk for the in-memory, change-scoped skip set.
func (m *MemorySync) skip(id string, gen int64) {
	m.mu.Lock()
	m.skipped[id] = gen
	m.mu.Unlock()
}

// SkippedCount is how many chunks Fleet refused this process (not
// counting credential blocks, which are durable SyncBlocked).
func (m *MemorySync) SkippedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.skipped)
}

// wireScope maps a local scope to the scope_kind sent. While project sync
// is hard-disabled Fleet rejects scope_kind=project outright
// (scope_not_supported_yet) — even for a demotion — which would leave the
// record live on every other device. A demotion into project is therefore
// sent as "session": the only meaning Fleet takes from either value is
// "left the synced scopes" (tombstone left_sync_scope; tombstones carry no
// scope on the pull feed). Unblocked with memory.ProjectScopeSyncEnabled.
func wireScope(kind string) string {
	if kind == memory.ScopeKindProject && !memory.ProjectScopeSyncEnabled {
		return memory.ScopeKindSession
	}
	return kind
}

func (m *MemorySync) buildItem(c memory.Chunk, scopes map[string]bool) (memPushItem, bool) {
	fields := map[string]memFieldValue{
		"scope_kind": {V: wireScope(c.ScopeKind), HLC: c.ScopeHLC},
	}
	if !memory.IsSyncScope(c.ScopeKind) {
		// Demotion: only the scope change; Fleet tombstones the record.
		return memPushItem{Op: "upsert", ID: c.ID, Fields: fields}, true
	}
	if c.TitleHLC != "" {
		fields["title"] = memFieldValue{V: c.Title, HLC: c.TitleHLC}
	}
	if c.PinnedHLC != "" {
		fields["pinned"] = memFieldValue{V: c.Pinned, HLC: c.PinnedHLC}
	}
	w := float64(c.RetrievalWeight)
	if !(w > 0 && w <= 10) {
		w = 1
	}
	own := int64(c.RecallOwn)
	item := memPushItem{
		Op:              "upsert",
		ID:              c.ID,
		ContentHash:     c.ContentHash,
		Kind:            orDefault(c.Kind, "raw"),
		RetrievalWeight: &w,
		TurnID:          c.TurnID,
		Source:          c.Source,
		SourceTurn:      c.SourceTurn,
		ToolName:        c.ToolName,
		FilesRead:       m.normFiles(c.FilesRead),
		FilesModified:   m.normFiles(c.FilesModified),
		Fields:          fields,
		RecallOwn:       &own,
	}
	if c.ContentHash == "" {
		item.ContentHash = memory.HashContent(c.Content)
	}
	if !c.CreatedAt.IsZero() {
		item.CreatedAt = c.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !c.LastAccessed.IsZero() {
		item.LastAccessed = c.LastAccessed.UTC().Format(time.RFC3339Nano)
	}
	if c.SyncedAt.IsZero() {
		// Fleet may hold no live record for this id: content is required.
		content := c.Content
		item.Content = &content
	}
	return item, false
}

// normFiles applies the H11 hygiene ($HOME → ~) and Fleet's caps (≤64
// entries of ≤1024 B) so an over-long list is trimmed rather than the
// whole record rejected invalid_field.
func (m *MemorySync) normFiles(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(in), memoryMaxFiles))
	home := strings.TrimRight(m.cfg.HomeDir, string(os.PathSeparator))
	for _, f := range in {
		if home != "" && (f == home || strings.HasPrefix(f, home+string(os.PathSeparator))) {
			f = "~" + strings.TrimPrefix(f, home)
		}
		if len(f) > memoryMaxFileBytes {
			continue
		}
		out = append(out, f)
		if len(out) == memoryMaxFiles {
			break
		}
	}
	return out
}

func (m *MemorySync) pushBatch(ctx context.Context, entries []pushEntry) error {
	req := memPushRequest{DeviceID: m.cfg.Clock.NodeID(), BaseCursor: m.state().Cursor}
	for _, e := range entries {
		req.Items = append(req.Items, e.item)
	}
	var resp memPushResponse
	err := m.doJSON(ctx, http.MethodPost, "/api/v1/memory/push", req, &resp)
	if isInvalidCursor(err) {
		// base_cursor refused (a corrupt cursor): resync rather than drop.
		if rerr := m.beginReset(ctx, "cursor_expired", ""); rerr != nil {
			return rerr
		}
		return fmt.Errorf("fleet: memory push: invalid base_cursor; resyncing")
	}
	var me *MemorySyncError
	if errors.As(err, &me) && (me.Status == http.StatusBadRequest || me.Status == http.StatusRequestEntityTooLarge) {
		// An envelope fault is a client bug, not transient: drop the
		// batch and log (spec FR-6), rather than resend the same bytes
		// every cycle. Nothing is blocked durably.
		logging.L().Error("fleet.memory_sync.batch_dropped", "status", me.Status, "code", me.Code, "items", len(entries))
		return m.dropBatch(ctx, entries, me)
	}
	if err != nil {
		return err
	}
	return m.applyResults(ctx, entries, resp)
}

func (m *MemorySync) dropBatch(_ context.Context, entries []pushEntry, me *MemorySyncError) error {
	var acks []string
	for _, e := range entries {
		if e.forget {
			// A 400 means the forget itself is malformed and will never
			// be accepted; a 413 is the upserts' size — keep forgets
			// queued (they are tiny and privacy-relevant).
			if me.Status == http.StatusBadRequest {
				acks = append(acks, e.item.ID)
			}
			continue
		}
		m.skip(e.item.ID, e.gen)
	}
	return m.cfg.Outbox.Ack(acks...)
}

func (m *MemorySync) applyResults(ctx context.Context, entries []pushEntry, resp memPushResponse) error {
	byID := make(map[string]pushEntry, len(entries))
	for _, e := range entries {
		byID[e.item.ID] = e
	}
	var (
		outs        []memory.SyncOutcome
		acks        []string
		clockFuture bool
		retryLater  error
	)
	for _, r := range resp.Results {
		e, ok := byID[r.ID]
		if !ok {
			continue
		}
		if e.forget {
			// forgotten (or any final answer) ⇒ done; retry:true ⇒ keep.
			if r.Status != "rejected" || r.Retry == nil || !*r.Retry {
				acks = append(acks, r.ID)
			}
			continue
		}
		o := memory.SyncOutcome{ID: r.ID, Gen: e.gen}
		switch r.Status {
		case "accepted", "unchanged":
			o.Kind = memory.OutcomeSynced
			if e.demote {
				o.Kind = memory.OutcomeLeftScope
			}
		case "merged":
			o.Kind, o.CanonicalID = memory.OutcomeRekey, r.CanonicalID
		case "superseded", "forgotten":
			if e.demote {
				o.Kind = memory.OutcomeLeftScope // gone on Fleet; keep ours
			} else {
				o.Kind = memory.OutcomeDelete
			}
		case "rejected":
			retry := r.Retry != nil && *r.Retry
			switch {
			case r.Code == "clock_in_future":
				clockFuture = true
				continue // stays dirty; lane degraded until the clock is fixed
			case r.Code == "scope_not_enabled":
				m.invalidateSettings()
				continue // scopes changed on Fleet; re-read next cycle
			case r.Code == "resync_required":
				if err := m.beginReset(ctx, "cursor_expired", ""); err != nil {
					return err
				}
				retryLater = fmt.Errorf("fleet: memory push: resync required")
				continue
			case r.Code == "sync_disabled":
				return errDisabledOnFleet
			case r.Code == "invalid_field" && r.Field == "content" && !e.demote:
				o.Kind = memory.OutcomeResendContent
			case e.demote:
				o.Kind = memory.OutcomeLeftScope // nothing more to send
			case retry:
				retryLater = fmt.Errorf("fleet: memory push: %s (retry)", r.Code)
				continue
			case r.Code == "secret_detected":
				// The one durable block: credential-shaped content must
				// never be resent; the user sees it in Learned.
				o.Kind, o.Code = memory.OutcomeBlocked, r.Code
			default:
				// Other retry:false refusals are dropped and logged; the
				// chunk is resent only after it changes locally.
				logging.L().Warn("fleet.memory_sync.item_rejected", "code", r.Code, "field", r.Field)
				m.skip(r.ID, e.gen)
				continue
			}
		default:
			continue
		}
		outs = append(outs, o)
	}
	if err := m.cfg.Outbox.Ack(acks...); err != nil {
		return err
	}
	if err := m.cfg.Store.ApplySyncOutcomes(ctx, outs, m.now().UTC(), m.scope()); err != nil {
		return err
	}
	if clockFuture {
		m.mu.Lock()
		ref := m.serverDate
		m.mu.Unlock()
		if ref.IsZero() {
			ref = m.now()
		}
		return errClockInFuture{ahead: max(m.cfg.Clock.AheadOf(ref), memory.HLCMaxSkew)}
	}
	return retryLater
}

// ── settings operations (WP08 surface) ──────────────────────────────────────

// MemorySyncStatus is the settings-panel readout.
type MemorySyncStatus struct {
	Entitled bool
	// OrgPaused / PausedCategory: a staff pause hold is on the org. The
	// panel shows the paused copy (never an upsell) and keeps the
	// data-rights actions (disable + delete from Fleet) enabled.
	OrgPaused      bool
	PausedCategory string
	LocalEnabled   bool
	Fleet          *MemorySyncSettings // nil when not fetched (not entitled / error)
	FleetError     string
	BlockedCount   int
	PendingPush    int
	// OlderNotes: unstamped notes held back until the legacy owner continues
	// past the one-time line (D164). OlderNotesAccount names that account.
	OlderNotes        int
	OlderNotesAccount string
	// LocalErasePending: erased from Fleet, not yet from this device.
	LocalErasePending bool
	// UnresolvedNotes: notes written while the keychain couldn't be read. They
	// stay on this device.
	UnresolvedNotes int
	// InheritedErase, InheritedOptOut: see memSyncState.
	InheritedErase  bool
	InheritedOptOut bool
	Lane            LaneSnapshot
}

// Status reads the local state, the lane, and (when entitled) Fleet's
// settings — a user-initiated request, never made by the idle lane.
func (m *MemorySync) Status(ctx context.Context) MemorySyncStatus {
	// A running cycle does this itself; don't wait for it to finish.
	if m.cycleMu.TryLock() {
		_, _ = m.ensureAccount()
		m.cycleMu.Unlock()
	}
	st := m.state()
	scope, current := m.scope(), m.currentAccount() != "" && m.currentAccount() == st.Account
	var older []string // scope kinds of the older notes waiting on the line
	out := MemorySyncStatus{Entitled: m.entitled(), LocalEnabled: st.Enabled, Lane: m.lanes().Snapshot(LaneMemorySync)}
	out.LocalErasePending = st.LocalErasePending != ""
	out.OrgPaused, out.PausedCategory = m.orgPaused()
	if m.isStopped() {
		// A lane stopped by sign-out (or node_removed) records nothing; the
		// panel names why it is off rather than echoing the default-deny
		// capability answer (not_entitled) the signed-out client gives.
		out.Lane = LaneSnapshot{Status: LaneOff, Reason: "signed_out", LastSuccessAt: out.Lane.LastSuccessAt}
	}
	if all, err := m.cfg.Store.ListAll(ctx); err == nil {
		for _, c := range all {
			if c.Account == memory.AccountUnresolved && memory.IsSyncScope(c.ScopeKind) {
				out.UnresolvedNotes++
			}
			// Only this account's notes: the panel must not count another
			// account's, or say they're waiting to sync here.
			if !current || !scope.Owns(c) {
				continue
			}
			switch {
			case c.SyncBlocked != "":
				out.BlockedCount++
			case !memory.IsSyncScope(c.ScopeKind) || !(c.SyncDirty || c.SyncedAt.IsZero()):
			case !st.OlderNotesAck && olderNoteToUpload(c):
				// Counted against the enabled scopes below: N must be what
				// would really upload.
				older = append(older, c.ScopeKind)
			default:
				out.PendingPush++
			}
		}
	}
	// Fleet's scopes when it answers, because that's what push uses; the last
	// known ones when it doesn't.
	countOlder := func(enabled func(string) bool) {
		out.OlderNotes = 0
		for _, kind := range older {
			if enabled(kind) {
				out.OlderNotes++
			}
		}
		out.OlderNotesAccount = ""
		if out.OlderNotes > 0 {
			out.OlderNotesAccount = m.accountLabel()
		}
	}
	countOlder(func(kind string) bool { return slices.Contains(st.Scopes, kind) })
	// Only to the account they were recorded for, and only once the panel can
	// show them: it's hidden without the capability, unless the org is paused.
	// The opt-out line offers to turn sync back on, which a paused org can't.
	if current && st.Account == st.LegacyOwner {
		out.InheritedErase = st.InheritedErase && (out.Entitled || out.OrgPaused)
		out.InheritedOptOut = st.InheritedOptOut && !st.Enabled && out.Entitled && !out.OrgPaused
	}
	if !out.Entitled || !current || m.cfg.Client == nil || m.cfg.Client.IsNop() {
		return out
	}
	set, err := m.currentSettings(ctx, true)
	if err != nil {
		out.FleetError = err.Error()
		return out
	}
	out.Fleet = &set
	fleetScopes := enabledScopes(set)
	countOlder(func(kind string) bool { return fleetScopes[kind] })
	// Fleet's opt-in is user-level. A disable elsewhere reaches this device
	// on its own (the lane sees enabled:false / sync_disabled and turns
	// itself off); a RE-enable elsewhere is picked up only here, when the
	// user opens this panel — intended: consent is per device, so a device
	// never starts uploading again without its user looking (F11, blessed).
	// An unconfirmed local opt-out (DisablePending) is never undone here.
	// Nor an unfinished erase: sync must stay off until it has run, or a later
	// plain "Turn off" would run it, and erase this device, unasked.
	if cur := m.state(); set.Enabled != out.LocalEnabled && !cur.DisablePending && !cur.ForgetAllPending {
		_ = m.update(func(s *memSyncState) {
			if s.Account == st.Account { // the account may have changed during the read
				s.Enabled, s.Scopes = set.Enabled, set.Scopes
				// Turned back on from another device: the same as turning it
				// on here, or the line would return if they turn it off later.
				s.InheritedOptOut = s.InheritedOptOut && !(set.Enabled && s.Account == s.LegacyOwner)
			}
		})
		if set.Enabled {
			out.InheritedOptOut = false
		}
		out.LocalEnabled = set.Enabled
	}
	return out
}

// Enable opts this user in on Fleet (PUT settings with consent) and this
// device locally, then kicks a cycle. scopes must be ⊆ {long_term, global}.
func (m *MemorySync) Enable(ctx context.Context, scopes []string, consentVersion string) (MemorySyncSettings, error) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	if _, err := m.ensureAccount(); err != nil {
		return MemorySyncSettings{}, err
	}
	if paused, cat := m.orgPaused(); paused {
		// Enabling / widening is refused while paused; say so, not "tier".
		return MemorySyncSettings{}, &OrgPausedError{PausedCategory: NormalizePausedCategory(cat)}
	}
	if !m.entitled() {
		return MemorySyncSettings{}, fmt.Errorf("%w: %s", ErrCapabilityNotInTier, CapMemorySync)
	}
	if consentVersion == "" {
		return MemorySyncSettings{}, errors.New("fleet: memory sync: consent version required")
	}
	clean := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s != memory.ScopeKindGlobal && s != memory.ScopeKindLongTerm {
			return MemorySyncSettings{}, fmt.Errorf("fleet: memory sync: scope %q is not supported", s)
		}
		clean = append(clean, s)
	}
	if len(clean) == 0 {
		return MemorySyncSettings{}, errors.New("fleet: memory sync: at least one scope required")
	}
	on := true
	var set MemorySyncSettings
	if err := m.doJSON(ctx, http.MethodPut, "/api/v1/memory/settings",
		memSettingsUpdate{Enabled: &on, Scopes: &clean, ConsentVersion: consentVersion}, &set); err != nil {
		return MemorySyncSettings{}, err
	}
	m.mu.Lock()
	m.settings, m.settingsAt = &set, m.now()
	m.nextAttempt, m.failures = time.Time{}, 0
	m.mu.Unlock()
	if err := m.update(func(s *memSyncState) {
		s.Enabled, s.Scopes, s.ConsentVersion = true, clean, consentVersion
		// A re-enable supersedes any unconfirmed opt-out: without this the
		// lane's retry PUT enabled=false could overwrite this (or another
		// device's) re-enable, and Status would stop mirroring Fleet.
		s.DisablePending, s.ForgetAllPending, s.EraseCutoff = false, false, ""
		// Sync is back on, so the line is no longer true. Only the account it
		// was recorded for can end it: another account's sync says nothing.
		s.InheritedOptOut = s.InheritedOptOut && s.Account != s.LegacyOwner
	}); err != nil {
		return set, err
	}
	m.Kick()
	return set, nil
}

// Disable turns memory sync off — for the user on Fleet and on this device
// — and, with deleteFromFleet, then erases everything Fleet holds
// (forget-all; confirm must be exactly "forget-all"). It is ONE operation
// under the cycle lock, ordered disable-first, so no ticker cycle can run
// between the erase and the opt-out and re-upload the memory the user just
// deleted:
//
//  1. local Enabled=false (+ DisablePending) is persisted BEFORE any
//     request, so whatever fails below, this device never syncs again
//     until the user re-enables;
//  2. queued forgets are flushed (deleting is always allowed, and a forget
//     left queued while off would never be sent);
//  3. PUT enabled=false — a failure returns the error and leaves
//     DisablePending set; the lane retries the PUT (never re-enables);
//  4. POST forget-all, then erase this device's copy (legal D157), persisted
//     first and pausing sync until it's done, so nothing erased can upload
//     again.
//
// forget-all is allowed even after a tier lapse (Fleet gates it on
// permission only), so this needs no capability. Returns the erased count.
func (m *MemorySync) Disable(ctx context.Context, deleteFromFleet bool, confirm string) (int, error) {
	if deleteFromFleet && confirm != "forget-all" {
		return 0, ErrMemoryConfirmRequired
	}
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	// With no account known nothing is sent, because the tokens could be
	// anyone's. An opt-out is recorded on the state it belongs to and finishes
	// when that account returns. An erase is refused: it can't be undone, and
	// whoever is at the device may not be that account.
	_, acctErr := m.ensureAccount()
	if acctErr != nil && deleteFromFleet {
		return 0, acctErr
	}
	var cutoff string
	if deleteFromFleet {
		cutoff = m.cfg.Clock.Tick()
	}
	if err := m.update(func(s *memSyncState) {
		s.Enabled, s.DisablePending = false, true
		s.ForgetAllPending = s.ForgetAllPending || deleteFromFleet
		if deleteFromFleet {
			s.EraseCutoff = cutoff
		}
	}); err != nil {
		return 0, err
	}
	m.invalidateSettings()
	m.lanes().RecordOff(LaneMemorySync, "not_opted_in")
	if m.cfg.Client == nil || m.cfg.Client.IsNop() {
		if deleteFromFleet {
			return 0, ErrFleetDisabled
		}
		return 0, nil
	}
	if acctErr != nil {
		return 0, nil
	}
	m.flushForgets(ctx)
	return m.finishDisable(ctx)
}

// finishDisable completes a persisted opt-out: PUT enabled=false while
// DisablePending, then forget-all while ForgetAllPending. Each flag is
// cleared only once Fleet confirms its step, so a failure anywhere is
// retried by the lane (both steps, in order). The confirmation literal was
// checked when the user asked (Disable).
func (m *MemorySync) finishDisable(ctx context.Context) (int, error) {
	if m.state().DisablePending {
		// A narrowing PUT (sync off) is accepted while the org is paused
		// (kenaz-fleet #206), so this needs no pause special case.
		if err := m.putDisabled(ctx); err != nil {
			return 0, err
		}
	}
	if !m.state().ForgetAllPending {
		return 0, nil
	}
	var resp memForgetAllResponse
	if err := m.doJSON(ctx, http.MethodPost, "/api/v1/memory/forget-all", map[string]string{"confirm": "forget-all"}, &resp); err != nil {
		return 0, err
	}
	pend := m.cfg.Outbox.PendingFor(m.scope())
	ids := make([]string, 0, len(pend))
	for _, p := range pend {
		ids = append(ids, p.ID)
	}
	if err := m.cfg.Outbox.Ack(ids...); err != nil {
		return resp.Erased, err
	}
	if resp.ErasedBefore != "" {
		m.cfg.Clock.Observe(resp.ErasedBefore)
	}
	// A request with no cutoff was made on a version that promised only Fleet's
	// copy would go. It finishes as promised: this device keeps its notes, and
	// they can upload again if sync returns.
	if m.state().EraseCutoff == "" {
		if err := m.cfg.Store.ClearSyncMarkers(ctx, m.scope()); err != nil {
			return resp.Erased, err
		}
		return resp.Erased, m.update(func(s *memSyncState) {
			s.AppliedErase = resp.ErasedBefore
			s.Cursor, s.ResetPending, s.ErasedBefore, s.ResetEpoch, s.ResetCursor, s.ResetRestarts = "", "", "", time.Time{}, "", 0
			s.ForgetAllPending = false
			s.InheritedErase = s.InheritedErase && s.Account != s.LegacyOwner
		})
	}
	if err := m.update(func(s *memSyncState) {
		s.LocalErasePending, s.LocalEraseFleet = s.EraseCutoff, resp.ErasedBefore
		s.Cursor, s.ResetPending, s.ErasedBefore, s.ResetEpoch, s.ResetCursor, s.ResetRestarts = "", "", "", time.Time{}, "", 0
		s.ForgetAllPending, s.EraseCutoff = false, ""
		// Done, if it was this account's erase. Another account's says nothing.
		s.InheritedErase = s.InheritedErase && s.Account != s.LegacyOwner
	}); err != nil {
		return resp.Erased, err
	}
	return resp.Erased, m.finishLocalErase(ctx)
}

// localCutoff turns Fleet's erased_before into this device's clock. A clock ahead
// of Fleet's stamps notes later than Fleet's time, so they'd look newer than an
// erase they came before, and upload; a clock behind would take notes made after
// it. Good to about a second: Fleet's Date header has no finer grain.
func (m *MemorySync) localCutoff(erasedBefore string) string {
	m.mu.Lock()
	offset := m.serverOffset
	m.mu.Unlock()
	return memory.ShiftHLC(erasedBefore, offset)
}

// finishLocalErase erases this device's copy of what Fleet has erased. Sync is off
// and paused until it's done, so every row it covers is older than the cutoff by
// this device's clock, and no snapshot needs telling apart.
func (m *MemorySync) finishLocalErase(ctx context.Context) error {
	st := m.state()
	if st.LocalErasePending == "" {
		return nil
	}
	scope := m.scope()
	n, err := m.cfg.Store.DeleteCreatedBefore(ctx, st.LocalErasePending, m.now().Add(24*time.Hour), scope)
	if err != nil {
		logging.L().Warn("fleet.memory_sync.local_erase_failed", "err", err.Error())
		return fmt.Errorf("fleet: memory sync: erased from Fleet, but not yet from this device: %w", err)
	}
	// What's left is newer than the erase, so it uploads like any new note.
	if err := m.cfg.Store.ClearSyncMarkers(ctx, scope); err != nil {
		return err
	}
	logging.L().Info("fleet.memory_sync.local_erase_done", "deleted", n)
	return m.update(func(s *memSyncState) {
		s.AppliedErase = s.LocalEraseFleet
		if s.AppliedErase == "" {
			s.AppliedErase = s.LocalErasePending
		}
		s.LocalErasePending, s.LocalEraseFleet = "", ""
	})
}

// flushForgets sends every queued forget (best effort; failures stay queued).
func (m *MemorySync) flushForgets(ctx context.Context) {
	pend := m.cfg.Outbox.PendingFor(m.scope())
	if len(pend) == 0 {
		return
	}
	entries := make([]pushEntry, 0, len(pend))
	for _, f := range pend {
		entries = append(entries, pushEntry{item: memPushItem{Op: "forget", ID: f.ID, HLC: f.HLC}, forget: true})
	}
	for start := 0; start < len(entries); start += memoryPushMaxItems {
		end := min(start+memoryPushMaxItems, len(entries))
		if err := m.pushBatch(ctx, entries[start:end]); err != nil {
			logging.L().Warn("fleet.memory_sync.forget_flush_failed", "err", err.Error())
			return
		}
	}
}

// putDisabled sends PUT enabled=false and clears DisablePending on success.
func (m *MemorySync) putDisabled(ctx context.Context) error {
	off := false
	if err := m.doJSON(ctx, http.MethodPut, "/api/v1/memory/settings", memSettingsUpdate{Enabled: &off}, nil); err != nil {
		return err
	}
	return m.update(func(s *memSyncState) { s.DisablePending = false })
}
