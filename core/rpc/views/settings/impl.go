// Package settings's impl provides a JSON-file backed SettingsAPI +
// SettingsStore.
//
// Persistence: $USER_CONFIG_DIR/kenaz-harness/settings.json (privacy
// CI invariant #5). Schema version + lastRoute + theme are the
// canonical persisted fields; window size + accent are also captured
// so the chassis can restore its full first-paint state.
//
// The store survives partial files: a missing key falls back to a
// safe default (system theme, /sessions route) so a user never sees
// the app brick because of a stale config.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

// FileStore is a SettingsStore backed by a single JSON file. Safe for
// concurrent use; reads parse on demand, writes serialize through a
// mutex to prevent torn writes.
type FileStore struct {
	mu   sync.Mutex
	path string
}

// NewFileStore returns a store rooted at <userConfigDir>/kenaz-harness/.
// The directory is created on first write.
func NewFileStore(userConfigDir string) (*FileStore, error) {
	if userConfigDir == "" {
		return nil, errors.New("settings: empty user config dir")
	}
	dir := filepath.Join(userConfigDir, "kenaz-harness")
	return &FileStore{path: filepath.Join(dir, "settings.json")}, nil
}

// NewFileStoreFromEnv resolves USER_CONFIG_DIR via os.UserConfigDir.
func NewFileStoreFromEnv() (*FileStore, error) {
	paths.MigrateLegacyConfigDir()
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("settings: user config dir: %w", err)
	}
	return NewFileStore(dir)
}

// Path returns the on-disk path the store reads/writes.
func (s *FileStore) Path() string { return s.path }

// LoadAll reads the file. Missing-file returns the default Settings;
// malformed JSON surfaces as an error so the user sees the problem.
func (s *FileStore) LoadAll() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *FileStore) loadLocked() (Settings, error) {
	def := defaultSettings()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return def, nil
		}
		return def, fmt.Errorf("settings: read: %w", err)
	}
	var got Settings
	if err := json.Unmarshal(data, &got); err != nil {
		return def, fmt.Errorf("settings: parse: %w", err)
	}
	// Apply defaults for any zero-valued fields so partial files don't
	// brick the UI.
	if got.SchemaVersion == 0 {
		got.SchemaVersion = def.SchemaVersion
	}
	if got.LastRoute == "" {
		got.LastRoute = def.LastRoute
	}
	if got.Theme == "" {
		got.Theme = def.Theme
	}
	if got.Accent == "" {
		got.Accent = def.Accent
	}
	if got.WindowSize.Width == 0 {
		got.WindowSize.Width = def.WindowSize.Width
	}
	if got.WindowSize.Height == 0 {
		got.WindowSize.Height = def.WindowSize.Height
	}
	return got, nil
}

// SaveAll persists every field atomically via a temp-file rename.
func (s *FileStore) SaveAll(in Settings) error {
	if err := validateCompactionFields(in); err != nil {
		return err
	}
	if err := validateShortcuts(in); err != nil {
		return err
	}
	if err := validateUpdateFields(in); err != nil {
		return err
	}
	if err := validateBranchFields(in); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(in)
}

// ErrInvalidCompactionAggressiveness is the typed error Save returns
// when the user (or a hand-edited settings file) supplies an unknown
// aggressiveness tier. Empty string IS valid (resolves to "balanced"
// via the Effective accessor); only known constants pass through.
var ErrInvalidCompactionAggressiveness = errors.New("settings: invalid compactionAggressiveness — must be one of off / conservative / balanced / aggressive / maximal")

// ErrInvalidCompactionArchiveDays is returned when the persisted
// archive-retention window falls outside the [Min, Max] range. Zero is
// valid (resolves to DefaultCompactionArchiveDays).
var ErrInvalidCompactionArchiveDays = fmt.Errorf("settings: invalid compactionArchiveDays — must be 0 (default) or in [%d, %d]", MinCompactionArchiveDays, MaxCompactionArchiveDays)

// ErrInvalidCompactionRecentWindow is returned when the recent-window
// override is negative.
var ErrInvalidCompactionRecentWindow = errors.New("settings: invalid compactionRecentWindow — must be >= 0")

// ErrInvalidMonthlyCostNotifyUSD is returned when the monthly-spend
// notification dial is negative or exceeds MaxMonthlyCostNotifyUSD.
// Zero IS valid — it disables the scheduler completely
// (token-cost-telemetry-01KQ8TD7 WP06).
var ErrInvalidMonthlyCostNotifyUSD = fmt.Errorf(
	"settings: invalid monthlyCostNotifyUsd — must be 0 (disabled) or in (0, %g]",
	MaxMonthlyCostNotifyUSD,
)

// ErrInvalidUpdateChannel is returned when the persisted UpdateChannel
// is neither "stable", "prerelease", nor empty (auto-update-v0.4.0 WP05).
var ErrInvalidUpdateChannel = errors.New("settings: invalid updateChannel — must be stable or prerelease")

// ErrInvalidUpdateCheckInterval is returned when the persisted
// UpdateCheckIntervalSec is negative or outside [Min, Max]. Zero is
// valid (resolves to DefaultUpdateCheckIntervalSec).
var ErrInvalidUpdateCheckInterval = fmt.Errorf(
	"settings: invalid updateCheckIntervalSec — must be 0 (default) or in [%d, %d]",
	MinUpdateCheckIntervalSec, MaxUpdateCheckIntervalSec,
)

// ErrInvalidBranchAdvisorMinConfidence is returned when the persisted
// confidence threshold falls outside [0, 1] (engineer-truth-pass-
// 01PMTP01 WP03, finding B2 / FR-005). Before this WP the docstring at
// api.go claimed "Save rejects values outside this range" but SaveAll
// ran no validator that touched BranchAdvisor* or BranchReintegration*
// fields at all.
var ErrInvalidBranchAdvisorMinConfidence = errors.New("settings: invalid branchAdvisorMinConfidence — must be in [0, 1]")

// ErrInvalidBranchReintegrationMaxTokens is returned when the persisted
// token budget is neither 0 (default) nor in
// [MinBranchReintegrationMaxTokens, MaxBranchReintegrationMaxTokens].
var ErrInvalidBranchReintegrationMaxTokens = fmt.Errorf(
	"settings: invalid branchReintegrationMaxTokens — must be 0 (default) or in [%d, %d]",
	MinBranchReintegrationMaxTokens, MaxBranchReintegrationMaxTokens,
)

// validateBranchFields enforces the branch-advisor dial constraints at
// SAVE time (engineer-truth-pass-01PMTP01 WP03, finding B2 / FR-005).
// Zero is valid for BranchReintegrationMaxTokens — it resolves to
// DefaultBranchReintegrationMaxTokens via the Effective accessor.
func validateBranchFields(in Settings) error {
	if c := in.BranchAdvisorMinConfidence; c != 0 && (c < 0 || c > 1) {
		return ErrInvalidBranchAdvisorMinConfidence
	}
	if t := in.BranchReintegrationMaxTokens; t != 0 && (t < MinBranchReintegrationMaxTokens || t > MaxBranchReintegrationMaxTokens) {
		return ErrInvalidBranchReintegrationMaxTokens
	}
	return nil
}

// validateUpdateFields enforces the WP05 update-dial constraints at
// SAVE time. Empty string / zero are valid (they resolve to defaults
// via the Effective accessors); only out-of-band values trip an error.
func validateUpdateFields(in Settings) error {
	switch in.UpdateChannel {
	case "", UpdateChannelStable, UpdateChannelPrerelease:
		// ok
	default:
		return ErrInvalidUpdateChannel
	}
	if v := in.UpdateCheckIntervalSec; v != 0 && (v < MinUpdateCheckIntervalSec || v > MaxUpdateCheckIntervalSec) {
		return ErrInvalidUpdateCheckInterval
	}
	return nil
}

