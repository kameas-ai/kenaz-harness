// Package settings defines the SettingsAPI view-scoped accessor and
// the SettingsStore implementation interface backing it. Persistence
// is a single JSON file per privacy CI invariant #5 (plan §4.3).
package settings

import "context"

// Settings is the persisted UI state shape (plan §5.5). schemaVersion
// gates migrations; lastRoute drives first-paint route restoration.
//
// MemoryEnabled is the explicit opt-in for the cross-session long-term
// memory feature. Privacy default is OFF: when false the harness never
// embeds, queries, or injects memory chunks regardless of what is on disk.
type Settings struct {
	SchemaVersion int        `json:"schemaVersion"`
	LastRoute     string     `json:"lastRoute"`
	Theme         string     `json:"theme"`
	Accent        string     `json:"accent"`
	WindowSize    WindowSize `json:"windowSize"`
	MemoryEnabled bool       `json:"memoryEnabled"`
	// ConfirmEachDisabled is the inverted persisted form of the
	// WP05 confirm-each tool-call modal flag. We store the disabled
	// bit (default false → modal ENABLED) so the zero value of a
	// freshly-installed settings file matches the spec's "default
	// ON" requirement without an extra Configured marker.
	//
	// Frontend / toolloop callers should never read this directly —
	// use ConfirmEachEnabled() helper or the Settings_GetConfirmEach
	// binding which inverts on the boundary.
	ConfirmEachDisabled bool `json:"confirmEachDisabled"`

	// Artifacts capture settings (artifacts-storage FR-017). All four
	// fields use a zero-value-friendly persisted form: AutoCapture*
	// stores the inverted "Disabled" bit so a fresh install (zero
	// values across the board) matches the spec's defaults
	// (capture ON, 10 lines, 200 bytes). The MinLines / MinBytes
	// zero-values fall back to the spec defaults via the helper
	// methods on the loaded record.
	AutoCaptureCodeBlocksDisabled  bool `json:"autoCaptureCodeBlocksDisabled,omitempty"`
	CodeBlockMinLines              int  `json:"codeBlockMinLines,omitempty"`
	CodeBlockMinBytes              int  `json:"codeBlockMinBytes,omitempty"`
	AutoCaptureToolOutputsDisabled bool `json:"autoCaptureToolOutputsDisabled,omitempty"`
}

// AutoCaptureCodeBlocks reports whether the code-block detector is
// active. Default true on a fresh install (zero-value Disabled).
func (s Settings) AutoCaptureCodeBlocks() bool { return !s.AutoCaptureCodeBlocksDisabled }

// AutoCaptureToolOutputs reports whether the tool-output detector is
// active. Default true on a fresh install.
func (s Settings) AutoCaptureToolOutputs() bool { return !s.AutoCaptureToolOutputsDisabled }

// EffectiveCodeBlockMinLines returns the user-tuned threshold or the
// spec default (10) when the persisted value is zero.
func (s Settings) EffectiveCodeBlockMinLines() int {
	if s.CodeBlockMinLines <= 0 {
		return 10
	}
	return s.CodeBlockMinLines
}

// EffectiveCodeBlockMinBytes returns the user-tuned threshold or the
// spec default (200) when the persisted value is zero.
func (s Settings) EffectiveCodeBlockMinBytes() int {
	if s.CodeBlockMinBytes <= 0 {
		return 200
	}
	return s.CodeBlockMinBytes
}

// ConfirmEachEnabled is the user-facing form of the WP05 modal flag.
// Defaults to true on a fresh install (zero-value ConfirmEachDisabled)
// and inverts the persisted bit so callers don't have to think about
// the storage shape.
func (s Settings) ConfirmEachEnabled() bool { return !s.ConfirmEachDisabled }

// WindowSize mirrors the charter's WindowSize type.
type WindowSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SettingsStore is the persistence interface (default impl: single
// JSON file at $USER_CONFIG_DIR/kaneaz-harness/settings.json).
type SettingsStore interface {
	LoadAll() (Settings, error)
	SaveAll(Settings) error
	LoadRoute() (string, error)
	SaveRoute(string) error
	LogRouteChange(from, to string) error
	LoadTheme() (string, error)
	SaveTheme(string) error
	// LoadMemory / SaveMemory expose the long-term-memory opt-in
	// independently of the full Settings round-trip so the rpc layer
	// can read it on the hot path (every send) without serializing
	// the whole record.
	LoadMemory() (bool, error)
	SaveMemory(enabled bool) error
	// LoadConfirmEach / SaveConfirmEach expose the WP05 confirm-each
	// tool-call modal opt-in independently of the full Settings
	// record. The toolloop reads this on every Run boundary so the
	// frontend toggle takes effect on the next chat without a
	// settings round-trip. Default true (modal ON unless the user
	// turns it off explicitly).
	LoadConfirmEach() (bool, error)
	SaveConfirmEach(enabled bool) error
}

// SettingsAPI is the view-scoped accessor exposed via HarnessAPI.
type SettingsAPI interface {
	Get(ctx context.Context) (Settings, error)
	Set(ctx context.Context, s Settings) error
}
