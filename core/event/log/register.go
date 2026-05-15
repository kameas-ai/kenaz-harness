package log

import (
	_ "embed"
)

// Migration is the storage-foundations migration shape stubbed here
// (the real interface lives in core/storage; this mission depends on
// storage-foundations-01KQ1A3K landing). The shape is the lowest
// reasonable common denominator: an ordered (id, sql) tuple.
type Migration struct {
	ID  string
	SQL string
}

//go:embed migrations/0001_events.sql
var migration0001 string

//go:embed migrations/0002_event_chain_heads.sql
var migration0002 string

//go:embed migrations/0003_redaction_rules.sql
var migration0003 string

//go:embed migrations/0004_retention_config.sql
var migration0004 string

//go:embed migrations/0005_schema_version.up.sql
var migration0005 string

//go:embed migrations/0006_saved_audit_queries.up.sql
var migration0006 string

// Migrations returns the event-log migrations in dependency order.
// The harness bootstrap calls this function and forwards the result
// to the storage-foundations migration framework.
func Migrations() []Migration {
	return []Migration{
		{ID: "0001_events", SQL: migration0001},
		{ID: "0002_event_chain_heads", SQL: migration0002},
		{ID: "0003_redaction_rules", SQL: migration0003},
		{ID: "0004_retention_config", SQL: migration0004},
		{ID: "0005_schema_version", SQL: migration0005},
		{ID: "0006_saved_audit_queries", SQL: migration0006},
	}
}

// Registry abstracts the storage-foundations migration framework.
// Stubbed here as a minimal interface; storage-foundations defines
// the real one.
type Registry interface {
	Register(owner string, migrations []Migration) error
}

// RegisterMigrations registers the four event-log migrations with the
// supplied registry. Owner is "core/event" by convention so audit
// tooling can attribute schema rows.
func RegisterMigrations(reg Registry) error {
	return reg.Register("core/event", Migrations())
}
