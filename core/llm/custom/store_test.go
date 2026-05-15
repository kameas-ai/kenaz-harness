package custom

import (
	"testing"
	"time"
)

// TestCapabilityMatrixIsStale verifies TTL logic.
func TestCapabilityMatrixIsStale(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		matrix *CapabilityMatrix
		want   bool
	}{
		{"nil matrix is stale", nil, true},
		{"fresh matrix", &CapabilityMatrix{ProbedAt: now.Unix()}, false},
		{"just at TTL", &CapabilityMatrix{ProbedAt: now.Add(-CapabilityTTL).Unix()}, true},
		{"old matrix", &CapabilityMatrix{ProbedAt: now.Add(-8 * 24 * time.Hour).Unix()}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.matrix.IsStale(now)
			if got != tc.want {
				t.Errorf("IsStale() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNewCapabilityMatrix verifies default field values.
func TestNewCapabilityMatrix(t *testing.T) {
	m := NewCapabilityMatrix("http://localhost:8000/v1")
	if m.Endpoint != "http://localhost:8000/v1" {
		t.Errorf("Endpoint = %q, want http://localhost:8000/v1", m.Endpoint)
	}
	if m.Streaming != CapabilityValueUnknown {
		t.Errorf("Streaming = %q, want unknown", m.Streaming)
	}
	if m.ToolCalling != CapabilityValueUnknown {
		t.Errorf("ToolCalling = %q, want unknown", m.ToolCalling)
	}
	if m.StreamingUsage != CapabilityValueUnknown {
		t.Errorf("StreamingUsage = %q, want unknown", m.StreamingUsage)
	}
}

// TestSplitSQL verifies the SQL splitter handles multi-statement DDL.
func TestSplitSQL(t *testing.T) {
	src := `CREATE TABLE IF NOT EXISTS foo (id TEXT PRIMARY KEY);
	CREATE INDEX IF NOT EXISTS idx_foo ON foo(id);`
	parts := splitSQL(src)
	if len(parts) != 2 {
		t.Errorf("splitSQL: got %d parts, want 2", len(parts))
	}
}

// TestCapabilityValueConstants verifies tri-state values.
func TestCapabilityValueConstants(t *testing.T) {
	vals := map[CapabilityValue]bool{
		CapabilityValueTrue:    true,
		CapabilityValueFalse:   true,
		CapabilityValueUnknown: true,
	}
	if len(vals) != 3 {
		t.Errorf("expected 3 distinct CapabilityValue constants, got %d", len(vals))
	}
}

// TestCapabilityTTL verifies TTL constant is 7 days.
func TestCapabilityTTL(t *testing.T) {
	want := 7 * 24 * time.Hour
	if CapabilityTTL != want {
		t.Errorf("CapabilityTTL = %v, want %v", CapabilityTTL, want)
	}
}
