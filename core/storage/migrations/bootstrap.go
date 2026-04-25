package migrations

import "context"

// StorageBootstrap returns the storage mission's reserved migrations
// (versions 1-2). They create harness_meta and harness_migrations
// themselves. Storage's Open implementation registers them before any
// consuming mission's migrations.
//
// Note: harness_meta and harness_migrations are also created by
// EnsureLedger to make the ledger itself available before the migration
// runner can record the first apply. The migrations below are the
// canonical declared form so the ledger has rows referencing them and
// the hash check has something to verify.
func StorageBootstrap() []Migration {
	const installInitDDL = `
        CREATE TABLE IF NOT EXISTS harness_meta (
            install_id          TEXT NOT NULL PRIMARY KEY,
            created_at          TEXT NOT NULL,
            libsql_version      TEXT NOT NULL DEFAULT '',
            sqlite_version      TEXT NOT NULL DEFAULT '',
            schema_version      INTEGER NOT NULL DEFAULT 0,
            encryption_status   TEXT NOT NULL DEFAULT 'disabled',
            wal_mode            INTEGER NOT NULL DEFAULT 1,
            foreign_keys_on     INTEGER NOT NULL DEFAULT 1
        );
    `
	const ledgerInitDDL = `
        CREATE TABLE IF NOT EXISTS harness_migrations (
            rowid                       INTEGER PRIMARY KEY AUTOINCREMENT,
            version                     INTEGER NOT NULL,
            id                          TEXT NOT NULL,
            applied_at                  TEXT NOT NULL,
            content_hash                TEXT NOT NULL,
            owning_mission              TEXT NOT NULL,
            action                      TEXT NOT NULL,
            rolled_back_from_version    INTEGER NULL
        );
        CREATE INDEX IF NOT EXISTS idx_harness_migrations_version
            ON harness_migrations (version);
        CREATE INDEX IF NOT EXISTS idx_harness_migrations_action
            ON harness_migrations (action);
    `

	return []Migration{
		{
			ID:            "storage/0001-harness-meta",
			Version:       1,
			OwningMission: "storage",
			UpSource:      installInitDDL,
			Up: func(ctx context.Context, tx WriteTx) error {
				_, err := tx.Exec(ctx, installInitDDL)
				return err
			},
			Down: func(ctx context.Context, tx WriteTx) error {
				_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS harness_meta")
				return err
			},
		},
		{
			ID:            "storage/0002-harness-migrations-ledger",
			Version:       2,
			OwningMission: "storage",
			UpSource:      ledgerInitDDL,
			Up: func(ctx context.Context, tx WriteTx) error {
				for _, stmt := range splitStatements(ledgerInitDDL) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_harness_migrations_action",
					"DROP INDEX IF EXISTS idx_harness_migrations_version",
					"DROP TABLE IF EXISTS harness_migrations",
				} {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
}
