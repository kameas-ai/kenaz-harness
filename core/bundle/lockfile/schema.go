package lockfile

// Schema versions and migration table.
//
// Adding a new schema version:
//   1. Bump CurrentSchemaVersion above.
//   2. Add an entry to migrations[] documenting the change.
//   3. If older lockfiles must be readable, add a converter; otherwise the
//      reader will reject them with bundle.ErrSchemaUnsupported.
//
// Lockfile schema is intentionally rigid in v1 — callers should regenerate
// rather than try to migrate in place.

// Migration documents a transition between two schema versions.
type Migration struct {
	From       int
	To         int
	Summary    string
	Breaking   bool
}

// Migrations is the documented list of schema transitions.
var Migrations = []Migration{
	// v1 is the initial release; no prior schema exists.
}
