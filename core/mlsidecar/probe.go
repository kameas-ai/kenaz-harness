package mlsidecar

// Healthy implements core/advice.SidecarProbe (the WP12 amendment that
// replaces ResolveAdvisorModel's placeholder ollama-profile scan for
// RungLocalLaya). It reads ONLY the cached Status — no network call, no
// spawn — because a resolver invoked at process boot (core/rpc/api.go's
// unconditional "advice.laya_ladder.boot_resolve" call site) must never
// itself become a lazy-start trigger; that would silently violate
// design item 5 ("Lazy start on first advisor demand, not app boot") the
// moment the ladder ran once at boot. Reconcile/Ensure is the only path
// that ever talks to the network or spawns anything.
func (m *Manager) Healthy() bool {
	return m.Status().State == StateHealthy
}

// Identity implements core/advice.SidecarProbe: the resolved "model" id
// for the RungLocalLaya rung once Healthy() is true.
func (m *Manager) Identity() string {
	s := m.Status()
	if s.EngineVersion == "" {
		return "kenaz-ml-sidecar"
	}
	return "kenaz-ml-sidecar@" + s.EngineVersion
}
