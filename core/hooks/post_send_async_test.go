// post_send_async_test.go pins finding #61 GAP-2: memory.persist's
// embedding call must not sit on the turn's critical path.
//
// blockingEmbedder below is deliberately a channel-based fake, not a
// plain struct field the test reads directly — every cross-goroutine
// signal goes through a channel, so there is nothing for `go test
// -race` to catch and no mutex/snapshot() boilerplate is needed (the
// CLAUDE.md race-safe-fake pattern is about protecting plain fields
// written from a goroutine and read by the test body; channels are
// already safe for that).
package hooks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

// blockingEmbedder simulates a hanging embeddings endpoint: Embed
// blocks on unblock until the test closes it (or the context times
// out/cancels). started fires (best-effort, capacity 1) the moment
// Embed is entered, so the test can deterministically wait for the
// async dispatch to have begun without a wall-clock sleep.
type blockingEmbedder struct {
	unblock chan struct{}
	started chan struct{}
}

func (e *blockingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case e.started <- struct{}{}:
	default:
	}
	select {
	case <-e.unblock:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	vec := make([]float32, 4)
	for i := range vec {
		vec[i] = 0.25
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = vec
	}
	return out, nil
}

// TestRunPostSend_EmbedDoesNotBlockTurn_ThenPersistsToRealDisk is the
// GAP-2 behavioral proof: RunPostSend must return long before a
// deliberately-hanging embedder unblocks (the turn is not held up),
// and the chunk it eventually writes must survive a real on-disk
// chromem store reopened as a FRESH instance (CLAUDE.md blind spot #2
// — an in-process handle proves nothing about the disk round trip).
func TestRunPostSend_EmbedDoesNotBlockTurn_ThenPersistsToRealDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "memory.gob")

	store, err := memory.NewChromemStore(storePath)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	embedder := &blockingEmbedder{
		unblock: make(chan struct{}),
		started: make(chan struct{}, 1),
	}

	reg, err := NewRegistry("")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	builtins := NewBuiltinRegistry()
	RegisterMemoryBuiltins(builtins, MemoryDeps{Store: store, Embedder: embedder})
	// StarterMemoryHooks()[1] is "starter:memory.persist" — the exact
	// hook the settings panel auto-installs when a user opts into
	// memory (InstallStarterMemoryHooks). Using it directly (rather
	// than a hand-rolled Hook{}) means this test exercises the same
	// config shape production installs.
	persistHook := StarterMemoryHooks()[1]
	if persistHook.Builtin != BuiltinMemoryPersist {
		t.Fatalf("StarterMemoryHooks()[1] = %+v, want the memory.persist hook", persistHook)
	}
	if err := reg.Add(persistHook); err != nil {
		t.Fatalf("Add: %v", err)
	}

	runner := NewRunner(Config{Registry: reg, Builtins: builtins})

	ev := PostSendEvent{
		SessionID:     "sess-1",
		UserTurn:      strings.Repeat("u", 100), // > min_user_chars (80)
		AssistantTurn: strings.Repeat("a", 250), // > min_assistant_chars (200)
		FinishReason:  "stop",
	}

	start := time.Now()
	runner.RunPostSend(context.Background(), ev)
	elapsed := time.Since(start)
	// The embedder blocks indefinitely until the test closes unblock.
	// If RunPostSend dispatched synchronously (the pre-fix shape) this
	// call would not return until the test itself unblocks it below —
	// i.e. it would deadlock this exact line, not just run slow. The
	// generous 500ms ceiling only guards against scheduling noise.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("RunPostSend blocked for %v — the embedding call must not sit on the turn's critical path", elapsed)
	}

	select {
	case <-embedder.started:
	case <-time.After(2 * time.Second):
		t.Fatal("embedder.Embed was never invoked on the async pool")
	}

	// The row must not exist yet — Embed hasn't returned, so
	// makeMemoryPersist hasn't reached Store.Add. This is the
	// "no row without its embedding" guarantee: the chunk is written
	// strictly after Embed succeeds, never before.
	if listed, lerr := store.List(context.Background()); lerr == nil && len(listed) != 0 {
		t.Fatalf("chunk persisted before Embed returned: %+v", listed)
	}

	close(embedder.unblock)
	// Shutdown drains the pool (closes the work channel and waits for
	// every in-flight worker), so once it returns the persist builtin
	// has either written the chunk or logged a failure — no race with
	// the read below.
	runner.Shutdown()

	listed, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List after Shutdown: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("post-send persist did not land after the async dispatch completed: %+v", listed)
	}
	if len(listed[0].Embedding) == 0 {
		t.Fatalf("persisted chunk has no embedding: %+v", listed[0])
	}

	// Real-disk round trip (CLAUDE.md blind spot #2): reopen a FRESH
	// store instance at the same path and confirm the chunk survived
	// the close/reopen, not just the live in-process handle.
	store.Close()
	reopened, err := memory.NewChromemStore(storePath)
	if err != nil {
		t.Fatalf("reopen NewChromemStore: %v", err)
	}
	defer reopened.Close()
	listed2, err := reopened.List(context.Background())
	if err != nil {
		t.Fatalf("List on reopened store: %v", err)
	}
	if len(listed2) != 1 {
		t.Fatalf("chunk did not survive close/reopen: %+v", listed2)
	}
	if len(listed2[0].Embedding) == 0 {
		t.Fatalf("reopened chunk has no embedding: %+v", listed2[0])
	}
}
