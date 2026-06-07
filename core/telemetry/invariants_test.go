// invariants_test.go — privacy and naming invariants for OTel signals.
//
// These tests assert that span attributes and log records emitted by
// the harness subsystems never carry rendered prompt bodies, raw arg
// values, or user-authored text. Each test that probes a subsystem
// must document the specific field or payload that is gated.
//
// user-slash-commands-01KQ8TD9 WP08.
package telemetry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// openSlashTestDB creates an in-process SQLite DB for slashcmd tests.
func openSlashTestDB(t *testing.T) (storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, dir
}

// TestSlashcmdSpan_NoRenderedBodyInAttrs asserts that the
// "harness.slashcmd.run" span does NOT carry rendered template body or
// arg values in any attribute — only classification metadata.
//
// Privacy invariant (NFR-007): verbose-attrs flag may gate raw values,
// but default attrs must be free of user-authored content.
func TestSlashcmdSpan_NoRenderedBodyInAttrs(t *testing.T) {
	// NOT t.Parallel() — this test mutates the global OTel TracerProvider,
	// which would race with other tests that use otel.GetTracerProvider().

	// Wire an in-memory span recorder as the global TracerProvider.
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(oteltrace.NewNoopTracerProvider()) })

	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	dispatch := slashcmd.NewDispatch(store, nil)

	ctx := context.Background()
	const secretArgValue = "SuperSecretKeyMaterialNeverInSpan"
	const sensitiveBody = "Confidential prompt body — must not appear in OTel span"

	if err := store.SaveUser(ctx, slashcmd.UserCommand{
		Name:        "private",
		Scope:       "global",
		Kind:        "prompt",
		Description: "Private command",
		Body:        sensitiveBody,
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	_, _ = dispatch.TraceRun(ctx, "private",
		map[string]string{"env": secretArgValue},
		slashcmd.SessionContext{SessionID: "s1"},
	)

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}

	found := false
	for _, s := range spans {
		if s.Name() != "harness.slashcmd.run" {
			continue
		}
		found = true

		for _, attr := range s.Attributes() {
			v := attr.Value.AsString()
			if strings.Contains(v, secretArgValue) {
				t.Errorf("span attribute %q = %q contains secret arg value", attr.Key, v)
			}
			if strings.Contains(v, sensitiveBody) {
				t.Errorf("span attribute %q = %q contains rendered body", attr.Key, v)
			}
		}
		break
	}

	if !found {
		t.Error("expected span named \"harness.slashcmd.run\" but not found in recorder")
	}
}
