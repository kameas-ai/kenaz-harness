package compliance

import (
	"context"
	"io"
	"net/http"
	"testing"

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

// ── test helpers ──────────────────────────────────────────────────────────────

// testNoopPoster is a fleet.AuditHTTPPoster that accepts all posts silently.
// It lets the fleet.AuditArchiver start without a live fleet endpoint.
type testNoopPoster struct{}

func (p *testNoopPoster) Post(_ context.Context, _ string, _ string, _ io.Reader) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

