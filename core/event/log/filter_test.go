package log

import (
	"context"
	"fmt"
	"testing"
)

// appendRow is a test helper that appends a row to a MemoryBackend
// while maintaining the chain head for the given session.
// It uses unique sessions per test (indexed by a prefix) to avoid
// head-mismatch conflicts.
func appendFiltered(t *testing.T, b *MemoryBackend, id, kind string, payload []byte) {
	t.Helper()
	ctx := context.Background()
	r := mkRow(id, id+"sess", kind, payload, [32]byte{})
	if err := b.AppendRow(ctx, r, [32]byte{}); err != nil {
		t.Fatalf("AppendRow %s: %v", id, err)
	}
}

func TestFilterQuery_KindFilter(t *testing.T) {
	b := NewMemoryBackend()
	appendFiltered(t, b, "01EV00000000000000000000001", "llm.req", []byte(`{}`))
	appendFiltered(t, b, "01EV00000000000000000000002", "mcp.call", []byte(`{}`))
	appendFiltered(t, b, "01EV00000000000000000000003", "llm.req", []byte(`{}`))
	appendFiltered(t, b, "01EV00000000000000000000004", "policy.gate", []byte(`{}`))

	q := FilterQuery{Kinds: []string{"llm.req"}}
	rows, err := q.ApplyToMemoryBackend(context.Background(), b)
	if err != nil {
		t.Fatalf("ApplyToMemoryBackend: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Kind != "llm.req" {
			t.Errorf("expected kind llm.req, got %s", r.Kind)
		}
	}
}

func TestFilterQuery_FreeText(t *testing.T) {
	b := NewMemoryBackend()
	payloads := []struct {
		id      string
		payload string
	}{
		{"01FT00000000000000000000001", `{"model":"claude-3"}`},
		{"01FT00000000000000000000002", `{"model":"gpt-4"}`},
		{"01FT00000000000000000000003", `{"model":"claude-opus"}`},
	}
	for _, p := range payloads {
		appendFiltered(t, b, p.id, "llm.req", []byte(p.payload))
	}

	q := FilterQuery{FreeText: "claude"}
	rows, err := q.ApplyToMemoryBackend(context.Background(), b)
	if err != nil {
		t.Fatalf("ApplyToMemoryBackend: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 claude rows, got %d", len(rows))
	}
}

func TestFilterQuery_VerboseFilter(t *testing.T) {
	b := NewMemoryBackend()
	kinds := []struct {
		id   string
		kind string
	}{
		{"01VB00000000000000000000001", "verbose.token.stream"},
		{"01VB00000000000000000000002", "llm.response"},
		{"01VB00000000000000000000003", "verbose.chunk"},
	}
	for _, k := range kinds {
		appendFiltered(t, b, k.id, k.kind, []byte(`{}`))
	}

	// Verbose=false should hide verbose. kinds.
	q := FilterQuery{Verbose: false}
	rows, err := q.ApplyToMemoryBackend(context.Background(), b)
	if err != nil {
		t.Fatalf("ApplyToMemoryBackend: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 non-verbose row, got %d", len(rows))
	}
	if len(rows) > 0 && rows[0].Kind != "llm.response" {
		t.Errorf("expected llm.response, got %s", rows[0].Kind)
	}

	// Verbose=true should include all.
	qV := FilterQuery{Verbose: true}
	allRows, err := qV.ApplyToMemoryBackend(context.Background(), b)
	if err != nil {
		t.Fatalf("ApplyToMemoryBackend verbose: %v", err)
	}
	if len(allRows) != 3 {
		t.Errorf("expected 3 rows with verbose=true, got %d", len(allRows))
	}
}

func TestFilterQuery_Limit(t *testing.T) {
	b := NewMemoryBackend()
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("01LM%020d", i)
		appendFiltered(t, b, id, "llm.req", []byte(`{}`))
	}

	q := FilterQuery{Limit: 5}
	rows, err := q.ApplyToMemoryBackend(context.Background(), b)
	if err != nil {
		t.Fatalf("ApplyToMemoryBackend: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("expected 5 rows with limit=5, got %d", len(rows))
	}
}
