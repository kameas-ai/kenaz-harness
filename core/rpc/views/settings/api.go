// Package settings defines the SettingsAPI view-scoped accessor and
// the SettingsStore implementation interface backing it. Persistence
// is a single JSON file per privacy CI invariant #5 (plan §4.3).
package settings

import "context"

// Settings is the persisted UI state shape (plan §5.5). schemaVersion
// gates migrations; lastRoute drives first-paint route restoration.
type Settings struct {
	SchemaVersion int        `json:"schemaVersion"`
	LastRoute     string     `json:"lastRoute"`
	Theme         string     `json:"theme"`
	Accent        string     `json:"accent"`
	WindowSize    WindowSize `json:"windowSize"`
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
}

// SettingsAPI is the view-scoped accessor exposed via HarnessAPI.
type SettingsAPI interface {
	Get(ctx context.Context) (Settings, error)
	Set(ctx context.Context, s Settings) error
}
