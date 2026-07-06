// Package fleet — sync.go
//
// Per-category settings sync (push-up / pull-down) between harness devices.
// Wire contract: §2b of kenaz-fleet/docs/contract-harness-sync.md.
//
// Categories: provider_profiles, model_prefs, mcp_recipes, installed_mcp,
// ui_theme.
//
// Fork-removal recipe: delete this file + sync_mcp.go + core/rpc/views/sync/ +
// frontend/src/views/settings/SyncPanel.vue + the "Sync" tab in settings.
package fleet

import (
	"context"
	"encoding/json"
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

// SyncPayload is the wire type for PUT/GET /api/v1/sync/{category}.
// Payload is the category-specific JSON blob; LWWTimestamp is the
// last-write-wins epoch (nanoseconds).
type SyncPayload struct {
	Category     SyncCategory    `json:"category"`
	LWWTimestamp int64           `json:"lww_ts"`
	Payload      json.RawMessage `json:"payload"`
}

// CategoryState holds the per-category runtime state.
type CategoryState struct {
	mu       sync.Mutex
	enabled  bool
	lastPush time.Time
	lastPull time.Time
	lastErr  string
	lwwTS    int64 // nanoseconds
}

// SyncStatus is the read-only view of a category's sync state,
// returned by Syncer.Status.
type SyncStatus struct {
	Category     SyncCategory `json:"category"`
	Enabled      bool         `json:"enabled"`
	LastPushAt   time.Time    `json:"last_push_at,omitempty"`
	LastPullAt   time.Time    `json:"last_pull_at,omitempty"`
	LastError    string       `json:"last_error,omitempty"`
	LWWTS        int64        `json:"lww_ts"`
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
	debounceMu  sync.Mutex
	debounce    map[SyncCategory]chan struct{}
	stopCh      chan struct{}
	once        sync.Once
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

// RegisterCategory wires a collector + applier for a sync category.
// Must be called before Start.
func (s *Syncer) RegisterCategory(cat SyncCategory, cfg CategoryConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[cat] = cfg
}

// SetEnabled toggles sync on/off for a category. When toggled on, an
// immediate push is triggered. When toggled off, no further pushes or
// polls occur for that category (local state is preserved).
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
		// Immediate push of current local state (FR-102).
		return s.Push(ctx, cat)
	}
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SyncStatus, 0, len(s.categories))
	for cat, st := range s.categories {
		st.mu.Lock()
		out = append(out, SyncStatus{
			Category:   cat,
			Enabled:    st.enabled,
			LastPushAt: st.lastPush,
			LastPullAt: st.lastPull,
			LastError:  st.lastErr,
			LWWTS:      st.lwwTS,
		})
		st.mu.Unlock()
	}
	return out
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
		if err := s.Push(ctx, cat); err != nil {
			logging.L().Warn("fleet.sync.debounced_push.failed",
				"category", string(cat),
				"err", err.Error(),
			)
		}
	}()
}

// Push immediately pushes the current local state for a category to fleet.
// Skips if the category is not enabled or has no collector.
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

	raw, err := cfg.Collector(ctx)
	if err != nil {
		return fmt.Errorf("fleet/sync: collect %s: %w", cat, err)
	}
	now := time.Now().UnixNano()
	sp := SyncPayload{
		Category:     cat,
		LWWTimestamp: now,
		Payload:      raw,
	}
	resp, err := s.client.PostJSON(ctx, "/api/v1/sync/"+string(cat), sp)
	if err != nil {
		st.mu.Lock()
		st.lastErr = err.Error()
		st.mu.Unlock()
		return fmt.Errorf("fleet/sync: push %s: %w", cat, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		st.mu.Lock()
		st.lastErr = ErrCapabilityNotInTier.Error()
		st.mu.Unlock()
		return ErrCapabilityNotInTier
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		msg := fmt.Sprintf("status %d: %s", resp.StatusCode, body)
		st.mu.Lock()
		st.lastErr = msg
		st.mu.Unlock()
		return fmt.Errorf("fleet/sync: push %s: %s", cat, msg)
	}
	st.mu.Lock()
	st.lastPush = time.Now()
	st.lastErr = ""
	st.lwwTS = now
	st.mu.Unlock()
	logging.L().Info("fleet.sync.push.ok", "category", string(cat))
	return nil
}

// Pull fetches the remote state for a category and applies it when
// remote-newer (LWW). Skips if the category is not enabled.
func (s *Syncer) Pull(ctx context.Context, cat SyncCategory) error {
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
	lwwTS := st.lwwTS
	st.mu.Unlock()
	if !enabled {
		return nil
	}

	// GET /api/v1/sync/<category>?since=<lww_ts>
	q := url.Values{}
	if lwwTS > 0 {
		q.Set("since", fmt.Sprintf("%d", lwwTS))
	}
	path := "/api/v1/sync/" + string(cat)
	if len(q) > 0 {
		path = path + "?" + q.Encode()
	}
	resp, err := s.client.Get(ctx, path)
	if err != nil {
		st.mu.Lock()
		st.lastErr = err.Error()
		st.mu.Unlock()
		return fmt.Errorf("fleet/sync: pull %s: %w", cat, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		// Remote is not newer — nothing to do.
		return nil
	}
	if resp.StatusCode == http.StatusForbidden {
		return ErrCapabilityNotInTier
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet/sync: pull %s: status %d: %s", cat, resp.StatusCode, body)
	}

	var remote SyncPayload
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return fmt.Errorf("fleet/sync: pull %s: decode: %w", cat, err)
	}

	// LWW: apply only when remote is strictly newer.
	st.mu.Lock()
	localLWW := st.lwwTS
	st.mu.Unlock()
	if remote.LWWTimestamp <= localLWW {
		return nil
	}

	// Apply.
	if cfgOk && cfg.Applier != nil {
		if err := cfg.Applier(ctx, remote.Payload); err != nil {
			st.mu.Lock()
			st.lastErr = err.Error()
			st.mu.Unlock()
			return fmt.Errorf("fleet/sync: apply %s: %w", cat, err)
		}
	}

	st.mu.Lock()
	st.lastPull = time.Now()
	st.lwwTS = remote.LWWTimestamp
	st.lastErr = ""
	st.mu.Unlock()
	logging.L().Info("fleet.sync.pull.applied",
		"category", string(cat),
		"remote_ts", remote.LWWTimestamp,
	)
	return nil
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
func (s *Syncer) Stop() {
	close(s.stopCh)
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
			for _, cat := range AllSyncCategories() {
				if !s.IsEnabled(cat) {
					continue
				}
				if err := s.Pull(ctx, cat); err != nil {
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