// validateCompactionFields runs the per-field validation rules for the
// four compaction settings (mission compaction-strategy-ui-01KQ8TDI
// WP06 acceptance: clamp rejects out-of-range values at SAVE, not just
// at Effective-time clamp). Returns the typed error so the rpc layer
// can render specific copy in the UI.
//
// Empty / zero values are valid for every field — they resolve to the
// documented defaults via the EffectiveCompaction* accessors.
func validateCompactionFields(in Settings) error {
	switch compactionpolicy.CompactionAggressiveness(in.CompactionAggressiveness) {
	case "",
		compactionpolicy.AggressivenessOff,
		compactionpolicy.AggressivenessConservative,
		compactionpolicy.AggressivenessBalanced,
		compactionpolicy.AggressivenessAggressive,
		compactionpolicy.AggressivenessMaximal:
		// ok
	default:
		return ErrInvalidCompactionAggressiveness
	}

	if d := in.CompactionArchiveDays; d != 0 && (d < MinCompactionArchiveDays || d > MaxCompactionArchiveDays) {
		return ErrInvalidCompactionArchiveDays
	}

	if in.CompactionRecentWindow < 0 {
		return ErrInvalidCompactionRecentWindow
	}

	if v := in.MonthlyCostNotifyUSD; v < 0 || v > MaxMonthlyCostNotifyUSD {
		return ErrInvalidMonthlyCostNotifyUSD
	}

	if in.LongSessionNudgeTurns < 0 {
		return ErrInvalidLongSessionNudgeTurns
	}
	if in.LongSessionNudgeTokens < 0 {
		return ErrInvalidLongSessionNudgeTokens
	}

	return nil
}

// ErrInvalidLongSessionNudgeTurns is returned when the turn-count threshold
// is negative. Zero is valid (resolves to DefaultLongSessionNudgeTurns).
var ErrInvalidLongSessionNudgeTurns = errors.New("settings: invalid longSessionNudgeTurns — must be >= 0")

// ErrInvalidLongSessionNudgeTokens is returned when the token-count threshold
// is negative. Zero is valid (resolves to DefaultLongSessionNudgeTokens).
var ErrInvalidLongSessionNudgeTokens = errors.New("settings: invalid longSessionNudgeTokens — must be >= 0")

// MaxShortcutEntries is the upper bound on the number of per-shortcut
// overrides the backend accepts in a single Settings write (plan §2.7 R4).
const MaxShortcutEntries = 200

// MaxShortcutValueLen is the maximum byte length of a single binding
// value. Generous enough for any legitimate `Cmd+Shift+Letter` string.
const MaxShortcutValueLen = 64

// ErrTooManyShortcuts is returned when the KeyboardShortcuts map exceeds
// MaxShortcutEntries.
var ErrTooManyShortcuts = fmt.Errorf(
	"settings: keyboardShortcuts map exceeds maximum of %d entries", MaxShortcutEntries,
)

// ErrShortcutValueTooLong is returned when a binding string exceeds
// MaxShortcutValueLen bytes.
var ErrShortcutValueTooLong = fmt.Errorf(
	"settings: keyboard shortcut binding value exceeds %d characters", MaxShortcutValueLen,
)

// ErrShortcutValueControlChar is returned when a binding value contains
// a control character (newline, tab, etc.).
var ErrShortcutValueControlChar = errors.New(
	"settings: keyboard shortcut binding value contains a control character",
)

// validateShortcuts checks the KeyboardShortcuts map for size and value
// constraints (plan §2.7). Called by SaveAll so malformed maps from a
// hand-edited settings file or a buggy client are rejected before they
// reach the filesystem.
func validateShortcuts(in Settings) error {
	if len(in.KeyboardShortcuts) > MaxShortcutEntries {
		return ErrTooManyShortcuts
	}
	for _, v := range in.KeyboardShortcuts {
		if len(v) > MaxShortcutValueLen {
			return ErrShortcutValueTooLong
		}
		for _, r := range v {
			if unicode.IsControl(r) {
				return ErrShortcutValueControlChar
			}
		}
	}
	return nil
}

func (s *FileStore) saveLocked(in Settings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("settings: mkdir: %w", err)
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	out, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("settings: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("settings: rename: %w", err)
	}
	return nil
}

// LoadRoute returns the persisted lastRoute (default "/sessions").
func (s *FileStore) LoadRoute() (string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.LastRoute, err
	}
	return got.LastRoute, nil
}

// SaveRoute updates lastRoute in place.
func (s *FileStore) SaveRoute(route string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.LastRoute = route
	return s.saveLocked(got)
}

// LogRouteChange is a no-op in the file store; route auditing
// pipelines through the event log when the audit feature is wired.
// We keep the seam stable so the SettingsStore interface is honored.
func (s *FileStore) LogRouteChange(_, _ string) error { return nil }

// LoadTheme returns the persisted theme (default "system").
func (s *FileStore) LoadTheme() (string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.Theme, err
	}
	return got.Theme, nil
}

// SaveTheme updates theme in place.
func (s *FileStore) SaveTheme(theme string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.Theme = theme
	return s.saveLocked(got)
}

// LoadMemory returns the memoryEnabled opt-in flag (default false —
// privacy posture: OFF unless the user explicitly toggles it).
func (s *FileStore) LoadMemory() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.MemoryEnabled, err
	}
	return got.MemoryEnabled, nil
}

// SaveMemory updates the long-term-memory opt-in flag.
func (s *FileStore) SaveMemory(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.MemoryEnabled = enabled
	return s.saveLocked(got)
}

// LoadConfirmEach returns the WP05 confirm-each modal opt-in flag.
// Default true (modal ON) — the persisted bit is inverted so a fresh
// install with no settings.json gets the spec's default-ON behaviour
// without writing any state. Errors return the default so the chat
// surface keeps working even if the settings file is unreadable.
func (s *FileStore) LoadConfirmEach() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.ConfirmEachEnabled(), err
	}
	return got.ConfirmEachEnabled(), nil
}

// SaveConfirmEach updates the confirm-each modal opt-in flag. Persists
// as the inverted ConfirmEachDisabled field so the JSON shape matches
// the storage contract.
func (s *FileStore) SaveConfirmEach(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.ConfirmEachDisabled = !enabled
	return s.saveLocked(got)
}

// LoadWebSearch returns the local-first web search built-in opt-in.
// Default false (off) — privacy / least-surface posture.
func (s *FileStore) LoadWebSearch() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.WebSearchEnabled, err
	}
	return got.WebSearchEnabled, nil
}

// SaveWebSearch updates the web search opt-in flag.
func (s *FileStore) SaveWebSearch(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.WebSearchEnabled = enabled
	return s.saveLocked(got)
}

// LoadBash returns the local-first bash built-in opt-in. Default
// false (off) — bash is gated behind both this toggle and the
// per-command allowlist regardless.
func (s *FileStore) LoadBash() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.BashEnabled, err
	}
	return got.BashEnabled, nil
}

// SaveBash updates the bash built-in opt-in flag.
func (s *FileStore) SaveBash(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.BashEnabled = enabled
	return s.saveLocked(got)
}

