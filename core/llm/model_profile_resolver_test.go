package llm

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/compaction"
)

func TestModelProfileStore_AbsentProfileIsInert(t *testing.T) {
	s := NewModelProfileStore()
	got, found, err := s.Resolve("claude-sonnet-4-7-20260420", "2026.07.30")
	if err != nil {
		t.Fatalf("unexpected error on empty store: %v", err)
	}
	if found {
		t.Fatalf("expected not-found on empty store, got %+v", got)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero-value ModelProfile on empty store, got %+v", got)
	}
}

func TestModelProfileStore_SpecificOverGlobOverDefault(t *testing.T) {
	s := NewModelProfileStore()
	const version = "2026.07.30"

	// Provider-level default (catch-all).
	if err := s.Load([]ModelProfile{{
		ID:      "*",
		Version: version,
		Context: &ContextPolicy{Aggressiveness: compaction.AggressivenessConservative},
	}}); err != nil {
		t.Fatalf("load default: %v", err)
	}
	// Family glob.
	if err := s.Load([]ModelProfile{{
		ID:      "claude-sonnet-*",
		Version: version,
		Context: &ContextPolicy{Aggressiveness: compaction.AggressivenessBalanced},
	}}); err != nil {
		t.Fatalf("load family glob: %v", err)
	}
	// Exact, most-specific entry.
	if err := s.Load([]ModelProfile{{
		ID:      "claude-sonnet-4-7-20260420",
		Version: version,
		Context: &ContextPolicy{Aggressiveness: compaction.AggressivenessAggressive},
	}}); err != nil {
		t.Fatalf("load exact: %v", err)
	}

	// Exact model matching all three layers must resolve to the exact
	// entry's value, not the family or default.
	exact, found, err := s.Resolve("claude-sonnet-4-7-20260420", version)
	if err != nil || !found {
		t.Fatalf("resolve exact: found=%v err=%v", found, err)
	}
	if exact.Context == nil || exact.Context.Aggressiveness != compaction.AggressivenessAggressive {
		t.Fatalf("expected exact-entry aggressiveness to win, got %+v", exact.Context)
	}

	// A model matching only the family glob (not the exact entry) must
	// resolve to the family's value, not the provider default.
	family, found, err := s.Resolve("claude-sonnet-4-5-20250929", version)
	if err != nil || !found {
		t.Fatalf("resolve family: found=%v err=%v", found, err)
	}
	if family.Context == nil || family.Context.Aggressiveness != compaction.AggressivenessBalanced {
		t.Fatalf("expected family-glob aggressiveness to win over default, got %+v", family.Context)
	}

	// A model matching only the catch-all must fall back to the
	// provider-level default.
	other, found, err := s.Resolve("gpt-4o", version)
	if err != nil || !found {
		t.Fatalf("resolve default: found=%v err=%v", found, err)
	}
	if other.Context == nil || other.Context.Aggressiveness != compaction.AggressivenessConservative {
		t.Fatalf("expected default aggressiveness, got %+v", other.Context)
	}
}

