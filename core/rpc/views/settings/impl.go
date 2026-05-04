// Package settings's impl provides a JSON-file backed SettingsAPI +
// SettingsStore.
//
// Persistence: $USER_CONFIG_DIR/kaneaz-harness/settings.json (privacy
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
	"unicode"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/compaction"
)

// FileStore is a SettingsStore backed by a single JSON file. Safe for
// concurrent use; reads parse on demand, writes serialize through a
// mutex to prevent torn writes.
type FileStore struct {
	mu   sync.Mutex
	path string
}

// NewFileStore returns a store rooted at <userConfigDir>/kaneaz-harness/.
// The directory is created on first write.
func NewFileStore(userConfigDir string) (*FileStore, error) {
	if userConfigDir == "" {
		return nil, errors.New("settings: empty user config dir")
	}
	dir := filepath.Join(userConfigDir, "kaneaz-harness")
	return &FileStore{path: filepath.Join(dir, "settings.json")}, nil
}

// NewFileStoreFromEnv resolves USER_CONFIG_DIR via os.UserConfigDir.
func NewFileStoreFromEnv() (*FileStore, error) {
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

// validateCompactionFields runs the per-field validation rules for the
// four compaction settings (mission compaction-strategy-ui-01KQ8TDI
// WP06 acceptance: clamp rejects out-of-range values at SAVE, not just
// at Effective-time clamp). Returns the typed error so the rpc layer
// can render specific copy in the UI.
//
// Empty / zero values are valid for every field — they resolve to the
// documented defaults via the EffectiveCompaction* accessors.
func validateCompactionFields(in Settings) error {
	switch compaction.CompactionAggressiveness(in.CompactionAggressiveness) {
	case "",
		compaction.AggressivenessOff,
		compaction.AggressivenessConservative,
		compaction.AggressivenessBalanced,
		compaction.AggressivenessAggressive,
		compaction.AggressivenessMaximal:
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

	return nil
}

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

// LoadSaveArtifactEnabled returns the kaneaz__save_artifact built-in
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

// LoadFSRequestAccessEnabled returns the kaneaz__request_filesystem_access
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

// defaultSettings is the safe-baseline a fresh install starts with.
func defaultSettings() Settings {
	return Settings{
		SchemaVersion: 1,
		LastRoute:     "/sessions",
		Theme:         "system",
		Accent:        "default",
		WindowSize:    WindowSize{Width: 1280, Height: 800},
	}
}

// API is the concrete SettingsAPI implementation, backed by a
// SettingsStore. Get and Set delegate to LoadAll / SaveAll under
// the store's own concurrency control.
type API struct {
	store SettingsStore
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
	return a.store.SaveAll(s)
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
