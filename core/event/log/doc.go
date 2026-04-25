// Package log is the unexported storage adapter for core/event.
//
// DIRECTIVE_001 / C-001: nothing outside core/event/ may import this
// package. The Store type is intentionally unexported; Emitter and
// Reader constructors in core/event reach the store directly.
//
// C-002 (append-only at storage): the store exposes no Update, Delete,
// or Patch primitive. Retention archive / truncate are explicit
// operations recorded in the events table itself.
package log