// LoadWebFetchEnabled returns the kenaz__web_fetch built-in opt-in.
// Default false (off) — the tool makes outbound HTTP requests with
// @secret: substitution; the user must opt in from the Tools panel.
// Cedar network policy applies regardless of this toggle.
// (crash-recovery-tool-gating-0XQTC4RK FR-005)
func (s *FileStore) LoadWebFetchEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.WebFetchEnabled, err
	}
	return got.WebFetchEnabled, nil
}

// SaveWebFetchEnabled persists the kenaz__web_fetch opt-in flag.
func (s *FileStore) SaveWebFetchEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.WebFetchEnabled = enabled
	return s.saveLocked(got)
}

// LoadSaveArtifactEnabled returns the kenaz__save_artifact built-in
// opt-in. Default true (on) — wire shape persists the inverted
// SaveArtifactDisabled bit so a fresh install (zero-value across the
// board) matches "tool enabled".
func (s *FileStore) LoadSaveArtifactEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.SaveArtifactEnabled(), err
	}
	return got.SaveArtifactEnabled(), nil
}

// SaveSaveArtifactEnabled updates the save_artifact built-in opt-in
// flag. Persists as the inverted SaveArtifactDisabled bit.
func (s *FileStore) SaveSaveArtifactEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.SaveArtifactDisabled = !enabled
	return s.saveLocked(got)
}

// LoadMaxAgentTurns returns the chat-graph LoopNode iteration cap.
// Default DefaultMaxAgentTurns (25) when the persisted value is zero
// or when the settings file is unreadable — the chat surface stays
// usable even if storage is broken. The returned int is the
// user-tuned value (zero-on-wire == default), not the effective
// value; callers run it through EffectiveMaxAgentTurns when they
// need the rounded-up form.
func (s *FileStore) LoadMaxAgentTurns() (int, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.MaxAgentTurns, err
	}
	return got.MaxAgentTurns, nil
}

// SaveMaxAgentTurns updates the chat-graph LoopNode iteration cap.
// The chassis reads this on every chat run start so the change takes
// effect on the next user turn. Zero clears the override (resets to
// DefaultMaxAgentTurns). Negative values are normalised to zero so
// the spec default re-engages.
func (s *FileStore) SaveMaxAgentTurns(turns int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	if turns < 0 {
		turns = 0
	}
	got.MaxAgentTurns = turns
	return s.saveLocked(got)
}

// ── WP08 permission dial FileStore accessors ──────────────────────

// LoadPermissionMode returns the global permission posture ("normal"
// when unset).
func (s *FileStore) LoadPermissionMode() (string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.EffectivePermissionMode(), err
	}
	return got.EffectivePermissionMode(), nil
}

// SavePermissionMode updates the global permission posture.
func (s *FileStore) SavePermissionMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.PermissionMode = mode
	return s.saveLocked(got)
}

// LoadShortcuts returns the KeyboardShortcuts map from the persisted
// settings. Missing settings file returns an empty map (no error).
func (s *FileStore) LoadShortcuts() (map[string]string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	if got.KeyboardShortcuts == nil {
		return map[string]string{}, nil
	}
	// Return a defensive copy.
	out := make(map[string]string, len(got.KeyboardShortcuts))
	for k, v := range got.KeyboardShortcuts {
		out[k] = v
	}
	return out, nil
}

// SaveShortcuts atomically replaces the full KeyboardShortcuts map.
func (s *FileStore) SaveShortcuts(m map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.KeyboardShortcuts = m
	return s.saveLocked(got)
}

// LoadPermissionCacheDangerousOps returns the dangerous-ops override flag
// (default false).
func (s *FileStore) LoadPermissionCacheDangerousOps() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.PermissionCacheDangerousOps, err
	}
	return got.PermissionCacheDangerousOps, nil
}

// SavePermissionCacheDangerousOps updates the dangerous-ops override flag.
func (s *FileStore) SavePermissionCacheDangerousOps(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.PermissionCacheDangerousOps = enabled
	return s.saveLocked(got)
}

// LoadBashAllowlistMigrated returns the WP10 migration marker.
func (s *FileStore) LoadBashAllowlistMigrated() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.BashAllowlistMigrated, err
	}
	return got.BashAllowlistMigrated, nil
}

// SaveBashAllowlistMigrated updates the WP10 migration marker.
func (s *FileStore) SaveBashAllowlistMigrated(migrated bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.BashAllowlistMigrated = migrated
	return s.saveLocked(got)
}

// LoadPermissionsMigrationToastShown returns the one-time toast marker.
func (s *FileStore) LoadPermissionsMigrationToastShown() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.PermissionsMigrationToastShown, err
	}
	return got.PermissionsMigrationToastShown, nil
}

// SavePermissionsMigrationToastShown updates the one-time toast marker.
func (s *FileStore) SavePermissionsMigrationToastShown(shown bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.PermissionsMigrationToastShown = shown
	return s.saveLocked(got)
}

// LoadCedarStrictCredentialMode returns the WP05 credential-gate
// strictness flag. Default false (lenient) — a fresh install with no
// settings.json allows NotApplicable outcomes through. Errors return
// the safe default so the credstore gate keeps working even if the
// settings file is unreadable.
func (s *FileStore) LoadCedarStrictCredentialMode() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.CedarStrictCredentialMode, err
	}
	return got.CedarStrictCredentialMode, nil
}

// SaveCedarStrictCredentialMode persists the credential-gate
// strictness flag.
func (s *FileStore) SaveCedarStrictCredentialMode(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.CedarStrictCredentialMode = enabled
	return s.saveLocked(got)
}

// LoadCedarStrictWorkflowMode returns the Workflow-family strictness
// dial. Default false (permissive) — a fresh install runs and saves
// workflows under the bundle's permissive arm. Errors return the safe
// default so a corrupt settings file cannot fail workflows closed.
func (s *FileStore) LoadCedarStrictWorkflowMode() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.CedarStrictWorkflowMode, err
	}
	return got.CedarStrictWorkflowMode, nil
}

// SaveCedarStrictWorkflowMode persists the Workflow-family strictness
// dial. Takes effect on the next workflow run/save — the gate reads it
// through CedarModeFn rather than caching it at construction.
func (s *FileStore) SaveCedarStrictWorkflowMode(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.CedarStrictWorkflowMode = enabled
	return s.saveLocked(got)
}

// LoadGraphAuthoringEnabled returns the model-authored-agent-graphs
// consent dial. Default false — a corrupt or missing settings file
// returns the safe (denied) default so an unreadable settings.json
// cannot silently grant graph-authoring.
func (s *FileStore) LoadGraphAuthoringEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.GraphAuthoringEnabled, err
	}
	return got.GraphAuthoringEnabled, nil
}

// SaveGraphAuthoringEnabled persists the model-authored-agent-graphs
// consent dial. Takes effect on the next graph.author evaluation — the
// gate reads it live rather than caching it at Manager construction.
func (s *FileStore) SaveGraphAuthoringEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.GraphAuthoringEnabled = enabled
	return s.saveLocked(got)
}

// LoadFSRequestAccessEnabled returns the kenaz__request_filesystem_access
// built-in opt-in. Default true (on) — zero-value FSRequestAccessDisabled
// means enabled. Errors return the safe default so the tool keeps working
// even if the settings file is unreadable.
func (s *FileStore) LoadFSRequestAccessEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.FSRequestAccessEnabled(), err
	}
	return got.FSRequestAccessEnabled(), nil
}

// SaveFSRequestAccessEnabled persists the request_filesystem_access
// built-in opt-in flag. Persists as the inverted FSRequestAccessDisabled bit.
func (s *FileStore) SaveFSRequestAccessEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.FSRequestAccessDisabled = !enabled
	return s.saveLocked(got)
}

