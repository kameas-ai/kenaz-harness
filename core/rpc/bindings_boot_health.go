package rpc

import "sync"

// bootHealthStore holds init-phase errors recorded by the harness startup
// wiring. The store is shared between SetBootErrors (called once at startup)
// and BootHealth_Get (called by the frontend on demand). A mutex keeps it
// race-safe in tests that call SetBootErrors from a goroutine.
type bootHealthStore struct {
	mu     sync.RWMutex
	report BootHealthReport
}

// global store wired to every Bindings via the package-level singleton.
// Tests that need isolation can create a fresh *Bindings with their own
// store by calling newBindingsWithBootStore (unexported, for test use).
var globalBootStore = &bootHealthStore{}

// SetBootErrors records the init-phase errors from the named subsystems.
// Should be called once by the harness startup path (harness_wiring.go)
// immediately after the three subsystems are initialised. Safe to call
// concurrently; later calls overwrite earlier ones.
func SetBootErrors(mcpErr, skillsErr, fleetErr string) {
	globalBootStore.mu.Lock()
	defer globalBootStore.mu.Unlock()
	globalBootStore.report = BootHealthReport{
		MCPInitError:    mcpErr,
		SkillsInitError: skillsErr,
		FleetInitError:  fleetErr,
	}
}

// BootHealth_Get returns a snapshot of the boot-phase health report. The
// frontend calls this once on startup (e.g. from App.vue onMounted) to
// decide whether to display a "boot error" banner for each subsystem.
// An empty MCPInitError / SkillsInitError / FleetInitError means the
// corresponding subsystem is healthy (FR-008 / agent-loop-robustness-parity WP08).
func (b *Bindings) BootHealth_Get() BootHealthReport {
	globalBootStore.mu.RLock()
	defer globalBootStore.mu.RUnlock()
	return globalBootStore.report
}
