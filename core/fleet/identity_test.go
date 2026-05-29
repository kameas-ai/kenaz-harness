package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOrgIDToString(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{float64(42), "42"},
		{"42", "42"},
		{int(99), "99"},
		{json.Number("123"), "123"},
	}
	for _, tt := range tests {
		got := orgIDToString(tt.in)
		if got != tt.want {
			t.Errorf("orgIDToString(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEnrollResponseParsing(t *testing.T) {
	// Simulate a fleet enroll response with numeric org_id.
	raw := `{
		"org_id":    101,
		"team_id":   "team-abc",
		"org_name":  "Acme Corp",
		"team_name": "Engineering",
		"role":      "member"
	}`
	var er enrollResponse
	if err := json.Unmarshal([]byte(raw), &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if orgIDToString(er.OrgID) != "101" {
		t.Errorf("OrgID = %q, want 101", orgIDToString(er.OrgID))
	}
	if er.Role != "member" {
		t.Errorf("Role = %q", er.Role)
	}
}

func TestEnrollResponseParsing_MissingFields(t *testing.T) {
	// Fields that early fleet versions don't serialize → empty strings tolerated.
	raw := `{"org_id": 5, "team_id": "t1", "org_name": "O", "team_name": "T", "role": "team_lead"}`
	var er enrollResponse
	if err := json.Unmarshal([]byte(raw), &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Missing tier/email/display_name → empty strings.
	if er.Tier != "" {
		t.Errorf("Tier should be empty, got %q", er.Tier)
	}
	if er.Email != "" {
		t.Errorf("Email should be empty, got %q", er.Email)
	}
	if er.DisplayName != "" {
		t.Errorf("DisplayName should be empty, got %q", er.DisplayName)
	}
}

func TestIdentityCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := Identity{
		UserID:   "user-123",
		OrgID:    "42",
		TeamID:   "team-1",
		OrgName:  "Acme",
		TeamName: "Eng",
		Roles:    []string{"member"},
		Tier:     "pro",
	}

	if err := SaveIdentity(dir, id); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	loaded, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if loaded.UserID != id.UserID {
		t.Errorf("UserID = %q, want %q", loaded.UserID, id.UserID)
	}
	if loaded.OrgID != id.OrgID {
		t.Errorf("OrgID = %q, want %q", loaded.OrgID, id.OrgID)
	}
	if len(loaded.Roles) != 1 || loaded.Roles[0] != "member" {
		t.Errorf("Roles = %v, want [member]", loaded.Roles)
	}
}

func TestIdentityCache_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	id := Identity{UserID: "atomic-test"}

	if err := SaveIdentity(dir, id); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// No tmp file should remain.
	tmp := filepath.Join(dir, "fleet", "identity.json.tmp")
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp file %s still exists after save", tmp)
	}
}

func TestLoadIdentity_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadIdentity(dir)
	if err == nil {
		t.Error("expected error for missing identity file, got nil")
	}
}