// LoadMonthlyCostNotifyUSD returns the persisted monthly-spend
// notification threshold (token-cost-telemetry-01KQ8TD7 WP06). Zero
// (the default) disables the scheduler. Errors return zero so a broken
// settings file silently disables the scheduler rather than spamming
// a default-on threshold.
func (s *FileStore) LoadMonthlyCostNotifyUSD() (float64, error) {
	got, err := s.LoadAll()
	if err != nil {
		return 0, err
	}
	return got.MonthlyCostNotifyUSD, nil
}

// SaveMonthlyCostNotifyUSD persists the monthly-spend notification
// threshold. Negative values are normalised to zero (the disabled
// state); values above MaxMonthlyCostNotifyUSD are rejected with the
// typed error so the UI can render specific copy.
func (s *FileStore) SaveMonthlyCostNotifyUSD(usd float64) error {
	if usd < 0 {
		usd = 0
	}
	if usd > MaxMonthlyCostNotifyUSD {
		return ErrInvalidMonthlyCostNotifyUSD
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.MonthlyCostNotifyUSD = usd
	return s.saveLocked(got)
}

// LoadAutonomyProfile returns the persisted global autonomy.Layer
// (autonomy-dial-01KR3M2A WP02). Empty / missing field returns the
// zero Layer, which the resolver treats as "fall through to the next
// layer / tier-default." Errors return the safe default (empty Layer)
// alongside the error so the resolver can keep working even if the
// settings file is unreadable.
func (s *FileStore) LoadAutonomyProfile() (autonomy.Layer, error) {
	got, err := s.LoadAll()
	if err != nil {
		return autonomy.Layer{}, err
	}
	return decodeAutonomyField(got.Autonomy)
}

// SaveAutonomyProfile persists the global autonomy.Layer. An empty
// Layer (Level=nil + no overrides) clears the field so a fresh load
// returns the empty Layer again.
func (s *FileStore) SaveAutonomyProfile(layer autonomy.Layer) error {
	encoded, err := encodeAutonomyField(layer)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.Autonomy = encoded
	return s.saveLocked(got)
}

// LoadMCPAutoRestart returns whether MCP servers should auto-restart.
// Default true (zero-value Disabled → restart enabled).
// (mcp-server-health-ui-01KQ8TD6 WP06)
func (s *FileStore) LoadMCPAutoRestart() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.MCPAutoRestart(), err
	}
	return got.MCPAutoRestart(), nil
}

// SaveMCPAutoRestart persists the MCP auto-restart dial. Stored as the
// inverted MCPAutoRestartDisabled bit.
func (s *FileStore) SaveMCPAutoRestart(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.MCPAutoRestartDisabled = !enabled
	return s.saveLocked(got)
}

// LoadAutoTitleEnabled returns whether session auto-titling is on.
// Default true (zero-value Disabled → feature enabled).
func (s *FileStore) LoadAutoTitleEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.AutoTitleEnabled(), err
	}
	return got.AutoTitleEnabled(), nil
}

// SaveAutoTitleEnabled persists the auto-title feature toggle. Stored
// as the inverted AutoTitleDisabled bit.
func (s *FileStore) SaveAutoTitleEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.AutoTitleDisabled = !enabled
	return s.saveLocked(got)
}

// ── Key-rotation FileStore accessors (provider-keychain-rotation-01KQ8TD9 WP07) ──

// LoadAutoResumeOnKeyRotation returns whether the harness should automatically
// redrive the paused turn after a key rotation. Default true (feature enabled).
func (s *FileStore) LoadAutoResumeOnKeyRotation() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.EffectiveAutoResumeOnKeyRotation(), err
	}
	return got.EffectiveAutoResumeOnKeyRotation(), nil
}

// SaveAutoResumeOnKeyRotation persists the auto-resume-on-key-rotation dial.
// Stored as the inverted AutoResumeOnKeyRotationDisabled bit.
func (s *FileStore) SaveAutoResumeOnKeyRotation(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.AutoResumeOnKeyRotationDisabled = !enabled
	return s.saveLocked(got)
}

// ── Builtin filesystem tool FileStore accessors (builtin-filesystem-tools-01KR3N4P) ──

// LoadFSReadEnabled returns the read-family filesystem tool opt-in.
// Default false (tools off until the user enables them). Errors return
// the safe default (false) so the tools remain disabled when the
// settings file is unreadable.
func (s *FileStore) LoadFSReadEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.FSReadEnabled(), err
	}
	return got.FSReadEnabled(), nil
}

// SaveFSReadEnabled persists the read-family filesystem tool opt-in.
// Persists as the inverted FSReadDisabled bit.
func (s *FileStore) SaveFSReadEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.FSReadDisabled = !enabled
	return s.saveLocked(got)
}

// LoadFSWriteEnabled returns the write-family filesystem tool opt-in.
// Default false (tools off). Errors return the safe default (false).
func (s *FileStore) LoadFSWriteEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.FSWriteEnabled(), err
	}
	return got.FSWriteEnabled(), nil
}

// SaveFSWriteEnabled persists the write-family filesystem tool opt-in.
// Persists as the inverted FSWriteDisabled bit.
func (s *FileStore) SaveFSWriteEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.FSWriteDisabled = !enabled
	return s.saveLocked(got)
}

// ── Todo tool FileStore accessors (builtin-tools-search-and-elicitation-01KZNP3D) ──

// LoadTodoEnabled returns the kenaz__todo_write opt-in.
// Default false (tool off until the user enables it). Errors return the
// safe default (false) so the tool remains disabled when the settings
// file is unreadable.
func (s *FileStore) LoadTodoEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.TodoEnabled(), err
	}
	return got.TodoEnabled(), nil
}

// SaveTodoEnabled persists the kenaz__todo_write opt-in.
// Persists as the inverted TodoDisabled bit.
func (s *FileStore) SaveTodoEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.TodoDisabled = !enabled
	return s.saveLocked(got)
}

// LoadEditFileArtifactSyncEnabled returns the per-user opt-in for the
// edit-file artifact sync pipeline. Default true (enabled, matching
// the zero-value of EditFileArtifactSyncDisabled). Errors return the
// safe default (true) so the feature stays on through a settings glitch.
func (s *FileStore) LoadEditFileArtifactSyncEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.EditFileArtifactSyncEnabled(), err
	}
	return got.EditFileArtifactSyncEnabled(), nil
}

// SaveEditFileArtifactSyncEnabled persists the per-user opt-in for the
// edit-file artifact sync pipeline. Persists as the inverted
// EditFileArtifactSyncDisabled bit.
func (s *FileStore) SaveEditFileArtifactSyncEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.EditFileArtifactSyncDisabled = !enabled
	return s.saveLocked(got)
}

// LoadEmbedderConfig returns the persisted embedder provider profile ID
// and model override.  Empty strings mean "use defaults" (auto-pick
// first eligible provider, per-Kind default model).
// (v0.5.2 universal-embedder fix)
func (s *FileStore) LoadEmbedderConfig() (profileID, modelOverride string, err error) {
	got, lerr := s.LoadAll()
	if lerr != nil {
		return "", "", lerr
	}
	return got.EmbedderProviderProfileID, got.EmbedderModelOverride, nil
}