func TestModelProfileStore_UnsetFieldsInheritExplicitOverrides(t *testing.T) {
	s := NewModelProfileStore()
	const version = "1"
	trueVal := true
	falseVal := false

	// Family-level default: dialect "native", parallel tool calls ON,
	// a fallback chain, and a retry ladder.
	if err := s.Load([]ModelProfile{{
		ID:      "claude-*",
		Version: version,
		ToolDialect: &ToolDialectConfig{
			Dialect:                 "native",
			ParallelToolCalls:       &trueVal,
			MaxToolDescriptionBytes: 4000,
		},
		Retry:           &RetryPolicy{MaxAttempts: 5, BaseMS: 100, MaxMS: 9000, Jitter: "full"},
		FallbackChainId: "family-fallback",
	}}); err != nil {
		t.Fatalf("load family: %v", err)
	}

	// Exact entry explicitly turns ParallelToolCalls OFF but says
	// nothing about Dialect, MaxToolDescriptionBytes, Retry, or
	// FallbackChainId — those must inherit from the family layer
	// unchanged. This is the WP08 shape: an explicit `false` must win
	// over an inherited `true`.
	if err := s.Load([]ModelProfile{{
		ID:      "claude-haiku-4-5",
		Version: version,
		ToolDialect: &ToolDialectConfig{
			ParallelToolCalls: &falseVal,
		},
	}}); err != nil {
		t.Fatalf("load exact: %v", err)
	}

	resolved, found, err := s.Resolve("claude-haiku-4-5", version)
	if err != nil || !found {
		t.Fatalf("resolve: found=%v err=%v", found, err)
	}

	if resolved.ToolDialect == nil {
		t.Fatalf("expected merged ToolDialect, got nil")
	}
	if resolved.ToolDialect.ParallelToolCalls == nil || *resolved.ToolDialect.ParallelToolCalls != false {
		t.Fatalf("expected explicit override false to win, got %+v", resolved.ToolDialect.ParallelToolCalls)
	}
	if resolved.ToolDialect.Dialect != "native" {
		t.Fatalf("expected unset Dialect to inherit 'native', got %q", resolved.ToolDialect.Dialect)
	}
	if resolved.ToolDialect.MaxToolDescriptionBytes != 4000 {
		t.Fatalf("expected unset MaxToolDescriptionBytes to inherit 4000, got %d", resolved.ToolDialect.MaxToolDescriptionBytes)
	}
	if resolved.Retry == nil || resolved.Retry.MaxAttempts != 5 {
		t.Fatalf("expected unset Retry to inherit family retry ladder, got %+v", resolved.Retry)
	}
	if resolved.FallbackChainId != "family-fallback" {
		t.Fatalf("expected unset FallbackChainId to inherit 'family-fallback', got %q", resolved.FallbackChainId)
	}
}

func TestModelProfileStore_MalformedOrUnknownVersionFailsCleanly(t *testing.T) {
	s := NewModelProfileStore()
	if err := s.Load([]ModelProfile{{
		ID:              "claude-sonnet-*",
		Version:         "1",
		FallbackChainId: "a",
	}}); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Empty model_id / version: clean errors, not silent fallback.
	if _, _, err := s.Resolve("", "1"); err == nil {
		t.Fatal("expected error for empty model_id")
	}
	if _, _, err := s.Resolve("claude-sonnet-4-7-20260420", ""); err == nil {
		t.Fatal("expected error for empty version")
	}

	// A known family but an unregistered version must NOT silently fall
	// back to a different version's config — it must report not-found,
	// with no error, identical to "no profile configured".
	got, found, err := s.Resolve("claude-sonnet-4-7-20260420", "999-does-not-exist")
	if err != nil {
		t.Fatalf("unknown version must not error, got %v", err)
	}
	if found {
		t.Fatalf("unknown version must not match, got %+v", got)
	}
	if !got.IsZero() {
		t.Fatalf("unknown version must resolve to zero-value profile, got %+v", got)
	}
}

func TestModelProfileStore_LoadRejectsDuplicateAndInvalid(t *testing.T) {
	s := NewModelProfileStore()
	if err := s.Load([]ModelProfile{{ID: "p", Version: "1"}}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if err := s.Load([]ModelProfile{{ID: "p", Version: "1"}}); err == nil {
		t.Fatal("expected collision error on duplicate (id, version)")
	}
	// Different version of the same family ID is NOT a collision.
	if err := s.Load([]ModelProfile{{ID: "p", Version: "2"}}); err != nil {
		t.Fatalf("distinct version must be accepted: %v", err)
	}
	// Malformed entries (missing version) must be rejected by
	// ValidateModelProfile and never installed.
	if err := s.Load([]ModelProfile{{ID: "q"}}); err == nil {
		t.Fatal("expected validation error for missing version")
	}
	if _, found, _ := s.Resolve("q", ""); found {
		t.Fatal("invalid entry must never be installed")
	}
}

func TestModelProfileStore_Evict(t *testing.T) {
	s := NewModelProfileStore()
	if err := s.Load([]ModelProfile{{ID: "p", Version: "1", FallbackChainId: "x"}}); err != nil {
		t.Fatalf("load: %v", err)
	}
	s.Evict("p", "1")
	if _, found, _ := s.Resolve("p", "1"); found {
		t.Fatal("expected entry to be evicted")
	}
	// Re-load after evict must not collide.
	if err := s.Load([]ModelProfile{{ID: "p", Version: "1", FallbackChainId: "y"}}); err != nil {
		t.Fatalf("reload after evict: %v", err)
	}
}
