// Package branch implements the branch primitive (FR-010): given a
// parent session and an event id E, compute a Replay Snapshot up to E
// and seed a new child session with a single
// event-log.session.branched event referencing
// (parent_session_id, parent_event_id).
package branch
