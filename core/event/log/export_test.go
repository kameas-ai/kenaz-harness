package log

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildExportRows inserts n simple rows into a MemoryBackend and returns
// the backend. Uses a per-test prefix to avoid event_id collisions.
func buildExportRows(t *testing.T, n int, prefix string) *MemoryBackend {
	t.Helper()
	b := NewMemoryBackend()
	ctx := context.Background()
	base := time.UnixMilli(1700000000000)
	for i := 0; i < n; i++ {
		r := Row{
			EventID:   fmt.Sprintf("%s%020d", prefix, i),
			SessionID: fmt.Sprintf("%s-sess", prefix),
			EmitterID: "test/export",
			Kind:      "test.export",
			EmittedAt: base.Add(time.Duration(i) * time.Second),
			Payload:   []byte(fmt.Sprintf(`{"seq":%d}`, i)),
			PrevHash:  [32]byte{},
		}
		_ = b.AppendRow(ctx, r, [32]byte{})
	}
	return b
}

func TestExportCSV_RoundTrip(t *testing.T) {
	b := buildExportRows(t, 5, "01EXC")
	dir := t.TempDir()
	opts := ExportOptions{DataDir: dir, Format: ExportFormatCSV}
	path, err := Export(context.Background(), b, opts)
	if err != nil {
		t.Fatalf("Export CSV: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Verify header row and 5 data rows.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 6 { // 1 header + 5 data
		t.Errorf("CSV: expected 6 lines (1 header + 5 data), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "event_id") {
		t.Errorf("CSV header missing event_id: %q", lines[0])
	}
}

func TestExportJSONL_RoundTrip(t *testing.T) {
	b := buildExportRows(t, 5, "01EXJ")
	dir := t.TempDir()
	opts := ExportOptions{DataDir: dir, Format: ExportFormatJSONL}
	path, err := Export(context.Background(), b, opts)
	if err != nil {
		t.Fatalf("Export JSONL: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Errorf("JSONL: expected 5 lines, got %d", len(lines))
	}
	// Verify each line is valid JSON with expected fields.
	for _, line := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("JSONL line not valid JSON: %q: %v", line, err)
		}
		if _, ok := m["event_id"]; !ok {
			t.Errorf("JSONL line missing event_id: %q", line)
		}
	}
}

func TestExportPDF_FileSize(t *testing.T) {
	b := buildExportRows(t, 10, "01EXP")
	dir := t.TempDir()
	opts := ExportOptions{
		DataDir:        dir,
		Format:         ExportFormatPDF,
		HarnessVersion: "v0.16.0-test",
		GitSHA:         "deadbeef",
		ChainStatus:    "verified",
	}
	path, err := Export(context.Background(), b, opts)
	if err != nil {
		t.Fatalf("Export PDF: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// PDF must be at least 1 KB and no more than 10 MB (budget guard).
	if info.Size() < 1024 {
		t.Errorf("PDF too small: %d bytes", info.Size())
	}
	if info.Size() > 10*1024*1024 {
		t.Errorf("PDF too large: %d bytes", info.Size())
	}
	// Verify PDF magic bytes: %PDF
	data := make([]byte, 4)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open PDF: %v", err)
	}
	defer f.Close()
	if _, err := f.Read(data); err != nil {
		t.Fatalf("Read PDF header: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Errorf("PDF magic mismatch: %q", data)
	}
}

func TestExport_Redaction(t *testing.T) {
	// Plant a secret literal (Anthropic key prefix) in the payload.
	b := NewMemoryBackend()
	ctx := context.Background()
	secretPayload := []byte(`{"key":"sk-ant-api03-xxxx"}`)
	r := Row{
		EventID:   "01EXSEC0000000000000000001",
		SessionID: "sec-sess",
		EmitterID: "test/export",
		Kind:      "test.secret",
		EmittedAt: time.UnixMilli(1700000000000),
		Payload:   secretPayload,
		PrevHash:  [32]byte{},
	}
	_ = b.AppendRow(ctx, r, [32]byte{})

	dir := t.TempDir()
	path, err := Export(ctx, b, ExportOptions{DataDir: dir, Format: ExportFormatJSONL})
	if err != nil {
		t.Fatalf("Export JSONL with secret: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Contains(data, []byte("sk-ant-")) {
		t.Error("redaction failed: secret literal found in JSONL output")
	}
}

func TestExport_UnknownFormat(t *testing.T) {
	b := NewMemoryBackend()
	_, err := Export(context.Background(), b, ExportOptions{
		DataDir: t.TempDir(),
		Format:  "xml",
	})
	if err == nil {
		t.Error("expected error for unknown format xml")
	}
}

func TestExportCSV_FilePath(t *testing.T) {
	b := buildExportRows(t, 1, "01EXF")
	dir := t.TempDir()
	path, err := Export(context.Background(), b, ExportOptions{DataDir: dir, Format: ExportFormatCSV})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if filepath.Dir(path) != filepath.Join(dir, "audit-exports") {
		t.Errorf("file not in audit-exports: %q", path)
	}
	if filepath.Ext(path) != ".csv" {
		t.Errorf("expected .csv extension, got %q", filepath.Ext(path))
	}
}
