package log

import (
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf/v2"
)

// exportPDF writes rows as a PDF document. The header embeds timestamp,
// filter summary, harness version, git SHA, and chain status per spec §4.4.
// Secret literals in payload bytes are replaced with [REDACTED].
// Uses github.com/jung-kurt/gofpdf/v2 — no CGO required.
func exportPDF(outPath string, rows []Row, opts ExportOptions) error {
	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.SetMargins(10, 10, 10)
	pdf.AddPage()

	// ── Header ────────────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(0, 8, "Kenaz Harness — Audit Log Export", "", 1, "L", false, 0, "")

	pdf.SetFont("Helvetica", "", 8)
	pdf.CellFormat(0, 5,
		fmt.Sprintf("Generated: %s   Version: %s   Commit: %s   Chain: %s",
			time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
			opts.HarnessVersion,
			opts.GitSHA,
			opts.ChainStatus,
		),
		"", 1, "L", false, 0, "")

	if opts.Filter.FreeText != "" || len(opts.Filter.Kinds) > 0 {
		pdf.SetFont("Helvetica", "I", 7)
		filterSummary := fmt.Sprintf("Filter — kinds: %v  free-text: %q",
			opts.Filter.Kinds, opts.Filter.FreeText)
		pdf.CellFormat(0, 4, filterSummary, "", 1, "L", false, 0, "")
	}

	pdf.Ln(3)

	// ── Table header ──────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 7)
	pdf.SetFillColor(220, 220, 220)
	colWidths := []float64{50, 35, 35, 35, 50, 80}
	headers := []string{"Event ID", "Session", "Emitter", "Kind", "Emitted At", "Payload"}
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], 5, h, "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)

	// ── Rows ──────────────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "", 6)
	for _, r := range rows {
		payload := redactSecrets(r.Payload)
		payloadStr := string(payload)
		if len(payloadStr) > 60 {
			payloadStr = payloadStr[:57] + "…"
		}
		cells := []string{
			truncate(r.EventID, 20),
			truncate(r.SessionID, 14),
			truncate(r.EmitterID, 14),
			truncate(r.Kind, 14),
			r.EmittedAt.UTC().Format("2006-01-02T15:04:05Z"),
			payloadStr,
		}
		for i, cell := range cells {
			pdf.CellFormat(colWidths[i], 4, cell, "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}

	if err := pdf.OutputFileAndClose(outPath); err != nil {
		return fmt.Errorf("audit export pdf: write %q: %w", outPath, err)
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
