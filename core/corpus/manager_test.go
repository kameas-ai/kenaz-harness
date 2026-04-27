package corpus_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/corpus"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// stubEmbedder returns deterministic 32-dim vectors hashed from each
// input. Used to keep ingest + retrieve tests offline + reproducible.
type stubEmbedder struct {
	dims int
	// fail, when non-nil, replaces Embed's success path. Used to test
	// the atomic-rollback path: if SQL committed first we'd see partial
	// rows; with the order Embed -> WriteTx the SQL never sees a chunk.
	fail func() error
}

func newStubEmbedder() *stubEmbedder { return &stubEmbedder{dims: 32} }

func (s *stubEmbedder) Dimensions() int { return s.dims }

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if s.fail != nil {
		if err := s.fail(); err != nil {
			return nil, err
		}
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = deterministicVector(t, s.dims)
	}
	return out, nil
}

// deterministicVector hashes text to a `dims`-wide float32 vector. The
// vector is filled by re-hashing the text with a salt counter so each
// dimension carries an independent signal — this gives identical-text
// pairs a similarity of exactly 1.0 while keeping unrelated texts low.
func deterministicVector(text string, dims int) []float32 {
	out := make([]float32, dims)
	salt := make([]byte, 4)
	for i := 0; i < dims; i++ {
		binary.LittleEndian.PutUint32(salt, uint32(i))
		h := sha256.Sum256(append(salt, []byte(text)...))
		raw := binary.LittleEndian.Uint32(h[:4])
		out[i] = float32(int32(raw)) / float32(1<<31)
	}
	return out
}

// openDB opens a fresh sqlite-backed storage.DB in tmp.
func openDB(t *testing.T) storage.DB {
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
	return db
}

// newManager wires Manager + DB + tmp data dir.
func newManager(t *testing.T) (*corpus.Manager, string) {
	t.Helper()
	db := openDB(t)
	dataDir := t.TempDir()
	m := corpus.NewManager(corpus.NewStorageDB(db), dataDir, newStubEmbedder())
	return m, dataDir
}

func TestMigration0307_TableExistsAfterOpen(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	ctx := context.Background()
	for _, table := range []string{"corpora", "corpus_files", "corpus_chunks"} {
		var name string
		if err := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).
			Scan(&name); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		if name != table {
			t.Errorf("table = %q, want %q", name, table)
		}
	}
	for _, idx := range []string{"idx_corpora_scope", "idx_corpus_files_corpus", "idx_corpus_chunks_corpus"} {
		var name string
		if err := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx).
			Scan(&name); err != nil {
			t.Fatalf("index %s: %v", idx, err)
		}
	}
}

func TestCreateCorpus_ScopeValidation(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()

	// global with scope_id is OK (scope_id gets cleared).
	c, err := m.CreateCorpus(ctx, "globals", corpus.ScopeGlobal, "ignored", "wiki")
	if err != nil {
		t.Fatalf("CreateCorpus: %v", err)
	}
	if c.ScopeID != "" {
		t.Errorf("global scope_id = %q, want empty", c.ScopeID)
	}

	// project without scope_id rejected.
	if _, err := m.CreateCorpus(ctx, "p", corpus.ScopeProject, "", ""); !errors.Is(err, corpus.ErrInvalidScope) {
		t.Errorf("project missing scope_id: err = %v, want ErrInvalidScope", err)
	}

	// unknown scope rejected.
	if _, err := m.CreateCorpus(ctx, "x", "weird", "", ""); !errors.Is(err, corpus.ErrInvalidScope) {
		t.Errorf("bad scope: err = %v, want ErrInvalidScope", err)
	}

	// blank name rejected.
	if _, err := m.CreateCorpus(ctx, "  ", corpus.ScopeGlobal, "", ""); !errors.Is(err, corpus.ErrInvalidName) {
		t.Errorf("blank name: err = %v, want ErrInvalidName", err)
	}
}