// SaveEmbedderConfig persists the embedder provider selection and
// optional model override.  Empty strings reset to auto-pick /
// per-Kind-default behaviour.
func (s *FileStore) SaveEmbedderConfig(profileID, modelOverride string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.EmbedderProviderProfileID = profileID
	got.EmbedderModelOverride = modelOverride
	return s.saveLocked(got)
}

// ── Memory narrative layer FileStore accessors (memory-narrative-layer-01KQ8TD1) ──

// LoadSummarizerProfileID returns the configured summariser profile ID.
// Empty means "auto-select cheapest". Safe-defaults on read error.
func (s *FileStore) LoadSummarizerProfileID() (string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return "", err
	}
	return got.SummarizerProfileID, nil
}

// SaveSummarizerProfileID persists the summariser profile ID.
func (s *FileStore) SaveSummarizerProfileID(profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.SummarizerProfileID = profileID
	return s.saveLocked(got)
}

// LoadChatCustomInstructions returns the user's chat custom-instructions
// text. Empty means "no user layer". (system-prompt-layers WP04)
func (s *FileStore) LoadChatCustomInstructions() (string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return "", err
	}
	return got.ChatCustomInstructions, nil
}

// SaveChatCustomInstructions persists the chat custom-instructions text.
func (s *FileStore) SaveChatCustomInstructions(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.ChatCustomInstructions = text
	return s.saveLocked(got)
}

// LoadMemoryNarrativeEnabled returns the narrative-layer opt-in.
func (s *FileStore) LoadMemoryNarrativeEnabled() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return true, err // safe default: enabled
	}
	return got.MemoryNarrativeEnabled, nil
}

// SaveMemoryNarrativeEnabled persists the narrative-layer opt-in.
func (s *FileStore) SaveMemoryNarrativeEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.MemoryNarrativeEnabled = enabled
	return s.saveLocked(got)
}

// encodeAutonomyField marshals a Layer to the json.RawMessage stored
// on Settings.Autonomy. The empty Layer encodes as nil so it omits via
// `omitempty` on disk.
func encodeAutonomyField(layer autonomy.Layer) (json.RawMessage, error) {
	if layer.IsZero() {
		return nil, nil
	}
	blob, err := layer.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("settings: marshal autonomy layer: %w", err)
	}
	return json.RawMessage(blob), nil
}

// decodeAutonomyField parses the Settings.Autonomy raw field. Missing
// or empty input yields the zero Layer (no error).
func decodeAutonomyField(raw json.RawMessage) (autonomy.Layer, error) {
	if len(raw) == 0 {
		return autonomy.Layer{}, nil
	}
	// A literal JSON null is also treated as "no value."
	trimmed := raw
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\n' || trimmed[0] == '\r') {
		trimmed = trimmed[1:]
	}
	if len(trimmed) >= 4 && string(trimmed[:4]) == "null" {
		return autonomy.Layer{}, nil
	}
	var l autonomy.Layer
	if err := l.UnmarshalJSON(raw); err != nil {
		return autonomy.Layer{}, fmt.Errorf("settings: parse autonomy layer: %w", err)
	}
	return l, nil
}

// ── Auto-update FileStore accessors (auto-update-v0.4.0 WP05) ─────────

// LoadAutoCheckUpdates returns the master auto-check toggle. Default
// true on a fresh install (zero-value AutoCheckUpdatesDisabled). Errors
// return the safe default so the checker scheduler keeps running even
// if the settings file is unreadable.
func (s *FileStore) LoadAutoCheckUpdates() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.AutoCheckUpdates(), err
	}
	return got.AutoCheckUpdates(), nil
}

// SaveAutoCheckUpdates persists the master auto-check toggle. Persists
// as the inverted AutoCheckUpdatesDisabled bit so the JSON shape matches
// the storage contract (default ON without writing any state).
func (s *FileStore) SaveAutoCheckUpdates(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.AutoCheckUpdatesDisabled = !enabled
	return s.saveLocked(got)
}

// LoadUpdateChannel returns the persisted release channel, falling back
// to "stable" via EffectiveUpdateChannel for unknown / empty values.
func (s *FileStore) LoadUpdateChannel() (string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.EffectiveUpdateChannel(), err
	}
	return got.EffectiveUpdateChannel(), nil
}

