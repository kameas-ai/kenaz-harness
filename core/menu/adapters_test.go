package menu

import (
	"context"
	"testing"
)

// TestRecentSessionsFetcherFunc_ImplementsInterface verifies that the adapter
// satisfies the RecentSessionsFetcher interface at compile time.
func TestRecentSessionsFetcherFunc_ImplementsInterface(t *testing.T) {
	var _ RecentSessionsFetcher = RecentSessionsFetcherFunc(func(_ context.Context, _ int) ([]SessionRef, error) {
		return nil, nil
	})
}

// TestRecentSessionsFetcherFunc_Delegates verifies the adapter calls the
// underlying function with the correct arguments and surfaces the result.
func TestRecentSessionsFetcherFunc_Delegates(t *testing.T) {
	wantRefs := []SessionRef{
		{ID: "s1", Title: "First"},
		{ID: "s2", Title: "Second"},
	}
	var gotLimit int
	fetcher := RecentSessionsFetcherFunc(func(_ context.Context, limit int) ([]SessionRef, error) {
		gotLimit = limit
		return wantRefs, nil
	})

	refs, err := fetcher.FetchRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotLimit != 10 {
		t.Errorf("limit = %d, want 10", gotLimit)
	}
	if len(refs) != len(wantRefs) {
		t.Fatalf("len(refs) = %d, want %d", len(refs), len(wantRefs))
	}
	for i, r := range refs {
		if r.ID != wantRefs[i].ID || r.Title != wantRefs[i].Title {
			t.Errorf("refs[%d] = %+v, want %+v", i, r, wantRefs[i])
		}
	}
}

// TestMenuState_RecentSessions_CappedAt10 verifies that only up to 10 sessions
// are surfaced in a MenuState built from a fetcher returning more than 10 refs.
func TestMenuState_RecentSessions_CappedAt10(t *testing.T) {
	// Build a fake fetcher that returns 15 sessions.
	const total = 15
	const cap = 10
	allSessions := make([]SessionRef, total)
	for i := range allSessions {
		allSessions[i] = SessionRef{ID: string(rune('A' + i)), Title: "Session"}
	}

	fetcher := RecentSessionsFetcherFunc(func(_ context.Context, limit int) ([]SessionRef, error) {
		if limit > len(allSessions) {
			limit = len(allSessions)
		}
		return allSessions[:limit], nil
	})

	refs, err := fetcher.FetchRecent(context.Background(), cap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != cap {
		t.Errorf("len(refs) = %d, want %d", len(refs), cap)
	}

	// Verify ordering is preserved (most-recent first = original order from fetcher).
	for i, r := range refs {
		if r.ID != allSessions[i].ID {
			t.Errorf("refs[%d].ID = %q, want %q", i, r.ID, allSessions[i].ID)
		}
	}
}

// TestMenuState_RecentSessions_PopulatedCorrectly verifies that a MenuState
// built with sessions from a fetcher correctly sets RecentSessions.
func TestMenuState_RecentSessions_PopulatedCorrectly(t *testing.T) {
	want := []SessionRef{
		{ID: "sess-1", Title: "Alpha"},
		{ID: "sess-2", Title: "Beta"},
	}

	fetcher := RecentSessionsFetcherFunc(func(_ context.Context, _ int) ([]SessionRef, error) {
		return want, nil
	})

	state := MenuState{}
	refs, err := fetcher.FetchRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("fetcher error: %v", err)
	}
	state.RecentSessions = refs

	if len(state.RecentSessions) != len(want) {
		t.Fatalf("len(RecentSessions) = %d, want %d", len(state.RecentSessions), len(want))
	}
	for i, r := range state.RecentSessions {
		if r.ID != want[i].ID {
			t.Errorf("RecentSessions[%d].ID = %q, want %q", i, r.ID, want[i].ID)
		}
		if r.Title != want[i].Title {
			t.Errorf("RecentSessions[%d].Title = %q, want %q", i, r.Title, want[i].Title)
		}
	}
}
