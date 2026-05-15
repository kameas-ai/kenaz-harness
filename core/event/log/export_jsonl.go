package log

import (
	"encoding/json"
	"fmt"
	"os"
)

// exportRow is the per-line JSONL shape.
type exportRow struct {
	EventID   string `json:"event_id"`
	SessionID string `json:"session_id"`
	EmitterID string `json:"emitter_id"`
	Kind      string `json:"kind"`
	EmittedAt string `json:"emitted_at"`
	Payload   string `json:"payload"`
}

// exportJSONL writes rows as newline-delimited JSON (JSONL / NDJSON).
// Each line is one JSON object. Secret literals in payload bytes are
// replaced with "[REDACTED]".
func exportJSONL(outPath string, rows []Row) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("audit export jsonl: create %q: %w", outPath, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, r := range rows {
		payload := redactSecrets(r.Payload)
		rec := exportRow{
			EventID:   r.EventID,
			SessionID: r.SessionID,
			EmitterID: r.EmitterID,
			Kind:      r.Kind,
			EmittedAt: r.EmittedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
			Payload:   string(payload),
		}
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("audit export jsonl: encode row: %w", err)
		}
	}
	return nil
}