// SaveUpdateChannel persists the release channel. Empty string clears
// the override (resets to "stable"); unknown values are rejected with
// the typed error so the UI can render specific copy.
func (s *FileStore) SaveUpdateChannel(channel string) error {
	switch channel {
	case "", UpdateChannelStable, UpdateChannelPrerelease:
		// ok
	default:
		return ErrInvalidUpdateChannel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.UpdateChannel = channel
	return s.saveLocked(got)
}

// LoadUpdateCheckInterval returns the user-tuned poll interval as a
// time.Duration, falling back to DefaultUpdateCheckIntervalSec.
func (s *FileStore) LoadUpdateCheckInterval() (time.Duration, error) {
	got, err := s.LoadAll()
	if err != nil {
		return time.Duration(got.EffectiveUpdateCheckIntervalSec()) * time.Second, err
	}
	return time.Duration(got.EffectiveUpdateCheckIntervalSec()) * time.Second, nil
}

// SaveUpdateCheckInterval persists the poll interval. Zero clears the
// override; out-of-range values are rejected with the typed error.
func (s *FileStore) SaveUpdateCheckInterval(d time.Duration) error {
	secs := int(d / time.Second)
	if secs < 0 {
		secs = 0
	}
	if secs != 0 && (secs < MinUpdateCheckIntervalSec || secs > MaxUpdateCheckIntervalSec) {
		return ErrInvalidUpdateCheckInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.UpdateCheckIntervalSec = secs
	return s.saveLocked(got)
}

// LoadSkippedUpdateVersions returns the user's skip-list. Always
// returns a non-nil slice (defensive copy) so callers can iterate.
func (s *FileStore) LoadSkippedUpdateVersions() ([]string, error) {
	got, err := s.LoadAll()
	if err != nil {
		return []string{}, err
	}
	if len(got.SkippedUpdateVersions) == 0 {
		return []string{}, nil
	}
	out := make([]string, len(got.SkippedUpdateVersions))
	copy(out, got.SkippedUpdateVersions)
	return out, nil
}

// SaveSkippedUpdateVersions atomically replaces the full skip-list. The
// caller is expected to dedup; we do a defensive normalise here so a
// hand-edited file with duplicates round-trips cleanly.
func (s *FileStore) SaveSkippedUpdateVersions(versions []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.SkippedUpdateVersions = dedupNonEmpty(versions)
	return s.saveLocked(got)
}

// AppendSkippedUpdateVersion adds one version (idempotent — duplicates
// are coalesced). Empty strings are ignored so the UpdateMenu's "Skip"
// button is safe to call even if the version field hasn't loaded yet.
func (s *FileStore) AppendSkippedUpdateVersion(version string) error {
	if version == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	for _, v := range got.SkippedUpdateVersions {
		if v == version {
			return nil // already present
		}
	}
	got.SkippedUpdateVersions = append(got.SkippedUpdateVersions, version)
	return s.saveLocked(got)
}

// RemoveSkippedUpdateVersion removes one version. No-op if missing.
func (s *FileStore) RemoveSkippedUpdateVersion(version string) error {
	if version == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	out := got.SkippedUpdateVersions[:0]
	for _, v := range got.SkippedUpdateVersions {
		if v != version {
			out = append(out, v)
		}
	}
	got.SkippedUpdateVersions = out
	return s.saveLocked(got)
}

// ── First-run onboarding completion flag (harness-onboarding-01NHON01 WP01) ──

// LoadFirstRunOnboardingCompleted returns whether the first-run onboarding
// flow has been completed or dismissed. Default false (= show onboarding).
// Errors return false so the dialog still shows on a fresh install even
// when the settings file is temporarily unreadable.
func (s *FileStore) LoadFirstRunOnboardingCompleted() (bool, error) {
	got, err := s.LoadAll()
	if err != nil {
		return got.FirstRunOnboardingCompleted, err
	}
	return got.FirstRunOnboardingCompleted, nil
}

// SaveFirstRunOnboardingCompleted persists the onboarding completion flag.
func (s *FileStore) SaveFirstRunOnboardingCompleted(completed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.loadLocked()
	if err != nil {
		return err
	}
	got.FirstRunOnboardingCompleted = completed
	return s.saveLocked(got)
}

// dedupNonEmpty preserves order, drops empty strings, and coalesces
// duplicates. Used by SaveSkippedUpdateVersions so a defensive call
// from a buggy client round-trips cleanly.
func dedupNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// defaultSettings is the safe-baseline a fresh install starts with.
func defaultSettings() Settings {
	return Settings{
		SchemaVersion: 1,
		LastRoute:     "/sessions",
		Theme:         "system",
		Accent:        "default",
		WindowSize:    WindowSize{Width: 1280, Height: 800},
		// Branching UX dials (branching-ux-polish-01KQ8TD7 WP06).
		// AutoCollapseBranchesInSidebar is left nil here (not
		// &DefaultAutoCollapseBranchesInSidebar) — a fresh install and an
		// upgraded pre-WP03 install must be indistinguishable, and both
		// resolve to true via EffectiveAutoCollapseBranchesInSidebar's nil
		// check (controls-and-readouts-that-tell-the-truth-01PMZ808 WP03).
		// DeleteBranchesWithParent defaults to false (safe: orphan, not cascade).
		// MaxVisibleBranchDepth defaults to 5 (spec default).
		MaxVisibleBranchDepth: DefaultMaxVisibleBranchDepth,
	}
}

// API is the concrete SettingsAPI implementation, backed by a
// SettingsStore. Get and Set delegate to LoadAll / SaveAll under
// the store's own concurrency control.
type API struct {
	store           SettingsStore
	auditSettingsMu sync.Mutex
	auditSettings   AuditSettings
	// fleet holds the optional fleet client and data dir. Populated via
	// SetFleetClient during chassis boot; nil when fleet is not wired.
	fleet *fleetState

	// syncNotify is the optional fleet-sync mutation hook
	// (harness-fleet-sync-activation-01NSYNC01 gap #1). When set via
	// SetSyncNotifier, Set() calls it with the affected sync category so the
	// fleet Syncer can schedule a debounced push. nil when fleet sync is not
	// wired; the call is a no-op in that case.
	syncNotify func(category string)

	// auditRetentionEnforced backs AuditSettings.RetentionEnforced
	// (audit-that-tells-the-truth-01PMZA10 UNIT-4). Set via
	// SetAuditRetentionEnforced at chassis-boot wiring time — NEVER a
	// literal inside GetAuditSettings — so the value genuinely comes
	// from the wiring and UNIT-8 can flip it to a live check with a
	// one-line change at the wiring site and zero frontend edit.
	auditRetentionEnforced bool
}

// SetSyncNotifier wires the fleet-sync mutation hook. The rpc layer connects
// this to fleet.Syncer.NotifyMutation so that a settings change schedules a
// debounced push-up. Safe to leave unset (fleet disabled / OSS build).
func (a *API) SetSyncNotifier(fn func(category string)) {
	a.syncNotify = fn
}

// SetAuditRetentionEnforced wires whether the audit retention policy is
// actually enforced by a real sweep, backing
// AuditSettings.RetentionEnforced. Defaults to false (the honest
// pre-UNIT-8 state — nothing sweeps yet); core/rpc/api.go calls this at
// construction. UNIT-8 (audit-that-tells-the-truth-01PMZA10) is the
// commit that starts passing true once a real sweep exists.
func (a *API) SetAuditRetentionEnforced(enforced bool) {
	a.auditRetentionEnforced = enforced
}

// notifySync invokes the sync mutation hook for the given category when one
// is wired. Best-effort: never blocks the caller (the Syncer debounces).
func (a *API) notifySync(category string) {
	if a.syncNotify != nil {
		a.syncNotify(category)
	}
}

// NewAPI constructs a SettingsAPI backed by the given store. nil
// store falls back to an in-memory implementation so tests can run
// without touching the filesystem.
func NewAPI(store SettingsStore) *API {
	if store == nil {
		store = newMemoryStore()
	}
	return &API{store: store}
}

// Store exposes the underlying SettingsStore so the rpc bindings
// layer can use the same instance for LoadRoute / SaveRoute /
// LoadTheme / SaveTheme.
func (a *API) Store() SettingsStore { return a.store }

// Get returns the persisted settings (or defaults on missing-file).
func (a *API) Get(_ context.Context) (Settings, error) {
	return a.store.LoadAll()
}

// Set persists every field.
func (a *API) Set(_ context.Context, s Settings) error {
	if err := a.store.SaveAll(s); err != nil {
		return err
	}
	// Schedule a debounced push of the ui_theme category (the credential-free
	// surface this full-settings save can affect). The Syncer no-ops when the
	// category is disabled (harness-fleet-sync-activation-01NSYNC01 gap #1).
	// Literal mirrors fleet.SyncCategoryUITheme; impl.go deliberately avoids a
	// core/fleet import to keep the OSS-first boundary clean.
	a.notifySync("ui_theme")
	return nil
}

// LoadAutonomyProfile delegates to the store. Returns the empty Layer
// when no override is persisted.
func (a *API) LoadAutonomyProfile(_ context.Context) (autonomy.Layer, error) {
	return a.store.LoadAutonomyProfile()
}

// SaveAutonomyProfile delegates to the store.
func (a *API) SaveAutonomyProfile(_ context.Context, layer autonomy.Layer) error {
	return a.store.SaveAutonomyProfile(layer)
}

// GetMCPAutoRestart returns whether MCP servers should auto-restart.
// (mcp-server-health-ui-01KQ8TD6 WP06)
func (a *API) GetMCPAutoRestart(_ context.Context) (bool, error) {
	return a.store.LoadMCPAutoRestart()
}

// SetMCPAutoRestart persists the MCP auto-restart dial.
func (a *API) SetMCPAutoRestart(_ context.Context, enabled bool) error {
	return a.store.SaveMCPAutoRestart(enabled)
}

// GetAutoTitleEnabled returns whether session auto-titling is on.
// (p0-wiring-fixes-3TVMG0MX WP05)
func (a *API) GetAutoTitleEnabled(_ context.Context) (bool, error) {
	return a.store.LoadAutoTitleEnabled()
}

// SetAutoTitleEnabled persists the auto-title feature toggle.
func (a *API) SetAutoTitleEnabled(_ context.Context, enabled bool) error {
	return a.store.SaveAutoTitleEnabled(enabled)
}

// envArtifactBinaryPreview is the feature-flag env-var for the binary
// preview feature. Default: on (empty or any non-"false" value).
const envArtifactBinaryPreview = "HARNESS_ARTIFACT_BINARY_PREVIEW"

// envArtifactPreviewMaxBytes overrides the byte cap enforced by the frontend
// before handing bytes to a renderer. Default: 5 MiB.
const envArtifactPreviewMaxBytes = "HARNESS_ARTIFACT_PREVIEW_MAX_BYTES"

// defaultArtifactPreviewMaxBytes is 5 MiB.
const defaultArtifactPreviewMaxBytes int64 = 5 * 1024 * 1024

// defaultArtifactPreviewTimeoutMs is 2 seconds.
const defaultArtifactPreviewTimeoutMs int64 = 2000

// GetArtifactPreview returns the runtime artifact-preview feature config.
// (artifact-preview-binary-rendering-01KQ8TD5 WP07)
func (a *API) GetArtifactPreview(_ context.Context) (enabled bool, maxBytes int64, timeoutMs int64, err error) {
	enabled = os.Getenv(envArtifactBinaryPreview) != "false"
	maxBytes = defaultArtifactPreviewMaxBytes
	if v := os.Getenv(envArtifactPreviewMaxBytes); v != "" {
		if parsed, parseErr := parseInt64Positive(v); parseErr == nil {
			maxBytes = parsed
		}
	}
	timeoutMs = defaultArtifactPreviewTimeoutMs
	return enabled, maxBytes, timeoutMs, nil
}

// parseInt64Positive parses a decimal string and returns an error if the
// value is not a positive integer.
func parseInt64Positive(s string) (int64, error) {
	var n int64
	_, parseErr := fmt.Sscanf(s, "%d", &n)
	if parseErr != nil {
		return 0, parseErr
	}
	if n <= 0 {
		return 0, fmt.Errorf("settings: must be positive, got %d", n)
	}
	return n, nil
}


// GetEmbedderConfig returns the persisted (profileID, modelOverride)
// pair for the memory embedder.  Empty strings mean "auto-pick".
// (v0.5.2 universal-embedder fix)
func (a *API) GetEmbedderConfig(_ context.Context) (profileID, modelOverride string, err error) {
	return a.store.LoadEmbedderConfig()
}

// SetEmbedderConfig persists the embedder provider selection and
// optional model override.  Empty strings reset to auto-pick /
// per-Kind-default behaviour.
func (a *API) SetEmbedderConfig(_ context.Context, profileID, modelOverride string) error {
	return a.store.SaveEmbedderConfig(profileID, modelOverride)
}

// ── Memory narrative layer API methods (memory-narrative-layer-01KQ8TD1) ──

// GetSummarizerProfileID returns the configured summariser profile ID.
func (a *API) GetSummarizerProfileID(_ context.Context) (string, error) {
	return a.store.LoadSummarizerProfileID()
}

// SetSummarizerProfileID persists the summariser profile ID.
func (a *API) SetSummarizerProfileID(_ context.Context, profileID string) error {
	return a.store.SaveSummarizerProfileID(profileID)
}

// GetChatCustomInstructions returns the user's chat custom-instructions
// text. Empty means "no user layer". (system-prompt-layers WP04)
func (a *API) GetChatCustomInstructions(_ context.Context) (string, error) {
	return a.store.LoadChatCustomInstructions()
}

// SetChatCustomInstructions persists the chat custom-instructions text.
func (a *API) SetChatCustomInstructions(_ context.Context, text string) error {
	return a.store.SaveChatCustomInstructions(text)
}

// GetMemoryNarrativeEnabled returns the narrative-layer opt-in.
func (a *API) GetMemoryNarrativeEnabled(_ context.Context) (bool, error) {
	return a.store.LoadMemoryNarrativeEnabled()
}

// SetMemoryNarrativeEnabled persists the narrative-layer opt-in.
func (a *API) SetMemoryNarrativeEnabled(_ context.Context, enabled bool) error {
	return a.store.SaveMemoryNarrativeEnabled(enabled)
}

// ── Audit settings (audit-log-enhancement-01KX5R8F WP07) ────────────────────

// GetAuditSettings returns the current audit retention policy.
// Defaults to keep_forever if no policy has been set.
func (a *API) GetAuditSettings(_ context.Context) (AuditSettings, error) {
	// Stored as a JSON blob in the shared settings field. The simpler approach
	// is to keep it in-memory with a mutex until a dedicated store field lands.
	a.auditSettingsMu.Lock()
	defer a.auditSettingsMu.Unlock()
	if a.auditSettings.Strategy == "" {
		return AuditSettings{Strategy: "keep_forever", WindowDays: 90, RetentionEnforced: a.auditRetentionEnforced}, nil
	}
	out := a.auditSettings
	out.RetentionEnforced = a.auditRetentionEnforced
	return out, nil
}

// SetAuditSettings persists the audit retention policy.
func (a *API) SetAuditSettings(_ context.Context, s AuditSettings) error {
	a.auditSettingsMu.Lock()
	defer a.auditSettingsMu.Unlock()
	a.auditSettings = s
	return nil
}

// memoryStore is the test-only in-memory SettingsStore.
type memoryStore struct {
	mu   sync.Mutex
	data Settings
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: defaultSettings()}
}

func (m *memoryStore) LoadAll() (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data, nil
}

func (m *memoryStore) SaveAll(s Settings) error {
	if err := validateCompactionFields(s); err != nil {
		return err
	}
	if err := validateUpdateFields(s); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.SchemaVersion == 0 {
		s.SchemaVersion = 1
	}
	m.data = s
	return nil
}

func (m *memoryStore) LoadRoute() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.LastRoute, nil
}

