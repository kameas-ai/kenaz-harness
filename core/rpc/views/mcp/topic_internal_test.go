package mcp

import "testing"

// TestTopicMCPHealthChanged_MatchesComposedEventKind pins the invariant
// impl.go's comments describe by hand: TopicMCPHealthChanged must equal
// "mcp:" + mcpHealthChangedEventKind, the exact composition
// rpc.StreamBroker.Subscribe performs at
// `a.broker.Subscribe(ctx, "mcp", mcpHealthChangedEventKind, ch)`.
//
// This is an internal (package mcp) test — not mcp_test — because
// mcpHealthChangedEventKind is unexported; nothing outside this package
// can assert the pair stays in sync. Written as a same-package test
// rather than a `const _ = ...` compile-time assertion because Go string
// concatenation of two untyped string consts IS a constant expression
// (it would compile either way); the point here is not "does this
// compile" but "do these two literals, which
// scripts/ci/check-broker-topic-consumers.sh's discovery pass requires
// to be independent plain-literal declarations (see TopicMCPHealthChanged's
// doc comment for why concatenation there breaks the gate), still agree
// at runtime" — exactly the kind of drift a hand-duplicated pair (the
// same shape TopicElicitPending/elicitview.TopicElicitPending already
// accepts) can silently develop.
func TestTopicMCPHealthChanged_MatchesComposedEventKind(t *testing.T) {
	want := "mcp:" + mcpHealthChangedEventKind
	if TopicMCPHealthChanged != want {
		t.Fatalf("TopicMCPHealthChanged = %q, want %q (\"mcp:\" + mcpHealthChangedEventKind) — "+
			"the Subscribe call composes the wire topic from \"mcp\" + mcpHealthChangedEventKind; "+
			"if these drift apart, SubscribeHealthChanges registers a different topic than "+
			"TopicMCPHealthChanged advertises", TopicMCPHealthChanged, want)
	}
}
