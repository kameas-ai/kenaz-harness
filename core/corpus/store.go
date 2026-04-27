package corpus

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"time"
)

// DB is the minimal storage seam this package needs. Mirrors the slice
// of core/storage.DB used here so tests can pass a fake without pulling
// the storage package's full surface in.
type DB interface {
	Reader() Reader
	WriteTx(ctx context.Context, fn func(tx WriteTx) error) error
}

// Reader is the read-only handle.
type Reader interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// WriteTx is the write transaction surface.
type WriteTx interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Row, Rows, Result mirror the storage equivalents.
type Row interface {
	Scan(dest ...any) error
}

type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

type Result interface {
	RowsAffected() (int64, error)
	LastInsertID() (int64, error)
}

// encodeEmbedding gob-encodes a float vector for storage.embedding BLOB
// column. The on-disk vector store (core/corpus/vectorstore.go) writes
// the same bytes to its gob snapshot so SQL <-> snapshot stay aligned.
func encodeEmbedding(vec []float32) ([]byte, error) {
	if len(vec) == 0 {
		return nil, errors.New("corpus: empty embedding")
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(vec); err != nil {
		return nil, fmt.Errorf("corpus: encode embedding: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeEmbedding decodes a gob-encoded float vector.
func decodeEmbedding(b []byte) ([]float32, error) {
	if len(b) == 0 {
		return nil, errors.New("corpus: empty embedding bytes")
	}
	var vec []float32
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&vec); err != nil {
		return nil, fmt.Errorf("corpus: decode embedding: %w", err)
	}
	return vec, nil
}

// insertCorpus persists a Corpus row inside an open WriteTx.
func insertCorpus(ctx context.Context, tx WriteTx, c Corpus) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO corpora
            (id, name, scope, scope_id, tag, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `,
		c.ID, c.Name, c.Scope, c.ScopeID, c.Tag,
		c.CreatedAt.UTC().UnixNano(), c.UpdatedAt.UTC().UnixNano())
	return err
}

// updateCorpusTimestamp bumps updated_at after an ingest write.
func updateCorpusTimestamp(ctx context.Context, tx WriteTx, corpusID string, ts time.Time) error {
	_, err := tx.Exec(ctx,
		`UPDATE corpora SET updated_at = ? WHERE id = ?`,
		ts.UTC().UnixNano(), corpusID)
	return err
}

// loadCorpus reads a single corpus row.
func loadCorpus(ctx context.Context, r Reader, corpusID string) (Corpus, error) {
	row := r.QueryRow(ctx,
		`SELECT id, name, scope, scope_id, tag, created_at, updated_at
         FROM corpora WHERE id = ?`, corpusID)
	var (
		c            Corpus
		createdAt    int64
		updatedAt    int64
	)
	if err := row.Scan(&c.ID, &c.Name, &c.Scope, &c.ScopeID, &c.Tag, &createdAt, &updatedAt); err != nil {
		return Corpus{}, fmt.Errorf("%w: %s", ErrCorpusNotFound, corpusID)
	}
	c.CreatedAt = time.Unix(0, createdAt).UTC()
	c.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return c, nil
}

// listCorpora returns all corpora matching scope (empty = no filter).
func listCorpora(ctx context.Context, r Reader, scope string) ([]Corpus, error) {
	var (
		rows Rows
		err  error
	)
	if scope == "" {
		rows, err = r.Query(ctx,
			`SELECT id, name, scope, scope_id, tag, created_at, updated_at
             FROM corpora ORDER BY created_at DESC`)
	} else {
		rows, err = r.Query(ctx,
			`SELECT id, name, scope, scope_id, tag, created_at, updated_at
             FROM corpora WHERE scope = ? ORDER BY created_at DESC`, scope)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Corpus, 0, 16)
	for rows.Next() {
		var (
			c                    Corpus
			createdAt, updatedAt int64
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Scope, &c.ScopeID, &c.Tag, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(0, createdAt).UTC()
		c.UpdatedAt = time.Unix(0, updatedAt).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// deleteCorpus removes a corpus row plus all dependent rows via FK
// CASCADE. Returns true when a row was removed.
func deleteCorpus(ctx context.Context, tx WriteTx, corpusID string) (bool, error) {
	res, err := tx.Exec(ctx, `DELETE FROM corpora WHERE id = ?`, corpusID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// fileByPath returns the corpus_files row for (corpus_id, path) — used
// by the hash-check skip path. err == ErrCorpusNotFound when missing.
func fileByPath(ctx context.Context, r Reader, corpusID, path string) (CorpusFile, error) {
	row := r.QueryRow(ctx,
		`SELECT id, corpus_id, path, sha256, file_size, line_count, ingested_at
         FROM corpus_files WHERE corpus_id = ? AND path = ?`, corpusID, path)
	var (
		f          CorpusFile
		ingestedAt int64
	)
	if err := row.Scan(&f.ID, &f.CorpusID, &f.Path, &f.SHA256, &f.FileSize, &f.LineCount, &ingestedAt); err != nil {
		return CorpusFile{}, ErrCorpusNotFound
	}
	f.IngestedAt = time.Unix(0, ingestedAt).UTC()
	return f, nil
}

// deleteFile drops a corpus_files row + cascade chunks. Used by the
// re-ingest path when a file's hash changes — the old chunks must go.
func deleteFile(ctx context.Context, tx WriteTx, fileID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM corpus_files WHERE id = ?`, fileID)
	return err
}

// insertFile persists one corpus_files row.
func insertFile(ctx context.Context, tx WriteTx, f CorpusFile) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO corpus_files
            (id, corpus_id, path, sha256, file_size, line_count, ingested_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `,
		f.ID, f.CorpusID, f.Path, f.SHA256, f.FileSize, f.LineCount,
		f.IngestedAt.UTC().UnixNano())
	return err
}

// insertChunk persists one corpus_chunks row.
func insertChunk(ctx context.Context, tx WriteTx, c Chunk) error {
	emb, err := encodeEmbedding(c.Embedding)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
        INSERT INTO corpus_chunks
            (id, corpus_id, file_id, chunk_seq, text, embedding,
             line_start, line_end, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `,
		c.ID, c.CorpusID, c.FileID, c.ChunkSeq, c.Text, emb,
		c.Provenance.LineStart, c.Provenance.LineEnd,
		c.CreatedAt.UTC().UnixNano())
	return err
}

// listFiles returns every file row for corpusID, ordered by path.
func listFiles(ctx context.Context, r Reader, corpusID string) ([]CorpusFile, error) {
	rows, err := r.Query(ctx, `
        SELECT id, corpus_id, path, sha256, file_size, line_count, ingested_at
        FROM corpus_files WHERE corpus_id = ? ORDER BY path
    `, corpusID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CorpusFile, 0, 32)
	for rows.Next() {
		var (
			f          CorpusFile
			ingestedAt int64
		)
		if err := rows.Scan(&f.ID, &f.CorpusID, &f.Path, &f.SHA256, &f.FileSize, &f.LineCount, &ingestedAt); err != nil {
			return nil, err
		}
		f.IngestedAt = time.Unix(0, ingestedAt).UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

// listChunksForFile returns chunk rows ordered by chunk_seq.
func listChunksForFile(ctx context.Context, r Reader, corpusID, fileID string) ([]Chunk, error) {
	rows, err := r.Query(ctx, `
        SELECT c.id, c.corpus_id, c.file_id, c.chunk_seq, c.text, c.embedding,
               c.line_start, c.line_end, c.created_at, f.path, f.sha256
        FROM corpus_chunks c
        JOIN corpus_files f ON f.id = c.file_id
        WHERE c.corpus_id = ? AND c.file_id = ?
        ORDER BY c.chunk_seq
    `, corpusID, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Chunk, 0, 32)
	for rows.Next() {
		var (
			c              Chunk
			embBytes       []byte
			createdAt      int64
			filePath, sha  string
		)
		if err := rows.Scan(&c.ID, &c.CorpusID, &c.FileID, &c.ChunkSeq, &c.Text,
			&embBytes, &c.Provenance.LineStart, &c.Provenance.LineEnd,
			&createdAt, &filePath, &sha); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(0, createdAt).UTC()
		c.Provenance.FilePath = filePath
		c.Provenance.SHA256 = sha
		if vec, err := decodeEmbedding(embBytes); err == nil {
			c.Embedding = vec
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadAllChunks streams every chunk for a corpus into memory. Used by
// the retrieval path when (re)building the in-memory vector index.
func loadAllChunks(ctx context.Context, r Reader, corpusID string) ([]Chunk, error) {
	rows, err := r.Query(ctx, `
        SELECT c.id, c.corpus_id, c.file_id, c.chunk_seq, c.text, c.embedding,
               c.line_start, c.line_end, c.created_at, f.path, f.sha256
        FROM corpus_chunks c
        JOIN corpus_files f ON f.id = c.file_id
        WHERE c.corpus_id = ?
        ORDER BY c.file_id, c.chunk_seq
    `, corpusID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Chunk, 0, 64)
	for rows.Next() {
		var (
			c             Chunk
			embBytes      []byte
			createdAt     int64
			filePath, sha string
		)
		if err := rows.Scan(&c.ID, &c.CorpusID, &c.FileID, &c.ChunkSeq, &c.Text,
			&embBytes, &c.Provenance.LineStart, &c.Provenance.LineEnd,
			&createdAt, &filePath, &sha); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(0, createdAt).UTC()
		c.Provenance.FilePath = filePath
		c.Provenance.SHA256 = sha
		if vec, err := decodeEmbedding(embBytes); err == nil {
			c.Embedding = vec
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
