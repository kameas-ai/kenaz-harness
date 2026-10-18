package usage_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/usage"
)

// tool-context-budget-01TCBUD01 WP05: the per-message cached count the
// usage pipeline writes reaches the session's message read path through
// real sqlite (it feeds the per-message token chip); a row with no usage
// reads back nil.
func TestCachedTokens_RoundTripToSessionMessages(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "sess-c")
	seedMessage(t, db, "sess-c", "msg-user", "user", 1)
	seedMessage(t, db, "sess-c", "msg-asst", "assistant", 2)

	ctx := context.Background()
	if err := usage.New(db).Add(ctx, usage.UsageTurn{
		SessionID: "sess-c", MessageID: "msg-asst",
		PromptTokens: 10000, CompletionTokens: 12,
		CachedTokens: 9000, CacheWriteTokens: 700,
		CostSource: "derived",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	msgs, err := session.NewSQLStore(session.NewStorageDB(db)).ListMessages(ctx, "sess-c")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if msgs[0].CachedTokens != nil {
		t.Errorf("user row CachedTokens = %v, want nil", *msgs[0].CachedTokens)
	}
	if msgs[1].CachedTokens == nil || *msgs[1].CachedTokens != 9000 {
		t.Errorf("assistant row CachedTokens = %v, want 9000", msgs[1].CachedTokens)
	}
	if msgs[1].PromptTokens == nil || *msgs[1].PromptTokens != 10000 {
		t.Errorf("assistant row PromptTokens = %v, want 10000", msgs[1].PromptTokens)
	}
}