func (m *memoryStore) SaveRoute(r string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.LastRoute = r
	return nil
}

func (m *memoryStore) LogRouteChange(_, _ string) error { return nil }

func (m *memoryStore) LoadTheme() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.Theme, nil
}

func (m *memoryStore) SaveTheme(t string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.Theme = t
	return nil
}

func (m *memoryStore) LoadMemory() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.MemoryEnabled, nil
}

func (m *memoryStore) SaveMemory(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.MemoryEnabled = enabled
	return nil
}

func (m *memoryStore) LoadConfirmEach() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.ConfirmEachEnabled(), nil
}

func (m *memoryStore) SaveConfirmEach(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.ConfirmEachDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadWebSearch() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.WebSearchEnabled, nil
}

func (m *memoryStore) SaveWebSearch(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.WebSearchEnabled = enabled
	return nil
}

func (m *memoryStore) LoadBash() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.BashEnabled, nil
}

func (m *memoryStore) SaveBash(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.BashEnabled = enabled
	return nil
}

func (m *memoryStore) LoadWebFetchEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.WebFetchEnabled, nil
}

func (m *memoryStore) SaveWebFetchEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.WebFetchEnabled = enabled
	return nil
}

func (m *memoryStore) LoadSaveArtifactEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.SaveArtifactEnabled(), nil
}

func (m *memoryStore) SaveSaveArtifactEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.SaveArtifactDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadMaxAgentTurns() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.MaxAgentTurns, nil
}

func (m *memoryStore) SaveMaxAgentTurns(turns int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if turns < 0 {
		turns = 0
	}
	m.data.MaxAgentTurns = turns
	return nil
}

// ── WP08 permission dial memoryStore accessors ────────────────────

func (m *memoryStore) LoadPermissionMode() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.EffectivePermissionMode(), nil
}

func (m *memoryStore) SavePermissionMode(mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.PermissionMode = mode
	return nil
}

func (m *memoryStore) LoadPermissionCacheDangerousOps() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.PermissionCacheDangerousOps, nil
}

func (m *memoryStore) SavePermissionCacheDangerousOps(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.PermissionCacheDangerousOps = enabled
	return nil
}

func (m *memoryStore) LoadBashAllowlistMigrated() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.BashAllowlistMigrated, nil
}

func (m *memoryStore) SaveBashAllowlistMigrated(migrated bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.BashAllowlistMigrated = migrated
	return nil
}

