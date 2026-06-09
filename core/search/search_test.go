package search_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/search"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// storageReaderAdapter bridges storage.Reader to search.Reader.
type storageReaderAdapter struct {
	r storage.Reader
}

func (a *storageReaderAdapter) QueryRow(ctx context.Context, query string, args ...any) search.Row {
	return a.r.QueryRow(ctx, query, args...)
}

func (a *storageReaderAdapter) Query(ctx context.Context, query string, args ...any) (search.Rows, error) {
	return a.r.Query(ctx, query, args...)
}

// storageDBAdapter bridges storage.DB to search.DB.
type storageDBAdapter struct {
	db storage.DB
}

func (a *storageDBAdapter) Reader() search.Reader {
	return &storageReaderAdapter{r: a.db.Reader()}
}

func openTestDB(t *testing.T) (storage.DB, *search.Manager) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	mgr := search.NewManager(&storageDBAdapter{db: db})
	return db, mgr
}

func seedMessages(t *testing.T, db storage.DB, messages []struct {
	sessionID string
	msgID     string
	content   string
}) {
	t.Helper()
	store := session.NewSQLStore(session.NewStorageDB(db))
	ctx := context.Background()
	now := time.Now().UTC()

	// Collect unique session IDs and create them.
	seen := map[string]bool{}
	for _, m := range messages {
		if seen[m.sessionID] {
			continue
		}
		seen[m.sessionID] = true
		rec := session.Record{
			ID:           m.sessionID,
			Name:         "session " + m.sessionID,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastActiveAt: now,
			Position:     0,
			ContextKind:  session.ContextKindSystem,
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create session %q: %v", m.sessionID, err)
		}
	}

	for _, m := range messages {
		if _, err := store.AppendMessage(ctx, session.Message{
			ID:        m.msgID,
			SessionID: m.sessionID,
			Role:      session.RoleUser,
			Content:   m.content,
			CreatedAt: now,
		}); err != nil {
			t.Fatalf("AppendMessage %q: %v", m.msgID, err)
		}
	}
}

// TestSearch_BasicMatch verifies that searching for a distinctive term
// returns the correct message.
func TestSearch_BasicMatch(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	seedMessages(t, db, []struct {
		sessionID string
		msgID     string
		content   string
	}{
		{"s1", "m1", "rust programming language borrow checker"},
		{"s1", "m2", "python is a dynamically typed language"},
	})

	ctx := context.Background()
	results, err := mgr.Search(ctx, search.Query{Text: "rust"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(results), results)
	}
	if results[0].MessageID != "m1" {
		t.Errorf("MessageID = %q, want m1", results[0].MessageID)
	}
	if results[0].SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1", results[0].SessionID)
	}
}

