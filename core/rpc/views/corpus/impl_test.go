package corpus_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	corecorpus "github.com/kameas-ai/kenaz-harness/core/corpus"
	corpusview "github.com/kameas-ai/kenaz-harness/core/rpc/views/corpus"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

type stubEmbedder struct{}

func (stubEmbedder) Dimensions() int { return 32 }

func (stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 32)
		for j := 0; j < 32; j++ {
			salt := make([]byte, 4)
			binary.LittleEndian.PutUint32(salt, uint32(j))
			h := sha256.Sum256(append(salt, []byte(t)...))
			raw := binary.LittleEndian.Uint32(h[:4])
			v[j] = float32(int32(raw)) / float32(1<<31)
		}
		out[i] = v
	}
	return out, nil
}

func newAPI(t *testing.T) *corpusview.API {
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
	mgr := corecorpus.NewManager(corecorpus.NewStorageDB(db), t.TempDir(), stubEmbedder{})
	return corpusview.New(mgr)
}

func TestNilManager_ReturnsUnavailable(t *testing.T) {
	t.Parallel()
	a := corpusview.New(nil)
	ctx := context.Background()
	if _, err := a.ListCorpora(ctx, ""); !errors.Is(err, corpusview.ErrManagerUnavailable) {
		t.Errorf("err = %v, want ErrManagerUnavailable", err)
	}
}

func TestCreateAndList(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	ctx := context.Background()
	c, err := a.CreateCorpus(ctx, corpusview.CreateRequest{
		Name: "wiki", Scope: corecorpus.ScopeGlobal,
	})
	if err != nil {
		t.Fatalf("CreateCorpus: %v", err)
	}
	if c.ID == "" {
		t.Errorf("empty id")
	}
	rows, err := a.ListCorpora(ctx, "")
	if err != nil {
		t.Fatalf("ListCorpora: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != c.ID {
		t.Errorf("rows = %v", rows)
	}
}

func TestIngestAndRetrieve(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	ctx := context.Background()
	c, err := a.CreateCorpus(ctx, corpusview.CreateRequest{
		Name: "k", Scope: corecorpus.ScopeGlobal,
	})
	if err != nil {
		t.Fatalf("CreateCorpus: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	st, err := a.IngestPath(ctx, c.ID, dir, corpusview.IngestOptions{Recursive: true})
	if err != nil {
		t.Fatalf("IngestPath: %v", err)
	}
	// Wait for completion (~2s budget).
	deadline := time.Now().Add(2 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		cur, err := a.JobStatus(ctx, st.JobID)
		if err != nil {
			t.Fatalf("JobStatus: %v", err)
		}
		if cur.State == corecorpus.IngestStateCompleted {
			completed = true
			break
		}
		if cur.State == corecorpus.IngestStateFailed {
			t.Fatalf("ingest failed: %s", cur.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !completed {
		t.Fatal("ingest did not complete in 2s")
	}

	resp, err := a.Retrieve(ctx, c.ID, corpusview.RetrieveRequest{
		Query: "hello world", TopK: 1,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("results = 0")
	}
	if resp.Results[0].Chunk.Provenance.FilePath != "a.md" {
		t.Errorf("filepath = %q", resp.Results[0].Chunk.Provenance.FilePath)
	}
}

func TestEmptyArgs_Rejected(t *testing.T) {
	t.Parallel()
	a := newAPI(t)
	ctx := context.Background()
	if err := a.DeleteCorpus(ctx, ""); !errors.Is(err, corpusview.ErrInvalidArg) {
		t.Errorf("Delete empty: %v", err)
	}
	if _, err := a.GetCorpus(ctx, ""); !errors.Is(err, corpusview.ErrInvalidArg) {
		t.Errorf("Get empty: %v", err)
	}
	if _, err := a.IngestPath(ctx, "x", "", corpusview.IngestOptions{}); !errors.Is(err, corpusview.ErrInvalidArg) {
		t.Errorf("IngestPath empty path: %v", err)
	}
	if _, err := a.Retrieve(ctx, "x", corpusview.RetrieveRequest{}); !errors.Is(err, corpusview.ErrInvalidArg) {
		t.Errorf("Retrieve empty query: %v", err)
	}
}
