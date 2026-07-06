package compliance

import (
	"context"
	"testing"

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
