package compliance

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestComplianceAPI_StatusNotEnabled(t *testing.T) {
	api := NewAPI(nil, nil, func() bool { return false })
	status, err := api.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Enabled {
		t.Error("want Enabled=false when cap not active")
	}
	if status.RetentionDays != fleet.DefaultAuditRetentionDays {
		t.Errorf("want default retention %d, got %d",
			fleet.DefaultAuditRetentionDays, status.RetentionDays)
	}
}

func TestComplianceAPI_ArchiveNow_NotEnabled(t *testing.T) {
	api := NewAPI(nil, nil, func() bool { return false })
	if err := api.ArchiveNow(context.Background()); err == nil {
		t.Error("ArchiveNow with cap disabled: want error")
	}
}

func TestComplianceAPI_SetRetention_NotEnabled(t *testing.T) {
	api := NewAPI(nil, nil, func() bool { return false })
	if err := api.SetRetention(context.Background(), 365); err == nil {
		t.Error("SetRetention with cap disabled: want error")
	}
}

func TestComplianceAPI_SetRetention_UpdatesSweeper(t *testing.T) {
	sweeper := fleet.NewAuditRetentionSweeper(fleet.AuditRetentionConfig{RetentionDays: 90})
	api := NewAPI(nil, sweeper, func() bool { return true })

	if err := api.SetRetention(context.Background(), 365); err != nil {
		t.Fatalf("SetRetention: %v", err)
	}
	status, _ := api.Status(context.Background())
	if status.RetentionDays != 365 {
		t.Errorf("want 365, got %d", status.RetentionDays)
	}
}

func TestComplianceAPI_Status_RetentionFromSweeper(t *testing.T) {
	sweeper := fleet.NewAuditRetentionSweeper(fleet.AuditRetentionConfig{RetentionDays: 60})
	api := NewAPI(nil, sweeper, func() bool { return true })

	status, err := api.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.RetentionDays != 60 {
		t.Errorf("want 60, got %d", status.RetentionDays)
	}
}

// TestComplianceAPI_Status_ArchiverRunning_RealState verifies that the
// compliance panel reports ArchiverRunning = true only when the archiver is
// actually running, and false after it is stopped.
//
// This is the acceptance test for review blocker 2: status.ArchiverRunning
// must use archiver.IsRunning() not a static "archiver != nil" check.
// (fleet-audit-archival-01NDFSEX13 WP05)
func TestComplianceAPI_Status_ArchiverRunning_RealState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// noopPoster satisfies fleet.AuditHTTPPoster so the archiver loop starts
	// without a live fleet endpoint.
	archiver := fleet.NewAuditArchiver(fleet.AuditArchiverConfig{
		Poster: &testNoopPoster{},
		Tail:   &contextaudit.MemoryTailReader{},
		// CapCheck nil → archiver runs unconditionally per AuditArchiverConfig docs.
	})
	sweeper := fleet.NewAuditRetentionSweeper(fleet.AuditRetentionConfig{})
	api := NewAPI(archiver, sweeper, func() bool { return true })

	// Before Start: Enabled=true (capCheck), ArchiverRunning=false.
	status, err := api.Status(context.Background())
	if err != nil {
		t.Fatalf("Status before Start: %v", err)
	}
	if !status.Enabled {
		t.Error("want Enabled=true (capCheck always returns true)")
	}
	if status.ArchiverRunning {
		t.Error("want ArchiverRunning=false before archiver.Start()")
	}

	// Start archiver; IsRunning() must flip to true.
	archiver.Start(ctx)
	if !archiver.IsRunning() {
		t.Fatal("archiver.IsRunning() should be true immediately after Start()")
	}

	// Compliance panel must reflect the live running state.
	status, err = api.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after Start: %v", err)
	}
	if !status.ArchiverRunning {
		t.Error("want ArchiverRunning=true after archiver.Start()")
	}

	// Stop the archiver; IsRunning() must return to false.
	archiver.Stop()
	if archiver.IsRunning() {
		t.Error("archiver.IsRunning() should be false after Stop()")
	}

	status, err = api.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after Stop: %v", err)
	}
	if status.ArchiverRunning {
		t.Error("want ArchiverRunning=false after archiver.Stop()")
	}
}

// ── AC-008 (fleet-enforcement-truth-01PMZ505 WP06) ──────────────────────────
//
// "The operator can clear a chain break": a real archiver with a planted
// chain break, driven entirely through the RPC-facing ComplianceAPI, not
// the underlying *fleet.AuditArchiver directly (that path is already
// pinned by core/fleet's own TestAuditArchival_ChainBreak_HaltsAndRecovers).
// This proves the RPC surface added in WP06 — SkipToID on ComplianceAPI,
// gated identically to its siblings — actually reaches the same recovery
// action, not merely that the underlying primitive works.

