package session

// ---------------------------------------------------------------------------
// Which model-visible-history mode a session was created under
// (model-moves-transcript-01PMCH01 WP03, spec §4).
//
// The gate that decides what the MODEL sees has two inputs, and this
// file owns the durable one.
//
//	LIVE INPUT   Settings.MoveFidelityHistoryEnabled(), read at the point
//	             of consumption on every history read. Turning it off is
//	             an instant, restart-free revert lever for every session
//	             at once — which is most of what a revert lever is worth.
//
//	DURABLE INPUT  this: the mode the session was OPENED under, stamped
//	             once at creation and never rewritten. It is what stops
//	             an in-flight conversation from changing shape underneath
//	             the model mid-thread when the dial moves, and it is why
//	             spec §4 says "sessions record which mode wrote them".
//
// Effective fidelity is the AND of the two, resolved fail-closed: see
// the composition in core/rpc/model_history.go. Anything ambiguous —
// NULL mode, unrecognised mode, unreadable settings — lands on the
// classic composition, which is the shape that shipped before this
// mission and the one no provider can reject on our account.
// ---------------------------------------------------------------------------

// MoveHistoryMode names the model-visible-history mode a session was
// created under. Persisted in sessions.move_history_mode (migration
// 0334).
type MoveHistoryMode string

const (
	// MoveHistoryModeClassic is one flattened assistant message per human
	// turn — the composition that shipped before this mission. Stamped on
	// sessions created while the dial was off.
	MoveHistoryModeClassic MoveHistoryMode = "classic"
	// MoveHistoryModeMoves is the full move chain in provider-native
	// shape. Stamped on sessions created while the dial was on.
	MoveHistoryModeMoves MoveHistoryMode = "moves"
)

// MoveHistoryModeFromRecord resolves the durable half of the gate for a
// session record, FAIL-CLOSED.
//
// The empty string is the load-bearing case, not an edge case: it is
// every session that existed before migration 0334 added the column, and
// spec §4 requires exactly those to keep the composition they were
// written with. An unrecognised value resolves the same way — a mode
// this build does not understand is not a mode it should act on.
//
// This is the ONLY reader of the column's vocabulary. Callers get a bool
// rather than the enum so there is no second place that decides what
// "moves" means.
func MoveHistoryModeFromRecord(stored string) bool {
	return MoveHistoryMode(stored) == MoveHistoryModeMoves
}

// MoveHistoryModeForNewSession renders the mode to stamp on a session
// being created, given the live dial's position.
//
// Deliberately total: there is no "unset" return. A session created
// after migration 0334 always records which mode wrote it, so the NULL
// in the column means one specific thing forever — "created before this
// mission" — rather than degrading into "created recently but nobody
// remembered to stamp it".
func MoveHistoryModeForNewSession(dialEnabled bool) MoveHistoryMode {
	if dialEnabled {
		return MoveHistoryModeMoves
	}
	return MoveHistoryModeClassic
}