func (m *memoryStore) LoadPermissionsMigrationToastShown() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.PermissionsMigrationToastShown, nil
}

func (m *memoryStore) SavePermissionsMigrationToastShown(shown bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.PermissionsMigrationToastShown = shown
	return nil
}

func (m *memoryStore) LoadCedarStrictCredentialMode() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.CedarStrictCredentialMode, nil
}

func (m *memoryStore) SaveCedarStrictCredentialMode(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.CedarStrictCredentialMode = enabled
	return nil
}

func (m *memoryStore) LoadCedarStrictWorkflowMode() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.CedarStrictWorkflowMode, nil
}

func (m *memoryStore) SaveCedarStrictWorkflowMode(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.CedarStrictWorkflowMode = enabled
	return nil
}

func (m *memoryStore) LoadGraphAuthoringEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.GraphAuthoringEnabled, nil
}

func (m *memoryStore) SaveGraphAuthoringEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.GraphAuthoringEnabled = enabled
	return nil
}

func (m *memoryStore) LoadFSRequestAccessEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.FSRequestAccessEnabled(), nil
}

func (m *memoryStore) SaveFSRequestAccessEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.FSRequestAccessDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadMonthlyCostNotifyUSD() (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.MonthlyCostNotifyUSD, nil
}

func (m *memoryStore) SaveMonthlyCostNotifyUSD(usd float64) error {
	if usd < 0 {
		usd = 0
	}
	if usd > MaxMonthlyCostNotifyUSD {
		return ErrInvalidMonthlyCostNotifyUSD
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.MonthlyCostNotifyUSD = usd
	return nil
}

func (m *memoryStore) LoadAutonomyProfile() (autonomy.Layer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return decodeAutonomyField(m.data.Autonomy)
}

func (m *memoryStore) SaveAutonomyProfile(layer autonomy.Layer) error {
	encoded, err := encodeAutonomyField(layer)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.Autonomy = encoded
	return nil
}

// ── MCP auto-restart memoryStore accessors (mcp-server-health-ui WP06) ──

func (m *memoryStore) LoadMCPAutoRestart() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.MCPAutoRestart(), nil
}

func (m *memoryStore) SaveMCPAutoRestart(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.MCPAutoRestartDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadAutoTitleEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.AutoTitleEnabled(), nil
}

func (m *memoryStore) SaveAutoTitleEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.AutoTitleDisabled = !enabled
	return nil
}

// ── Key-rotation memoryStore accessors (provider-keychain-rotation-01KQ8TD9 WP07) ──

func (m *memoryStore) LoadAutoResumeOnKeyRotation() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.EffectiveAutoResumeOnKeyRotation(), nil
}

func (m *memoryStore) SaveAutoResumeOnKeyRotation(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.AutoResumeOnKeyRotationDisabled = !enabled
	return nil
}

// ── Builtin filesystem tool memoryStore accessors (builtin-filesystem-tools-01KR3N4P) ──

func (m *memoryStore) LoadFSReadEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.FSReadEnabled(), nil
}

func (m *memoryStore) SaveFSReadEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.FSReadDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadFSWriteEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.FSWriteEnabled(), nil
}

func (m *memoryStore) SaveFSWriteEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.FSWriteDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadEditFileArtifactSyncEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.EditFileArtifactSyncEnabled(), nil
}

func (m *memoryStore) SaveEditFileArtifactSyncEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.EditFileArtifactSyncDisabled = !enabled
	return nil
}

// ── Todo tool memoryStore accessors (builtin-tools-search-and-elicitation-01KZNP3D) ──

func (m *memoryStore) LoadTodoEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.TodoEnabled(), nil
}

func (m *memoryStore) SaveTodoEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.TodoDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadEmbedderConfig() (profileID, modelOverride string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.EmbedderProviderProfileID, m.data.EmbedderModelOverride, nil
}

func (m *memoryStore) SaveEmbedderConfig(profileID, modelOverride string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.EmbedderProviderProfileID = profileID
	m.data.EmbedderModelOverride = modelOverride
	return nil
}

// ── Memory narrative layer memoryStore accessors (memory-narrative-layer-01KQ8TD1) ──

func (m *memoryStore) LoadSummarizerProfileID() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.SummarizerProfileID, nil
}

func (m *memoryStore) SaveSummarizerProfileID(profileID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.SummarizerProfileID = profileID
	return nil
}

func (m *memoryStore) LoadChatCustomInstructions() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.ChatCustomInstructions, nil
}

func (m *memoryStore) SaveChatCustomInstructions(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.ChatCustomInstructions = text
	return nil
}

func (m *memoryStore) LoadMemoryNarrativeEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.MemoryNarrativeEnabled, nil
}

func (m *memoryStore) SaveMemoryNarrativeEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.MemoryNarrativeEnabled = enabled
	return nil
}

// ── Auto-update memoryStore accessors (auto-update-v0.4.0 WP05) ────────

func (m *memoryStore) LoadAutoCheckUpdates() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.AutoCheckUpdates(), nil
}

func (m *memoryStore) SaveAutoCheckUpdates(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.AutoCheckUpdatesDisabled = !enabled
	return nil
}

func (m *memoryStore) LoadUpdateChannel() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.EffectiveUpdateChannel(), nil
}

func (m *memoryStore) SaveUpdateChannel(channel string) error {
	switch channel {
	case "", UpdateChannelStable, UpdateChannelPrerelease:
		// ok
	default:
		return ErrInvalidUpdateChannel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.UpdateChannel = channel
	return nil
}

func (m *memoryStore) LoadUpdateCheckInterval() (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Duration(m.data.EffectiveUpdateCheckIntervalSec()) * time.Second, nil
}

func (m *memoryStore) SaveUpdateCheckInterval(d time.Duration) error {
	secs := int(d / time.Second)
	if secs < 0 {
		secs = 0
	}
	if secs != 0 && (secs < MinUpdateCheckIntervalSec || secs > MaxUpdateCheckIntervalSec) {
		return ErrInvalidUpdateCheckInterval
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.UpdateCheckIntervalSec = secs
	return nil
}

func (m *memoryStore) LoadSkippedUpdateVersions() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.data.SkippedUpdateVersions) == 0 {
		return []string{}, nil
	}
	out := make([]string, len(m.data.SkippedUpdateVersions))
	copy(out, m.data.SkippedUpdateVersions)
	return out, nil
}

func (m *memoryStore) SaveSkippedUpdateVersions(versions []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.SkippedUpdateVersions = dedupNonEmpty(versions)
	return nil
}

func (m *memoryStore) AppendSkippedUpdateVersion(version string) error {
	if version == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.data.SkippedUpdateVersions {
		if v == version {
			return nil
		}
	}
	m.data.SkippedUpdateVersions = append(m.data.SkippedUpdateVersions, version)
	return nil
}

func (m *memoryStore) RemoveSkippedUpdateVersion(version string) error {
	if version == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.data.SkippedUpdateVersions[:0]
	for _, v := range m.data.SkippedUpdateVersions {
		if v != version {
			out = append(out, v)
		}
	}
	m.data.SkippedUpdateVersions = out
	return nil
}

// ── First-run onboarding completion flag memoryStore (harness-onboarding-01NHON01 WP01) ──

func (m *memoryStore) LoadFirstRunOnboardingCompleted() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.FirstRunOnboardingCompleted, nil
}

func (m *memoryStore) SaveFirstRunOnboardingCompleted(completed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.FirstRunOnboardingCompleted = completed
	return nil
}
