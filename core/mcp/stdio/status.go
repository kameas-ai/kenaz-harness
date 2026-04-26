package stdio

import "time"

// StatusStderrTailBytes is the cap on stderr tail bytes embedded in
// each RecipeStatus. 4 KiB is enough for the last 50-100 lines of
// a misbehaving server's output without ballooning the JSON-RPC
// response that wraps a status snapshot.
const StatusStderrTailBytes = 4 * 1024

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

// RecipeStatus produces the live snapshot for this instance. Holds
// the lifecycle lock for the duration so RestartAttempts, State,
// and LastRestartAt agree.
//
// The plumbing layer cannot evaluate keychain presence (recipes /
// secrets resolution is a WP04 concern), so KeysPresent is hard-
// coded to true and the upstream RPC view overlays the real value.
func (s *ServerInstance) RecipeStatus() RecipeStatus {
	negotiated := s.Negotiated()
	tools := s.Tools()

	s.lifecycleMu.Lock()
	state := s.state
	lastError := s.lastError
	lastRestartAt := s.lastRestartAt
	pruned := pruneHistory(s.restartHistory, s.opts.Now(), restartWindow)
	restartAttempts := len(pruned)
	pid := 0
	// Only surface a PID when we're confident the cmd refers to a
	// live, post-handshake child. During restarting the cmd
	// pointer briefly aliases the dying process; better to report
	// 0 than a misleading number.
	if state == StateRunning && s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}
	s.lifecycleMu.Unlock()

	tail := s.stderr.Snapshot(StatusStderrTailBytes)
	if lastError == "" && state == StateFailed && tail != "" {
		// When the supervisor has given up but no explicit error
		// was captured (e.g., the reader saw clean EOF), surface
		// the stderr tail so users have something to work with.
		lastError = tail
	}

	return RecipeStatus{
		ID:              s.id,
		Enabled:         true,
		State:           string(state),
		LastError:       lastError,
		RestartAttempts: restartAttempts,
		LastRestartAt:   lastRestartAt,
		KeysPresent:     true,
		PID:             pid,
		ProtocolVersion: negotiated.ProtocolVersion,
		ServerName:      negotiated.ServerInfo.Name,
		ServerVersion:   negotiated.ServerInfo.Version,
		ToolCount:       len(tools),
		ResourceCount:   0,
		PromptCount:     0,
		StderrTail:      tail,
		UpdatedAt:       s.opts.Now(),
	}
}

// RecipeStatus is the public read accessor on Pool. Returns the
// snapshot and ok=true for a known recipe id. ok=false signals an
// unknown id (or a closed pool).
func (p *Pool) RecipeStatus(id string) (RecipeStatus, bool) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return RecipeStatus{}, false
	}
	inst, ok := p.servers[id]
	p.mu.RUnlock()
	if !ok {
		return RecipeStatus{}, false
	}
	return inst.RecipeStatus(), true
}

// State returns the current lifecycle state. Useful for tests and
// upstream callers that don't need the full RecipeStatus snapshot.
func (s *ServerInstance) State() State {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.state
}
