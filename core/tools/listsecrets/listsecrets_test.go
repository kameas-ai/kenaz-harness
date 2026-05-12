package listsecrets_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/credstore/refs"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/tools/listsecrets"
)

// fakeIndex is an in-memory stub implementing the Index interface.
type fakeIndex struct {
	entries []secrets.ExposedEntry
}

func (f *fakeIndex) List(_ context.Context, scope secrets.Scope) []secrets.ExposedEntry {
	if scope == "" {
		return f.entries
	}
	var out []secrets.ExposedEntry
	for _, e := range f.entries {
		if e.Scope == scope {
			out = append(out, e)
		}
	}
	return out
}

func makeEntry(locator, desc string, scope secrets.Scope, kind secrets.KindHint) secrets.ExposedEntry {
	return secrets.ExposedEntry{
		Locator:     locator,
		Description: desc,
		Scope:       scope,
		KindHint:    kind,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func callTool(t *testing.T, tool *listsecrets.Tool, argsJSON string) []listsecrets.ListEntry {
	t.Helper()
	raw, err := tool.Call(context.Background(), json.RawMessage(argsJSON))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out []listsecrets.ListEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v (raw=%s)", err, raw)
	}
	return out
}

// TestListSecrets_Empty verifies empty index returns empty list.
func TestListSecrets_Empty(t *testing.T) {
	tool := listsecrets.New(listsecrets.Options{
		Index: &fakeIndex{},
	})
	entries := callTool(t, tool, `{}`)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestListSecrets_OneEntry verifies a single entry is returned correctly.
func TestListSecrets_OneEntry(t *testing.T) {
	idx := &fakeIndex{entries: []secrets.ExposedEntry{
		makeEntry("user:example-api-token", "Example Inc. API", secrets.ScopeSession, secrets.KindHintBearer),
	}}
	tool := listsecrets.New(listsecrets.Options{Index: idx})
	entries := callTool(t, tool, `{}`)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Ref != "@secret:user:example-api-token" {
		t.Errorf("Ref = %q", entries[0].Ref)
	}
	if entries[0].Description != "Example Inc. API" {
		t.Errorf("Description = %q", entries[0].Description)
	}
	if entries[0].Kind != "bearer" {
		t.Errorf("Kind = %q", entries[0].Kind)
	}
	if entries[0].ScopeHint != "session" {
		t.Errorf("ScopeHint = %q", entries[0].ScopeHint)
	}
}

// TestListSecrets_NoValueField asserts the response never contains a
// "value" JSON key (FR-005a).
func TestListSecrets_NoValueField(t *testing.T) {
	idx := &fakeIndex{entries: []secrets.ExposedEntry{
		makeEntry("user:foo", "My Token", secrets.ScopeSession, secrets.KindHintRaw),
	}}
	tool := listsecrets.New(listsecrets.Options{Index: idx})
	raw, err := tool.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if strings.Contains(string(raw), `"value"`) {
		t.Errorf("response contains 'value' key: %s", raw)
	}
}

// TestListSecrets_Filter verifies substring filtering.
func TestListSecrets_Filter(t *testing.T) {
	idx := &fakeIndex{entries: []secrets.ExposedEntry{
		makeEntry("user:anthropic-key", "Anthropic API", secrets.ScopeSession, secrets.KindHintBearer),
		makeEntry("user:github-pat", "GitHub PAT", secrets.ScopeProject, secrets.KindHintBearer),
	}}
	tool := listsecrets.New(listsecrets.Options{Index: idx})

	// Filter by locator substring.
	entries := callTool(t, tool, `{"filter":"anthropic"}`)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching 'anthropic', got %d", len(entries))
	}

	// Filter by description substring.
	entries = callTool(t, tool, `{"filter":"GitHub"}`)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching 'GitHub', got %d", len(entries))
	}
}

// TestListSecrets_BudgetRemaining verifies budget field is included when
// a Budget is wired.
func TestListSecrets_BudgetRemaining(t *testing.T) {
	idx := &fakeIndex{entries: []secrets.ExposedEntry{
		makeEntry("user:tok", "Token", secrets.ScopeSession, secrets.KindHintBearer),
	}}
	b := refs.NewBudget(50)
	tool := listsecrets.New(listsecrets.Options{Index: idx, Budget: b})
	entries := callTool(t, tool, `{}`)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].BudgetRemaining == nil {
		t.Error("expected BudgetRemaining to be set when budget is wired")
	}
	if *entries[0].BudgetRemaining != 50 {
		t.Errorf("BudgetRemaining = %d; want 50", *entries[0].BudgetRemaining)
	}
}

// TestListSecrets_NoBudgetField verifies budget field absent when not wired.
func TestListSecrets_NoBudgetField(t *testing.T) {
	idx := &fakeIndex{entries: []secrets.ExposedEntry{
		makeEntry("user:tok", "Token", secrets.ScopeSession, secrets.KindHintBearer),
	}}
	tool := listsecrets.New(listsecrets.Options{Index: idx})
	entries := callTool(t, tool, `{}`)
	if len(entries) != 1 {
		t.Fatalf("expected 1, got %d", len(entries))
	}
	if entries[0].BudgetRemaining != nil {
		t.Errorf("expected nil BudgetRemaining when budget not wired")
	}
}