func TestListAndDeleteCorpora(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()

	a, _ := m.CreateCorpus(ctx, "alpha", corpus.ScopeGlobal, "", "")
	b, _ := m.CreateCorpus(ctx, "beta", corpus.ScopeProject, "p1", "")

	all, err := m.ListCorpora(ctx, "")
	if err != nil {
		t.Fatalf("ListCorpora: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("len = %d, want 2", len(all))
	}

	gOnly, _ := m.ListCorpora(ctx, corpus.ScopeGlobal)
	if len(gOnly) != 1 || gOnly[0].ID != a.ID {
		t.Errorf("global filter = %v", gOnly)
	}

	if err := m.DeleteCorpus(ctx, b.ID); err != nil {
		t.Fatalf("DeleteCorpus: %v", err)
	}
	if _, err := m.GetCorpus(ctx, b.ID); !errors.Is(err, corpus.ErrCorpusNotFound) {
		t.Errorf("after delete: err = %v, want ErrCorpusNotFound", err)
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func TestIngestPath_BasicAndChunks(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.md":      "# Title\n\nfoo bar\n\n## Section 2\n\nbaz qux\n",
		"sub/x.go":  "package x\n\nfunc Hello() string { return \"hi\" }\n",
		"empty.txt": "",
	})

	st, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("IngestPathSync: %v", err)
	}
	if st.State != corpus.IngestStateCompleted {
		t.Fatalf("state = %s, err = %s", st.State, st.Error)
	}
	if st.FilesDone != 3 {
		t.Errorf("FilesDone = %d, want 3", st.FilesDone)
	}
	if st.ChunksTotal == 0 {
		t.Errorf("ChunksTotal = 0, want >0")
	}

	files, err := m.ListFiles(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("ListFiles len = %d, want 3", len(files))
	}
}

func TestIngestPath_HashSkip(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"keep.md":  "# Hello\n\nworld\n",
		"touch.md": "# Touch\n\nfirst\n",
	})

	first, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.FilesDone != 2 || first.FilesSkip != 0 {
		t.Errorf("first: FilesDone=%d FilesSkip=%d", first.FilesDone, first.FilesSkip)
	}

	// Mutate one file; second ingest should re-embed only that file.
	writeFiles(t, dir, map[string]string{"touch.md": "# Touch\n\nmutated\n"})
	second, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if second.FilesSkip != 1 {
		t.Errorf("second: FilesSkip=%d, want 1", second.FilesSkip)
	}
	if second.FilesDone-second.FilesSkip != 1 {
		t.Errorf("second: re-embedded = %d, want 1", second.FilesDone-second.FilesSkip)
	}
}

func TestIngestPath_AtomicRollbackOnEmbedError(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	dataDir := t.TempDir()
	emb := newStubEmbedder()
	m := corpus.NewManager(corpus.NewStorageDB(db), dataDir, emb)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.md": "# Title\n\nfoo bar\n",
	})

	// Force every Embed call to fail.
	emb.fail = func() error { return errors.New("synthetic embed failure") }

	st, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("IngestPathSync: %v", err)
	}
	if st.State != corpus.IngestStateFailed {
		t.Errorf("state = %s, want failed", st.State)
	}
	if !strings.Contains(st.Error, "synthetic embed failure") {
		t.Errorf("error = %q, want contains synthetic", st.Error)
	}

	// SQL state must show no rows committed: atomic per-file means a
	// failed embed for a.md leaves zero corpus_files + zero corpus_chunks.
	files, err := m.ListFiles(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("ListFiles after failed ingest = %d rows, want 0", len(files))
	}

	// On-disk vector store should not exist (or be empty).
	storeFile := filepath.Join(dataDir, "corpora", c.ID, "vectors.gob")
	if fi, err := os.Stat(storeFile); err == nil && fi.Size() > 0 {
		// gob of empty slice is small; but no entries should be in it.
		_, _, err := m.Retrieve(ctx, c.ID, "anything", corpus.RetrieveOptions{})
		if err != nil {
			t.Fatalf("Retrieve after failed ingest: %v", err)
		}
	}
}

func TestRetrieve_TopKAndProvenance(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"alpha.md": "alpha content one two three\n",
		"beta.md":  "beta content four five six\n",
	})
	if _, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true}); err != nil {
		t.Fatalf("IngestPathSync: %v", err)
	}

	// Query exactly matches the chunk text (no trailing newline — the
	// chunker trims it so chunked text matches the query verbatim).
	results, dropped, err := m.Retrieve(ctx, c.ID, "alpha content one two three",
		corpus.RetrieveOptions{TopK: 2})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("Retrieve returned 0 results")
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	top := results[0]
	if top.Chunk.Provenance.FilePath != "alpha.md" {
		t.Errorf("top.FilePath = %q, want alpha.md", top.Chunk.Provenance.FilePath)
	}
	if top.Similarity <= 0.99 {
		t.Errorf("top similarity = %f; expected near 1.0 for identical query+text", top.Similarity)
	}
	if top.Chunk.Provenance.LineStart == 0 {
		t.Errorf("LineStart not populated")
	}
}