func TestComplianceAPI_SkipToID_ClearsChainBreakAndEmits(t *testing.T) {
	dir := t.TempDir()
	emitter := &testCaptureEmitter{}

	e1 := testTailEvent("SKIP001", [32]byte{})
	e2 := testTailEvent("SKIP002", [32]byte{0xFF}) // wrong prev_hash → break
	tr := &contextaudit.MemoryTailReader{}
	tr.Append(e1)
	tr.Append(e2)

	archiver := fleet.NewAuditArchiver(fleet.AuditArchiverConfig{
		Poster:        &testNoopPoster{},
		DataDir:       dir,
		Tail:          tr,
		Signer:        testStaticSigner{},
		Verifier:      &fleet.BatchChainVerifier{},
		Emitter:       emitter,
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
	})
	api := NewAPI(archiver, nil, func() bool { return true })

	archiver.Start(context.Background())
	t.Cleanup(archiver.Stop)

	// Drive the break through the same path the panel's "Archive now"
	// button would: ArchiveNow's own flush detects and halts.
	if err := archiver.ArchiveNow(context.Background()); err == nil {
		t.Fatal("ArchiveNow with a planted chain break: want error")
	}
	if !archiver.ChainBreakDetected() {
		t.Fatal("want chain break detected before SkipToID")
	}

	// The RPC surface, not the underlying archiver.
	if err := api.SkipToID(context.Background(), "SKIP002"); err != nil {
		t.Fatalf("Compliance API.SkipToID: %v", err)
	}

	if archiver.ChainBreakDetected() {
		t.Error("chain break must be cleared after Compliance API.SkipToID")
	}
	if err := archiver.ArchiveNow(context.Background()); err != nil {
		t.Errorf("ArchiveNow after SkipToID: %v (want success — the halt must be cleared)", err)
	}

	var sawSkip bool
	for _, e := range emitter.snapshot() {
		if e.Kind == contextaudit.KindFleetAuditChainSkipped {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Error("want fleet.audit_chain_skipped emitted from production code, not a test helper")
	}
}

func TestComplianceAPI_SkipToID_NotEnabled(t *testing.T) {
	api := NewAPI(nil, nil, func() bool { return false })
	if err := api.SkipToID(context.Background(), "whatever"); err == nil {
		t.Error("SkipToID with cap disabled: want error")
	}
}

func TestComplianceAPI_SkipToID_NilArchiver(t *testing.T) {
	api := NewAPI(nil, nil, func() bool { return true })
	if err := api.SkipToID(context.Background(), "whatever"); err == nil {
		t.Error("SkipToID with nil archiver: want error, not a panic or a silent success")
	}
}

// ── test helpers ──────────────────────────────────────────────────────────────

// testNoopPoster is a fleet.AuditHTTPPoster that accepts all posts silently.
// It lets the fleet.AuditArchiver start without a live fleet endpoint.
type testNoopPoster struct{}

func (p *testNoopPoster) Post(_ context.Context, _ string, _ string, _ io.Reader) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// testStaticSigner is a fleet.Signer stub for AC-008.
type testStaticSigner struct{}

func (testStaticSigner) Sign(_ []byte) (string, string, error) {
	return "test-sig", "sha256:testfp", nil
}

// testCaptureEmitter is a race-safe contextaudit.Emitter for AC-008.
type testCaptureEmitter struct {
	mu      sync.Mutex
	emitted []contextaudit.Event
}

func (e *testCaptureEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.emitted = append(e.emitted, ev)
	return nil
}

func (e *testCaptureEmitter) snapshot() []contextaudit.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contextaudit.Event, len(e.emitted))
	copy(out, e.emitted)
	return out
}

// testTailEvent builds a minimal contextaudit.TailEvent with a
// deliberately mismatched hash chain when prevHash does not match the
// preceding event's actual hash — mirrors core/fleet/audit_archive_test.go's
// makeTailEvent, duplicated here rather than exported across packages
// for a two-test fixture.
func testTailEvent(id string, prevHash [32]byte) contextaudit.TailEvent {
	payload := []byte(`{"test":true}`)
	ph := sha256.Sum256([]byte(id + string(payload)))
	return contextaudit.TailEvent{
		ID:          id,
		Kind:        "test.event",
		EmittedAt:   time.Now(),
		Payload:     payload,
		PayloadHash: ph,
		PrevHash:    prevHash,
	}
}
