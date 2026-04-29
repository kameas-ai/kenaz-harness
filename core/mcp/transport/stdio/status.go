package stdio

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
