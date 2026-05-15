package log

import (
	"encoding/csv"
	"fmt"
	"os"
)

// exportCSV writes rows as RFC 4180 CSV.
// Columns: event_id, session_id, emitter_id, kind, emitted_at, payload.
// Secret literals in payload bytes are replaced with [REDACTED].
func exportCSV(outPath string, rows []Row) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("audit export csv: create %q: %w", outPath, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"event_id", "session_id", "emitter_id", "kind", "emitted_at_utc", "payload",
	}); err != nil {
		return fmt.Errorf("audit export csv: header: %w", err)
	}

	for _, r := range rows {
		payload := redactSecrets(r.Payload)
		if err := w.Write([]string{
			r.EventID,
			r.SessionID,
			r.EmitterID,
			r.Kind,
			r.EmittedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
			string(payload),
		}); err != nil {
			return fmt.Errorf("audit export csv: write row: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}
