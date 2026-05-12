// migrations.go — on-load migration for hooks.json files.
//
// The migration pass is called from NewRegistry after loading the on-disk
// file. It fills optional fields that were absent in pre-v2 hooks.json
// files (TimeoutMs defaults, etc.) and validates every hook against the
// extended AllEvents list. Hooks with unrecognised events are left in
// place but are marked disabled so they don't fire until the operator
// reviews them.
//
// Design: migrations.go NEVER rewrites the file unless the loaded hooks
// are invalid (e.g. malformed JSON already recovered by load). Filling
// in default values is in-memory only — the file is only written on
// the next Add / Update / Remove call. This preserves the on-disk file
// for human inspection.
package hooks

import "log/slog"

// MigrateHooks validates the loaded hooks slice against the v2 event
// surface and fills any empty required fields with defaults. It is
// called by NewRegistry after loading the on-disk file.
//
// Mutations are in-memory only; the caller must call saveLocked() when
// it wants to persist the changes (the normal write path does this on
// the next mutation).
func MigrateHooks(hooks []Hook, logger *slog.Logger) []Hook {
	if hooks == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	out := make([]Hook, len(hooks))
	copy(out, hooks)
	for i, h := range out {
		changed := false

		// Fill TimeoutMs default (0 == use kind default; no migration needed).

		// Validate event: hooks with unrecognised events are disabled.
		if !isKnownEvent(h.Event) {
			logger.Warn("hooks.migrate.unknown_event",
				"id", h.ID, "event", h.Event,
				"action", "disabling until operator reviews")
			out[i].Enabled = false
			changed = true
		}

		_ = changed // reserved for future in-memory rewrites
	}
	return out
}
