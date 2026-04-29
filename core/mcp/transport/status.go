package transport

import "time"

// StatusStderrTailBytes is the cap on stderr tail bytes embedded in
// each RecipeStatus. 4 KiB is enough for the last 50-100 lines of
// a misbehaving server's output without ballooning the JSON-RPC
// response that wraps a status snapshot.
const StatusStderrTailBytes = 4 * 1024

// State values for the per-instance lifecycle state machine. Per
// data-model.md §ServerInstance:
//
//	stopped → starting → running → restarting → running … → failed
//
// The pool transitions an instance to `stopped` once Close completes;
// `failed` is sticky until a user-driven toggle clears the
// restartHistory and re-spawns.
type State string

const (
	StateStopped    State = "stopped"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateRestarting State = "restarting"
	StateFailed     State = "failed"
)

// RecipeStatus is the user-facing live snapshot of one server
// instance, mirrored field-for-field from data-model.md so the
// upstream RPC view (WP05) can surface it through
// `Tools_RecipeStatus(id)` without any further translation.
//
// Fields are populated under the instance's lifecycle lock, so the
// snapshot is consistent with respect to one moment in time —
// callers don't have to worry about RestartAttempts and State
// disagreeing.
//
// Recipe-level fields (Enabled, KeysPresent — modulo the
// plumbing-layer "always true" placeholder) are owned by upstream
// catalog code and overlaid on the snapshot WP05 returns through
// the RPC view; the stdio layer fills only what it knows.
type RecipeStatus struct {
	ID              string    `json:"id"`
	Enabled         bool      `json:"enabled"`
	State           string    `json:"state"`
	LastError       string    `json:"last_error,omitempty"`
	RestartAttempts int       `json:"restart_attempts"`
	LastRestartAt   time.Time `json:"last_restart_at,omitempty"`
	KeysPresent     bool      `json:"keys_present"`
	PID             int       `json:"pid"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	ServerName      string    `json:"server_name,omitempty"`
	ServerVersion   string    `json:"server_version,omitempty"`
	ToolCount       int       `json:"tool_count"`
	ResourceCount   int       `json:"resource_count"`
	PromptCount     int       `json:"prompt_count"`
	StderrTail      string    `json:"stderr_tail,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}
