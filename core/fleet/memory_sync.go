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
	Enabled        bool     `json:"enabled"`
	Scopes         []string `json:"scopes,omitempty"`
	ConsentVersion string   `json:"consent_version,omitempty"`
	Cursor         string   `json:"cursor"`
	// ResetPending survives a crash mid-reset: "" | cursor_expired | erased.
	ResetPending string `json:"reset_pending,omitempty"`
	ErasedBefore string `json:"erased_before,omitempty"`
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
	nextAttempt time.Time
	failures    int
	settings    *MemorySyncSettings
	settingsAt  time.Time
	// serverDate is Fleet's last Date header — the reference for naming
	// how far ahead a fast OS clock is (clock_in_future).
	serverDate time.Time
	kick       chan struct{}
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
	m := &MemorySync{cfg: cfg, now: cfg.Now, kick: make(chan struct{}, 1)}
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

// Start runs the cycle loop until ctx is cancelled.
func (m *MemorySync) Start(ctx context.Context) {
	go func() {
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
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
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

// RunOnce runs one cycle if the lane is entitled, opted in and not backing
// off. It never returns an error: outcomes land on the lane board.
func (m *MemorySync) RunOnce(ctx context.Context) {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	if m.cfg.Client == nil || m.cfg.Client.IsNop() {
		m.lanes().RecordOff(LaneMemorySync, "fleet_disabled")
		return
	}
	if !m.entitled() {
		m.lanes().RecordOff(LaneMemorySync, "not_entitled")
		return
	}
	if !m.state().Enabled {
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
	case errors.Is(err, ErrNotSignedIn), errors.Is(err, ErrTokenExpired):
		m.mu.Unlock()
		m.lanes().RecordOff(LaneMemorySync, "signed_out")
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
	pulled, err := m.pull(ctx)
	if err != nil {
		return err
	}
	if pulled && m.cfg.AfterPull != nil {
		m.cfg.AfterPull(ctx)
	}
	if m.state().ResetPending != "" {
		// A reset snapshot is still incomplete (more pages than one
		// cycle pulls). Pushing now could re-upload memory an erase just
		// removed; the contract order is reset to exhaustion, THEN push.
		return nil
	}
	return m.push(ctx, set)
}

// ── HTTP helpers ────────────────────────────────────────────────────────────

func (m *MemorySync) doJSON(ctx context.Context, method, path string, body, out any) error {
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
	m.mu.Unlock()
	var s MemorySyncSettings
	if err := m.doJSON(ctx, http.MethodGet, "/api/v1/memory/settings", nil, &s); err != nil {
		return MemorySyncSettings{}, err
	}
	m.mu.Lock()
	m.settings, m.settingsAt = &s, m.now()
	m.mu.Unlock()
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

// pull applies pages until has_more=false (bounded per cycle). It returns
// whether any record was applied. A reset (cursor below the floor, or a
// forget-all) restarts from "" and, once the snapshot is complete, deletes
// synced local chunks absent from it — and for erased, every sync-scope
// chunk created before erased_before.
func (m *MemorySync) pull(ctx context.Context) (bool, error) {
	device := m.cfg.Clock.NodeID()
	applied := false
	st := m.state()
	resetting := st.ResetPending != ""
	cursor := st.Cursor
	if resetting {
		cursor = ""
	}
	keep := map[string]bool{}
	for page := 0; page < memoryPullPagesPerCycle; page++ {
		q := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(memoryPullLimit)}, "device_id": {device}}
		var resp memPullResponse
		if err := m.doJSON(ctx, http.MethodGet, "/api/v1/memory/pull?"+q.Encode(), nil, &resp); err != nil {
			return applied, err
		}
		if !resp.Enabled {
			return applied, errDisabledOnFleet
		}
		if resp.Reset {
			if err := m.update(func(s *memSyncState) {
				s.ResetPending = orDefault(resp.ResetReason, "cursor_expired")
				if resp.ErasedBefore != "" {
					s.ErasedBefore = resp.ErasedBefore
				}
				s.Cursor = ""
			}); err != nil {
				return applied, err
			}
			logging.L().Info("fleet.memory_sync.reset", "reason", resp.ResetReason)
			resetting, cursor, keep = true, "", map[string]bool{}
			continue
		}
		n, err := m.applyPage(ctx, resp.Records, keep)
		if err != nil {
			return applied, err
		}
		applied = applied || n > 0
		cursor = resp.Cursor
		if !resetting {
			// Persist the cursor only after its page is applied.
			if err := m.update(func(s *memSyncState) { s.Cursor = cursor }); err != nil {
				return applied, err
			}
		}
		if !resp.HasMore {
			if resetting {
				if err := m.finishReset(ctx, keep, cursor); err != nil {
					return applied, err
				}
			}
			return applied, nil
		}
	}
	// More pages remain: a non-reset pull resumes next cycle from the
	// durable cursor; a reset restarts (its snapshot must be complete
	// before anything is deleted).
	return applied, nil
}

func (m *MemorySync) finishReset(ctx context.Context, keep map[string]bool, cursor string) error {
	// Fleet answers a reset snapshot's last page with cursor = the last
	// record's seq, which after a tombstone sweep can sit BELOW the cursor
	// floor (live rows older than the newest swept tombstone) — the next
	// pull would reset again, forever. The snapshot is complete, so no row
	// exists between its last seq and the floor: advance to the floor,
	// learned from an empty push (the only route that reports it).
	floor, err := m.probeFloor(ctx)
	if err != nil {
		return err
	}
	if seqLess(cursor, floor) {
		cursor = floor
	}
	st := m.state()
	n, err := m.cfg.Store.DeleteSyncedExcept(ctx, keep)
	if err != nil {
		return err
	}
	erased := 0
	if st.ResetPending == "erased" && st.ErasedBefore != "" {
		if erased, err = m.cfg.Store.DeleteCreatedBefore(ctx, st.ErasedBefore); err != nil {
			return err
		}
	}
	logging.L().Info("fleet.memory_sync.reset_done", "reason", st.ResetPending, "deleted_absent", n, "deleted_erased", erased)
	return m.update(func(s *memSyncState) {
		s.ResetPending, s.ErasedBefore, s.Cursor = "", "", cursor
	})
}

// probeFloor asks Fleet for the user's cursor floor with an item-less push.
func (m *MemorySync) probeFloor(ctx context.Context) (string, error) {
	var resp memPushResponse
	req := memPushRequest{DeviceID: m.cfg.Clock.NodeID(), BaseCursor: "", Items: []memPushItem{}}
	if err := m.doJSON(ctx, http.MethodPost, "/api/v1/memory/push", req, &resp); err != nil {
		return "", err
	}
	if resp.CursorFloor == "0" {
		return "", nil
	}
	return resp.CursorFloor, nil
}

// seqLess compares two Fleet cursors ("" = before everything).
func seqLess(a, b string) bool {
	if b == "" {
		return false
	}
	if a == "" {
		return true
	}
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	return aerr == nil && berr == nil && ai < bi
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (m *MemorySync) applyPage(ctx context.Context, recs []memPullRecord, keep map[string]bool) (int, error) {
	out := make([]memory.RemoteRecord, 0, len(recs))
	var acks []string
	for _, r := range recs {
		m.cfg.Clock.Observe(r.TitleHLC)
		m.cfg.Clock.Observe(r.PinnedHLC)
		m.cfg.Clock.Observe(r.ScopeHLC)
		if r.State == "tombstone" {
			if m.cfg.Outbox.Has(r.ID) {
				acks = append(acks, r.ID) // Fleet already tombstoned it
			}
			out = append(out, memory.RemoteRecord{ID: r.ID, Tombstone: true, Reason: r.Reason})
			continue
		}
		if !memory.IsSyncScope(r.ScopeKind) {
			continue // defensive: Fleet only serves synced scopes
		}
		keep[r.ID] = true
		for _, a := range r.Aliases {
			keep[a] = true
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
	if err := m.cfg.Store.ApplyRemote(ctx, out, m.now().UTC()); err != nil {
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
	forgets := m.cfg.Outbox.Pending()
	all, err := m.cfg.Store.List(ctx)
	if err != nil {
		return err
	}
	var ids []string
	for _, c := range all {
		if pushCandidate(c, scopes) {
			ids = append(ids, c.ID)
		}
	}
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

// pushCandidate: a chunk pushes when it is in an enabled sync scope and is
// new to Fleet or changed (and not blocked); or when it left the sync
// scopes while Fleet may still hold it (the demotion push).
func pushCandidate(c memory.Chunk, scopes map[string]bool) bool {
	if c.SyncBlocked != "" {
		return false
	}
	if memory.IsSyncScope(c.ScopeKind) {
		return scopes[c.ScopeKind] && (c.SyncDirty || c.SyncedAt.IsZero())
	}
	return c.SyncDirty && c.FleetMayKnow() // demotion (to session or project)
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
	var me *MemorySyncError
	if errors.As(err, &me) && (me.Status == http.StatusBadRequest || me.Status == http.StatusRequestEntityTooLarge) {
		// An envelope fault is a client bug, not transient: drop the
		// batch (block its upserts, drop its forgets) and log, rather than
		// resend the same bytes forever.
		logging.L().Error("fleet.memory_sync.batch_dropped", "status", me.Status, "code", me.Code, "items", len(entries))
		return m.dropBatch(ctx, entries, me)
	}
	if err != nil {
		return err
	}
	return m.applyResults(ctx, entries, resp)
}

func (m *MemorySync) dropBatch(ctx context.Context, entries []pushEntry, me *MemorySyncError) error {
	var outs []memory.SyncOutcome
	var acks []string
	for _, e := range entries {
		if e.forget {
			acks = append(acks, e.item.ID)
			continue
		}
		outs = append(outs, memory.SyncOutcome{ID: e.item.ID, Gen: e.gen, Kind: memory.OutcomeBlocked, Code: orDefault(me.Code, "client_error")})
	}
	if err := m.cfg.Outbox.Ack(acks...); err != nil {
		return err
	}
	return m.cfg.Store.ApplySyncOutcomes(ctx, outs, m.now().UTC())
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
				_ = m.update(func(s *memSyncState) { s.ResetPending = "cursor_expired" })
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
			default:
				o.Kind, o.Code = memory.OutcomeBlocked, r.Code
			}
		default:
			continue
		}
		outs = append(outs, o)
	}
	if err := m.cfg.Outbox.Ack(acks...); err != nil {
		return err
	}
	if err := m.cfg.Store.ApplySyncOutcomes(ctx, outs, m.now().UTC()); err != nil {
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
	Entitled     bool
	LocalEnabled bool
	Fleet        *MemorySyncSettings // nil when not fetched (not entitled / error)
	FleetError   string
	BlockedCount int
	PendingPush  int
	Lane         LaneSnapshot
}

// Status reads the local state, the lane, and (when entitled) Fleet's
// settings — a user-initiated request, never made by the idle lane.
func (m *MemorySync) Status(ctx context.Context) MemorySyncStatus {
	out := MemorySyncStatus{Entitled: m.entitled(), LocalEnabled: m.state().Enabled, Lane: m.lanes().Snapshot(LaneMemorySync)}
	if all, err := m.cfg.Store.List(ctx); err == nil {
		for _, c := range all {
			if c.SyncBlocked != "" {
				out.BlockedCount++
			} else if memory.IsSyncScope(c.ScopeKind) && (c.SyncDirty || c.SyncedAt.IsZero()) {
				out.PendingPush++
			}
		}
	}
	if !out.Entitled || m.cfg.Client == nil || m.cfg.Client.IsNop() {
		return out
	}
	set, err := m.currentSettings(ctx, true)
	if err != nil {
		out.FleetError = err.Error()
		return out
	}
	out.Fleet = &set
	if set.Enabled != out.LocalEnabled {
		// Another device changed the user-level opt-in: follow it.
		_ = m.update(func(s *memSyncState) { s.Enabled = set.Enabled; s.Scopes = set.Scopes })
		out.LocalEnabled = set.Enabled
	}
	return out
}

// Enable opts this user in on Fleet (PUT settings with consent) and this
// device locally, then kicks a cycle. scopes must be ⊆ {long_term, global}.
func (m *MemorySync) Enable(ctx context.Context, scopes []string, consentVersion string) (MemorySyncSettings, error) {
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
	}); err != nil {
		return set, err
	}
	m.Kick()
	return set, nil
}

// Disable turns sync off for the user on Fleet (server data is kept) and on
// this device. Queued forgets are flushed first — deleting is always
// allowed, and a forget left queued while disabled would never be sent.
func (m *MemorySync) Disable(ctx context.Context) error {
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	if m.cfg.Client != nil && !m.cfg.Client.IsNop() {
		if pend := m.cfg.Outbox.Pending(); len(pend) > 0 {
			entries := make([]pushEntry, 0, len(pend))
			for _, f := range pend {
				entries = append(entries, pushEntry{item: memPushItem{Op: "forget", ID: f.ID, HLC: f.HLC}, forget: true})
			}
			for start := 0; start < len(entries); start += memoryPushMaxItems {
				end := min(start+memoryPushMaxItems, len(entries))
				if err := m.pushBatch(ctx, entries[start:end]); err != nil {
					logging.L().Warn("fleet.memory_sync.disable_forget_flush_failed", "err", err.Error())
					break
				}
			}
		}
		off := false
		if err := m.doJSON(ctx, http.MethodPut, "/api/v1/memory/settings", memSettingsUpdate{Enabled: &off}, nil); err != nil {
			return err
		}
	}
	m.invalidateSettings()
	if err := m.update(func(s *memSyncState) { s.Enabled = false }); err != nil {
		return err
	}
	m.lanes().RecordOff(LaneMemorySync, "not_opted_in")
	return nil
}

// ForgetAll erases every record of this user on Fleet (all devices reset
// on their next pull with reason erased). confirm must be "forget-all".
// It is allowed even after a tier lapse (Fleet gates it on permission
// only), so it does not require the capability.
func (m *MemorySync) ForgetAll(ctx context.Context, confirm string) (int, error) {
	if confirm != "forget-all" {
		return 0, ErrMemoryConfirmRequired
	}
	if m.cfg.Client == nil || m.cfg.Client.IsNop() {
		return 0, ErrFleetDisabled
	}
	m.cycleMu.Lock()
	defer m.cycleMu.Unlock()
	var resp memForgetAllResponse
	if err := m.doJSON(ctx, http.MethodPost, "/api/v1/memory/forget-all", map[string]string{"confirm": confirm}, &resp); err != nil {
		return 0, err
	}
	// Everything Fleet held is gone. Other devices reset (erased) on their
	// next pull; THIS device's local memory is not erased — "delete from
	// Fleet" is not "delete here". Its synced markers are stale, so it
	// reads as never-synced from now on (a later re-opt-in uploads it
	// afresh), queued forgets are moot, and the cursor restarts at "" so
	// its own next pull is not an erase replay against local memory.
	pend := m.cfg.Outbox.Pending()
	ids := make([]string, 0, len(pend))
	for _, p := range pend {
		ids = append(ids, p.ID)
	}
	if err := m.cfg.Outbox.Ack(ids...); err != nil {
		return resp.Erased, err
	}
	if err := m.cfg.Store.ClearSyncMarkers(ctx); err != nil {
		return resp.Erased, err
	}
	return resp.Erased, m.update(func(s *memSyncState) { s.Cursor, s.ResetPending, s.ErasedBefore = "", "", "" })
}