// TestSearch_CrossSession verifies results are returned across multiple
// sessions and are ordered by relevance (BM25 rank).
func TestSearch_CrossSession(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	seedMessages(t, db, []struct {
		sessionID string
		msgID     string
		content   string
	}{
		{"s1", "m1", "rust is great for systems programming"},
		{"s2", "m2", "rust rust rust — repeated term boosts rank"},
		{"s3", "m3", "python is not rust"},
	})

	ctx := context.Background()
	results, err := mgr.Search(ctx, search.Query{Text: "rust", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 3 {
		t.Fatalf("got %d results, want ≥3", len(results))
	}
	// The most-repeated term should rank highest.
	if results[0].MessageID != "m2" {
		t.Errorf("top result = %q, want m2 (highest term frequency)", results[0].MessageID)
	}
}

// TestSearch_SessionFilter confirms the optional SessionID filter
// narrows results to one session.
func TestSearch_SessionFilter(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	seedMessages(t, db, []struct {
		sessionID string
		msgID     string
		content   string
	}{
		{"s1", "m1", "rust memory safety"},
		{"s2", "m2", "rust async runtime"},
	})

	ctx := context.Background()
	results, err := mgr.Search(ctx, search.Query{Text: "rust", SessionID: "s1"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].SessionID != "s1" || results[0].MessageID != "m1" {
		t.Errorf("result = (%q, %q), want (s1, m1)", results[0].SessionID, results[0].MessageID)
	}
}

// TestSearch_LimitOffset confirms LIMIT/OFFSET paging semantics.
func TestSearch_LimitOffset(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	// Seed 5 messages in 5 sessions all containing "gopher".
	msgs := make([]struct {
		sessionID string
		msgID     string
		content   string
	}, 5)
	for i := range msgs {
		id := string(rune('a' + i))
		msgs[i] = struct {
			sessionID string
			msgID     string
			content   string
		}{
			sessionID: "s-paged-" + id,
			msgID:     "m-paged-" + id,
			content:   "gopher " + id,
		}
	}
	seedMessages(t, db, msgs)

	ctx := context.Background()

	// First page: 2 results.
	page1, err := mgr.Search(ctx, search.Query{Text: "gopher", Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("page1 Search: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: got %d, want 2", len(page1))
	}

	// Second page: next 2.
	page2, err := mgr.Search(ctx, search.Query{Text: "gopher", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page2 Search: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: got %d, want 2", len(page2))
	}

	// Third page: last 1.
	page3, err := mgr.Search(ctx, search.Query{Text: "gopher", Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("page3 Search: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3: got %d, want 1", len(page3))
	}

	// No duplicates across pages.
	seen := map[string]bool{}
	for _, r := range append(append(page1, page2...), page3...) {
		if seen[r.MessageID] {
			t.Errorf("duplicate MessageID %q across pages", r.MessageID)
		}
		seen[r.MessageID] = true
	}
}

// TestSearch_HighlightRanges confirms that the snippet string and
// HighlightRanges are consistent — each range's bytes in the snippet
// match the searched term (modulo Porter stemming).
func TestSearch_HighlightRanges(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	seedMessages(t, db, []struct {
		sessionID string
		msgID     string
		content   string
	}{
		{"s1", "m1", "the rust compiler is written in rust"},
	})

	ctx := context.Background()
	results, err := mgr.Search(ctx, search.Query{Text: "rust"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	r := results[0]

	if len(r.Highlights) == 0 {
		t.Fatal("no highlight ranges")
	}
	snip := r.Snippet
	for _, h := range r.Highlights {
		if h.Start < 0 || h.End > len(snip) || h.Start >= h.End {
			t.Errorf("invalid range [%d,%d) for snippet len=%d", h.Start, h.End, len(snip))
			continue
		}
		term := strings.ToLower(snip[h.Start:h.End])
		if term != "rust" {
			t.Errorf("highlighted term = %q, want rust", term)
		}
	}
}

// TestSearch_EmptyQuery confirms ErrEmptyQuery is returned for blank text.
func TestSearch_EmptyQuery(t *testing.T) {
	t.Parallel()
	_, mgr := openTestDB(t)
	ctx := context.Background()

	_, err := mgr.Search(ctx, search.Query{Text: ""})
	if !isErrEmptyQuery(err) {
		t.Errorf("empty query: got %v, want ErrEmptyQuery", err)
	}
	_, err = mgr.Search(ctx, search.Query{Text: "   "})
	if !isErrEmptyQuery(err) {
		t.Errorf("whitespace query: got %v, want ErrEmptyQuery", err)
	}
}

func isErrEmptyQuery(err error) bool {
	return err != nil && err.Error() == search.ErrEmptyQuery.Error()
}

// TestSearch_PorterStemmer confirms the Porter stemmer is active: a
// search for "programming" matches a message containing "programmer".
func TestSearch_PorterStemmer(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	// Use a stemming pair the Porter algorithm actually unifies. "running"
	// and "ran" both stem to "run" (canonical Porter test case); the
	// earlier "programming" vs "programmer" pair stems to different roots
	// ("program" vs "programm") and is not expected to match.
	seedMessages(t, db, []struct {
		sessionID string
		msgID     string
		content   string
	}{
		{"s1", "m1", "she goes running every morning"},
	})

	ctx := context.Background()
	results, err := mgr.Search(ctx, search.Query{Text: "runs"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("Porter stemmer: expected match for 'runs' against 'running', got no results")
	}
}

// TestSearch_ConcurrentReads confirms the Manager is safe for concurrent
// use (-race should catch any issues).
func TestSearch_ConcurrentReads(t *testing.T) {
	t.Parallel()
	db, mgr := openTestDB(t)

	seedMessages(t, db, []struct {
		sessionID string
		msgID     string
		content   string
	}{
		{"s1", "m1", "goroutine concurrency rust"},
		{"s2", "m2", "concurrency in go"},
	})

	ctx := context.Background()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := mgr.Search(ctx, search.Query{Text: "concurrency"}); err != nil {
				t.Errorf("concurrent Search: %v", err)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
