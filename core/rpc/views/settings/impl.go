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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(in)
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
