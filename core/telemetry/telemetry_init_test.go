package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
	"github.com/sigil-tech/kaneaz-harness/core/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// openStorageAt opens (or re-opens) a storage.DB at a stable
// DataDir. Used by Init tests that need to assert instance.id
// persistence across simulated process restarts.
func openStorageAt(t *testing.T, dir string) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open at %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// TestInit_HappyPath constructs the telemetry surface end-to-end,
// confirms the InstanceID round-trips through a second Init, and
// asserts the resource carries the spec-required attributes.
func TestInit_HappyPath(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	db := openStorageAt(t, dataDir)

	t1, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir:      dataDir,
		BuildVersion: "v0.0.1",
		Storage:      db,
	})
	if err != nil {
		t.Fatalf("Init #1: %v", err)
	}
	if t1.InstanceID == "" {
		t.Fatalf("InstanceID empty")
	}
	if t1.TracerProvider == nil || t1.MeterProvider == nil || t1.LoggerProvider == nil {
		t.Fatalf("provider(s) nil")
	}

	// Resource attributes per FR-002.
	wantAttrs := map[string]string{
		"service.name":        telemetry.ServiceName,
		"service.version":     "v0.0.1",
		"service.instance.id": t1.InstanceID,
	}
	gotAttrs := map[string]string{}
	for _, kv := range t1.Resource.Attributes() {
		gotAttrs[string(kv.Key)] = kv.Value.Emit()
	}
	for k, want := range wantAttrs {
		if got := gotAttrs[k]; got != want {
			t.Errorf("resource[%s] = %q, want %q", k, got, want)
		}
	}
	if gotAttrs["host.os"] == "" {
		t.Errorf("resource missing host.os")
	}
	if gotAttrs["process.pid"] == "" {
		t.Errorf("resource missing process.pid")
	}

	if err := t1.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown #1: %v", err)
	}
	// Shutdown is idempotent.
	if err := t1.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown #2: %v", err)
	}
	// Close the underlying DB so we can re-open it under the same
	// DataDir (the lockfile is per-DataDir).
	_ = db.Close(context.Background())

	// Second Init under the same DataDir reuses the persisted
	// instance.id (NFR-003 spirit: fleet operator correlation
	// survives a process restart).
	db2 := openStorageAt(t, dataDir)
	t2, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir: dataDir,
		Storage: db2,
	})
	if err != nil {
		t.Fatalf("Init #2: %v", err)
	}
	if t2.InstanceID != t1.InstanceID {
		t.Errorf("InstanceID changed across Init calls: %q -> %q", t1.InstanceID, t2.InstanceID)
	}
	_ = t2.Shutdown(context.Background())
}

// TestInit_RejectsNilStorage pins the Storage precondition.
func TestInit_RejectsNilStorage(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir: t.TempDir(),
		Storage: nil,
	}); err == nil {
		t.Errorf("expected error for nil Storage")
	}
}

// TestInit_RejectsEmptyDataDir pins the DataDir precondition.
func TestInit_RejectsEmptyDataDir(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	if _, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir: "",
		Storage: db,
	}); err == nil {
		t.Errorf("expected error for empty DataDir")
	}
}

// TestInit_DefaultBuildVersion verifies the empty-build fallback to
// "dev" so a local wails-dev boot still has a stable label.
func TestInit_DefaultBuildVersion(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	db := openStorageAt(t, dataDir)
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir: dataDir,
		Storage: db,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer tel.Shutdown(context.Background())

	var got string
	for _, kv := range tel.Resource.Attributes() {
		if string(kv.Key) == "service.version" && kv.Value.Type() == attribute.STRING {
			got = kv.Value.AsString()
		}
	}
	if got != "dev" {
		t.Errorf("service.version = %q, want dev", got)
	}
}

// TestInit_OTLPRoundTrip: with cfg.OTLPEndpoint set, a span emitted
// reaches BOTH the local SQLite store AND the configured OTLP
// receiver. Pins A2 (otlp wiring) + dual-export semantics.
func TestInit_OTLPRoundTrip(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	dataDir := t.TempDir()
	db := openStorageAt(t, dataDir)

	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir:      dataDir,
		BuildVersion: "v0.0.1",
		Storage:      db,
		OTLPEndpoint: rec.URL(),
		OTLPInsecure: true,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer tel.Shutdown(context.Background())

	tracer := tel.TracerProvider.Tracer("test")
	_, span := tracer.Start(context.Background(), "init.otlp.span")
	span.End()
	if err := tel.TracerProvider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	// Wait for OTLP request to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.Requests()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rec.Requests()) == 0 {
		t.Fatalf("OTLP receiver got no spans")
	}
	got := rec.Requests()[0]
	if got.Path != "/v1/traces" {
		t.Errorf("path = %q, want /v1/traces", got.Path)
	}

	// Drain local exporter via Shutdown so the sqlite write is
	// observable, then confirm the span landed locally too.
	if err := tel.LocalSpan.Shutdown(context.Background()); err != nil {
		t.Fatalf("LocalSpan.Shutdown: %v", err)
	}
	var count int
	row := db.Reader().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM telemetry_spans WHERE name = ?", "init.otlp.span")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Errorf("local span rows = %d, want 1", count)
	}
}

// TestInit_NoOTLP_NoNetwork pins NFR-005: without an OTLP endpoint,
// the SDK pipelines never reach an OTLP receiver. We host a recording
// collector but DO NOT pass its URL to Init, then exercise spans;
// the receiver must remain silent. This is end-to-end evidence that
// no outbound HTTP work happens for local-only telemetry.
func TestInit_NoOTLP_NoNetwork(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)

	dataDir := t.TempDir()
	db := openStorageAt(t, dataDir)
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		DataDir: dataDir,
		Storage: db,
		// OTLPEndpoint deliberately empty.
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer tel.Shutdown(context.Background())

	tracer := tel.TracerProvider.Tracer("test")
	for i := 0; i < 5; i++ {
		_, span := tracer.Start(context.Background(), "no.otlp.span")
		span.End()
	}
	if err := tel.TracerProvider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := len(rec.Requests()); got != 0 {
		t.Fatalf("OTLP receiver got %d requests with no endpoint configured — NFR-005 broken", got)
	}
}
