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
}

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
}

// SettingsAPI is the view-scoped accessor exposed via HarnessAPI.
type SettingsAPI interface {
	Get(ctx context.Context) (Settings, error)
	Set(ctx context.Context, s Settings) error
}
