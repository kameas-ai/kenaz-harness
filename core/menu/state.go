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
