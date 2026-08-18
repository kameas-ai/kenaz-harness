package menu

// ThemeMode is the user-selected colour theme.
// Values mirror the frontend useTheme composable: "light" | "dark" | "system".
type ThemeMode string

const (
	ThemeLight  ThemeMode = "light"
	ThemeDark   ThemeMode = "dark"
	ThemeSystem ThemeMode = "system"
)

// UpdateMenuState carries the minimal update-state needed to label the
// Help → Check for Updates menu item.
type UpdateMenuState int

const (
	// UpdateIdle — no update available or check not yet performed.
	UpdateIdle UpdateMenuState = iota
	// UpdateAvailable — a newer version exists; user has not started download.
	UpdateAvailable
	// UpdateDownloading — binary download in progress.
	UpdateDownloading
	// UpdateStaged — download complete; waiting for user to confirm restart.
	UpdateStaged
	// UpdateFailed — last check/download attempt failed.
	UpdateFailed
)

// UpdateMenuLabel returns the Help → "Check for Updates" label text for the
// given UpdateMenuState.
func UpdateMenuLabel(s UpdateMenuState) string {
	switch s {
	case UpdateAvailable:
		return "Install Update"
	case UpdateDownloading:
		return "Downloading…"
	case UpdateStaged:
		return "Install & Restart"
	case UpdateFailed:
		return "Retry Update"
	default:
		return "Check for Updates…"
	}
}

// updateTopicState maps the self-update broker topics this package's
// Help-menu subscriber reacts to onto the UpdateMenuState each drives
// (self-update-repair-01PMUP01 WP03). Deliberately literal strings, not
// imported constants from core/rpc/views/update: this package stays
// wiring-agnostic of core/rpc by design (see adapters.go — main.go wires
// closures rather than handing this package the full rpc surface). The
// values MUST match updateview.TopicDownload{Progress,Complete,Failed}
// and the update:available topic published by core/update.Service; drift
// between the two is exactly what scripts/ci/check-broker-topic-consumers.sh
// (WP06) and TestUpdateTopicState_MatchesProductionTopics guard against.
var updateTopicState = map[string]UpdateMenuState{
	"update:available":         UpdateAvailable,
	"update:download-progress": UpdateDownloading,
	"update:download-complete": UpdateStaged,
	"update:download-failed":   UpdateFailed,
}

// UpdateTopicState maps a self-update broker topic literal to the
// UpdateMenuState it drives. ok is false for topics this package does
// not react to (the caller should leave the current state unchanged).
//
// This is "the subscriber" in the sense AC-8 means it: main.go's
// wailsruntime.EventsOn callbacks call this exact function rather than
// re-deriving the mapping inline, so a Go test exercising
// UpdateTopicState against the real topic strings a production
// update.Manager publishes IS exercising the subscriber logic — not
// hand-setting MenuState.UpdateState. The only part a Go test cannot
// reach is the wailsruntime.EventsOn Wails-IPC round-trip itself (same
// limit the pre-existing update:available wiring already had).
func UpdateTopicState(topic string) (state UpdateMenuState, ok bool) {
	s, known := updateTopicState[topic]
	return s, known
}

// SessionRef is a minimal session pointer for the File → Open Recent submenu.
type SessionRef struct {
	ID    string
	Title string
}

// MenuState is the snapshot of application state that the menu builder reads.
// It contains no fleet-identity fields — per spec FR-005 the menu bar carries
// no fleet-bound items.
type MenuState struct {
	// ThemeMode is the currently active colour theme.
	ThemeMode ThemeMode
	// UpdateState determines the Help → Check for Updates label.
	UpdateState UpdateMenuState
	// RecentSessions is the ordered (most-recent first) list of sessions
	// to populate File → Open Recent. Capped at 10.
	RecentSessions []SessionRef
}