func TestRetrieve_TokenBudgetCap(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")

	// Build 10 files of ~5 KiB each so top-K of 10 would total ~50 KiB
	// — well past the 16 KiB budget; the cap should fire.
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		filename := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		// 5 KiB of content with the text "needle" sprinkled in so
		// the query matches.
		content := strings.Repeat("needle haystack content line\n", 200)
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if _, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true}); err != nil {
		t.Fatalf("IngestPathSync: %v", err)
	}

	// Tight budget: 4 KiB. The first chunk is well under that; the
	// second should overflow and be dropped.
	results, dropped, err := m.Retrieve(ctx, c.ID, "needle",
		corpus.RetrieveOptions{TopK: 10, TokenBudget: 4 * 1024})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	totalBytes := 0
	for _, r := range results {
		totalBytes += len(r.Chunk.Text)
	}
	if totalBytes > 4*1024+1024 {
		t.Errorf("total = %d bytes, want under budget+slack", totalBytes)
	}
	if dropped == 0 && len(results) >= 10 {
		t.Errorf("dropped = 0 with %d results; budget should have capped", len(results))
	}
}

func TestRetrieve_EmptyCorpus(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")
	results, _, err := m.Retrieve(ctx, c.ID, "anything", corpus.RetrieveOptions{})
	if err != nil {
		t.Fatalf("Retrieve empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len = %d, want 0", len(results))
	}
}

func TestRetrieve_ResumeFromSQLAfterSnapshotLoss(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	dataDir := t.TempDir()
	m := corpus.NewManager(corpus.NewStorageDB(db), dataDir, newStubEmbedder())
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.md": "alpha line\n"})
	if _, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Simulate a crash that lost the on-disk vector snapshot; the
	// next Retrieve should rehydrate from SQL.
	storePath := filepath.Join(dataDir, "corpora", c.ID, "vectors.gob")
	if err := os.Remove(storePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove snapshot: %v", err)
	}

	// Fresh manager re-opens against the same dataDir + SQL.
	m2 := corpus.NewManager(corpus.NewStorageDB(db), dataDir, newStubEmbedder())
	results, _, err := m2.Retrieve(ctx, c.ID, "alpha line\n", corpus.RetrieveOptions{TopK: 1})
	if err != nil {
		t.Fatalf("Retrieve after rehydrate: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("rehydrated retrieve returned 0 chunks; SQL fallback failed")
	}
}

func TestIngest_NoEmbedder(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	dataDir := t.TempDir()
	m := corpus.NewManager(corpus.NewStorageDB(db), dataDir, nil)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.md": "x\n"})
	if _, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true}); !errors.Is(err, corpus.ErrEmbedderUnavailable) {
		t.Errorf("err = %v, want ErrEmbedderUnavailable", err)
	}
}

func TestIngest_HashStable(t *testing.T) {
	// Verify that hash skip works for binary-clean re-ingest with no
	// content change — files done > 0 but skip = files done.
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"x.txt": "first\nsecond\nthird\n",
	})
	if _, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true}); err != nil {
		t.Fatalf("first: %v", err)
	}
	st, err := m.IngestPathSync(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if st.FilesSkip != 1 {
		t.Errorf("FilesSkip = %d, want 1", st.FilesSkip)
	}
}

func TestJobStatus_NotFound(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	if _, err := m.JobStatus("nope"); !errors.Is(err, corpus.ErrJobNotFound) {
		t.Errorf("err = %v, want ErrJobNotFound", err)
	}
}

func TestIngest_AsyncJobFlow(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t)
	ctx := context.Background()
	c, _ := m.CreateCorpus(ctx, "k", corpus.ScopeGlobal, "", "")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.md": "hi\n"})

	st, err := m.IngestPath(ctx, c.ID, dir, corpus.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("IngestPath: %v", err)
	}
	if st.JobID == "" {
		t.Fatal("empty job id")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := m.JobStatus(st.JobID)
		if err != nil {
			t.Fatalf("JobStatus: %v", err)
		}
		if cur.State == corpus.IngestStateCompleted {
			return
		}
		if cur.State == corpus.IngestStateFailed {
			t.Fatalf("ingest failed: %s", cur.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ingest did not complete within 2s")
}
