package event

// Log is the integration façade core.Core depends on for event-log
// lifecycle. The full event-log implementation lives across
// core/event/log/, core/event/redact/, core/event/chain/, etc.
// (event-log mission). This minimal interface keeps core/core.go
// compiling until the real Emitter / Reader surface is wired in.
//
// Consumers of the real event log should import core/event subpackages
// directly; this façade is only for the top-level Core lifecycle.
type Log interface {
	Close() error
}
