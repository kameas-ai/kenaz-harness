// Package replay implements the deterministic, byte-stable lazy
// iterator over a session's events (FR-009 / NFR-004). Default replay
// applies current visibility under any redaction-supersession events;
// Raw mode exposes original visibility for incident response only and
// is itself logged.
package replay
