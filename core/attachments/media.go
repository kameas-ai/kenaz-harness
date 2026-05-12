package attachments

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	// Register JPEG, PNG, GIF decoders for image.DecodeConfig.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ledongthuc/pdf"
	"golang.org/x/image/webp"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// MediaArtifact is the durable metadata row for one binary blob stored
// under <DataDir>/media/<content-hash>. Multiple MediaArtifact rows may
// share a ContentHash (CAS dedup): each upload mints a fresh ID + row,
// but at most one file lives on disk per hash.
//
// NOTE: Do NOT capture user-attached media into the artifact pipeline
// automatically. MediaStore already provides dedup + persistence; the
// artifact panel is for model-emitted artifacts. Users can manually save
// an attachment as an artifact via the existing "Save as artifact"
// affordance. (Decision recorded in plan.md §8, multimodal-io-01KQ8TDF.)
type MediaArtifact struct {
	// ID is a 26-char Crockford-base32 ULID minted at Put time.
	ID string
	// ContentHash is the hex sha256 of the raw bytes; matches the file
	// name on disk (one file per unique hash).
	ContentHash string
	// MediaType is the IANA MIME type recorded by the uploader.
	MediaType string
	// ByteSize is the on-disk size in bytes.
	ByteSize int64
	// OriginalName is the uploader-supplied filename. May be empty.
	OriginalName string
	// CreatedAt is the wall-clock at insert time (UTC).
	CreatedAt time.Time

	// ImageWidth and ImageHeight are the pixel dimensions extracted on Put
	// for image/* MIME types. Both are zero when not applicable or when
	// extraction fails (non-fatal; best-effort). Populated by
	// multimodal-io-01KQ8TDF WP06.
	ImageWidth  int
	ImageHeight int
	// PageCount is the number of pages extracted for application/pdf
	// attachments. Zero means unknown or not a PDF. Populated by WP06.
	PageCount int
}

// MediaFilter narrows the List query. Empty fields match every row.
type MediaFilter struct {
	// ContentHash filters to rows sharing one sha256 hex hash.
	ContentHash string
	// MediaType filters to rows of one IANA MIME type.
	MediaType string
}

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrMediaNotFound is returned when an id has no matching row.
	ErrMediaNotFound = errors.New("attachments/media: not found")

	// ErrOversize is returned by Put when the supplied bytes exceed the
	// configured MaxBytes. The default boundary is 50 MiB; the rpc layer
	// imposes a smaller per-file cap on top of this for user-supplied
	// uploads.
	ErrOversize = errors.New("attachments/media: bytes exceed maximum size")

	// ErrCorruptedStore is returned when a metadata row points at a
	// content hash whose on-disk file is missing. Callers can recover by
	// running PruneOrphans + re-uploading.
	ErrCorruptedStore = errors.New("attachments/media: on-disk file missing for metadata row")
)

// DefaultMaxBytes is the default boundary above which Put returns
// ErrOversize. Tunable via NewSQLMediaStoreWithOptions.
const DefaultMaxBytes int64 = 50 * 1024 * 1024

// RefcountSource counts the references to a content hash held by one
// reference table (e.g. context_attachments, or — once the
// artifacts-storage mission lands — artifacts). The MediaStore composes
// every registered RefcountSource into the total refcount returned by
// RefcountFor. Future missions register additional sources via
// MediaStore.RegisterRefcountSource without having to touch this
// package's persistence code.
type RefcountSource interface {
	// Refcount reports how many rows in this source reference any
	// MediaArtifact row carrying the supplied content hash.
	Refcount(ctx context.Context, contentHash string) (int, error)
}

// MediaStore is the persistence contract for binary media. The CAS
// guarantee: at most one file per sha256 hash, regardless of how many
// metadata rows reference it. Refcount-aware deletion walks every
// registered RefcountSource so the on-disk file only goes away when no
// caller still references the hash.
type MediaStore interface {
	// Put materializes a fresh MediaArtifact. Bytes are sha256-hashed;
	// if no file exists at <DataDir>/media/<hash> the bytes are written
	// atomically. A new metadata row is always inserted (so callers can
	// identify the upload by a unique id even when the bytes dedupe).
	Put(ctx context.Context, bytes []byte, mediaType, originalName string) (MediaArtifact, error)

	// Get returns the metadata + on-disk bytes for one id.
	Get(ctx context.Context, id string) (MediaArtifact, []byte, error)

	// GetByHash returns the most recently created metadata row for a
	// content hash plus the on-disk bytes. ErrMediaNotFound when no row
	// references the hash.
	GetByHash(ctx context.Context, contentHash string) (MediaArtifact, []byte, error)

	// Delete removes one metadata row by id. The on-disk file is
	// untouched: callers that want to free the file invoke RefcountFor
	// against the deleted row's content hash and remove the file when
	// the count drops to zero. The split keeps Delete's
	// single-responsibility — the artifacts mission needs the same
	// metadata-only delete primitive.
	Delete(ctx context.Context, id string) error

	// List returns metadata rows matching filter, ordered by created_at
	// DESC.
	List(ctx context.Context, filter MediaFilter) ([]MediaArtifact, error)

	// RefcountFor walks every registered RefcountSource and returns the
	// total reference count for the supplied content hash. Includes the
	// count of media_artifacts rows (one per upload) by default —
	// callers ANDing zero-refcount + file-removal must subtract one from
	// the count for the row they're about to delete (or call Delete
	// first, then RefcountFor).
	RefcountFor(ctx context.Context, contentHash string) (int, error)

	// PruneOrphans walks <DataDir>/media/ and removes files whose hash
	// is not referenced by any metadata row OR any registered
	// RefcountSource. Returns the count of files removed. Idempotent.
	PruneOrphans(ctx context.Context) (int, error)

	// RegisterRefcountSource adds a RefcountSource to the composite
	// total returned by RefcountFor. Safe to call after construction;
	// future missions (artifacts-storage) plug in an additional source
	// here.
	RegisterRefcountSource(rs RefcountSource)
}

// MediaStoreOption configures a SQLMediaStore at construction time.
type MediaStoreOption func(*sqlMediaStore)

// WithMediaClock overrides the wall-clock source. Tests pin this for
// deterministic CreatedAt timestamps.
func WithMediaClock(now func() time.Time) MediaStoreOption {
	return func(s *sqlMediaStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithMediaIDGen overrides the id generator. Production uses the
// monotonic ULID generator.
func WithMediaIDGen(gen func() (string, error)) MediaStoreOption {
	return func(s *sqlMediaStore) {
		if gen != nil {
			s.idGen = gen
		}
	}
}

// WithMaxBytes overrides DefaultMaxBytes. n <= 0 disables the cap (not
// recommended outside tests).
func WithMaxBytes(n int64) MediaStoreOption {
	return func(s *sqlMediaStore) {
		s.maxBytes = n
	}
}

// NewSQLMediaStore returns a MediaStore backed by storage.DB +
// <dataDir>/media. The directory is created on first Put.
func NewSQLMediaStore(db storage.DB, dataDir string, opts ...MediaStoreOption) MediaStore {
	s := &sqlMediaStore{
		db:       db,
		dataDir:  dataDir,
		now:      func() time.Time { return time.Now().UTC() },
		idGen:    defaultMediaID,
		maxBytes: DefaultMaxBytes,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// sqlMediaStore is the SQL+disk-backed MediaStore implementation.
type sqlMediaStore struct {
	db       storage.DB
	dataDir  string
	now      func() time.Time
	idGen    func() (string, error)
	maxBytes int64

	// putMu serializes concurrent Put of identical bytes through a
	// per-hash lock. Filesystem rename atomicity already protects the
	// on-disk file; the mutex prevents two goroutines from both
	// computing "file missing" → both writing the tmpfile, which on a
	// crash window between rename + fsync could race.
	putMu sync.Mutex
	hashLocks map[string]*sync.Mutex

	// sourcesMu guards the RefcountSource slice. RegisterRefcountSource
	// is safe to call after construction.
	sourcesMu sync.RWMutex
	sources   []RefcountSource
}

// RegisterRefcountSource adds a RefcountSource to the composite
// refcount total. WP02 wires the attachments source at chassis-construction
// time; future missions extend the predicate by calling this from their
// own wiring code.
func (s *sqlMediaStore) RegisterRefcountSource(rs RefcountSource) {
	if rs == nil {
		return
	}
	s.sourcesMu.Lock()
	s.sources = append(s.sources, rs)
	s.sourcesMu.Unlock()
}

// Put materializes a fresh MediaArtifact, dedup'd on disk by sha256.
//
// For image/* MIME types, Put also extracts pixel dimensions (best-effort;
// zero on failure). For application/pdf, Put extracts the page count
// (best-effort; encrypted PDFs return ErrAttachmentEncrypted).
// These metadata fields are populated on the returned MediaArtifact but
// are NOT persisted to the DB in this version — the DB schema migration
// (adding image_width/image_height/page_count columns) is tracked as a
// follow-up. Callers should read them from the returned value and attach
// them to ContentBlock.Source.ImageDimensions for the gate (WP06).
func (s *sqlMediaStore) Put(ctx context.Context, b []byte, mediaType, originalName string) (MediaArtifact, error) {
	if s.maxBytes > 0 && int64(len(b)) > s.maxBytes {
		return MediaArtifact{}, fmt.Errorf("%w: %d > %d", ErrOversize, len(b), s.maxBytes)
	}
	sum := sha256.Sum256(b)
	hashHex := hex.EncodeToString(sum[:])

	if err := s.ensureMediaDir(); err != nil {
		return MediaArtifact{}, err
	}
	if err := s.writeFileIfMissing(hashHex, b); err != nil {
		return MediaArtifact{}, err
	}

	id, err := s.idGen()
	if err != nil {
		return MediaArtifact{}, fmt.Errorf("attachments/media: id gen: %w", err)
	}
	now := s.now()
	row := MediaArtifact{
		ID:           id,
		ContentHash:  hashHex,
		MediaType:    mediaType,
		ByteSize:     int64(len(b)),
		OriginalName: originalName,
		CreatedAt:    now,
	}

	// Best-effort metadata extraction (WP06).
	if strings.HasPrefix(mediaType, "image/") {
		w, h, _ := extractImageDimensions(b, mediaType)
		row.ImageWidth = w
		row.ImageHeight = h
	} else if mediaType == "application/pdf" {
		pages, pdfErr := extractPDFPageCount(b)
		if pdfErr != nil {
			var encErr *llm.ErrAttachmentEncrypted
			if errors.As(pdfErr, &encErr) {
				return MediaArtifact{}, pdfErr
			}
			// Non-fatal: page count stays 0 on other parse failures.
		} else {
			row.PageCount = pages
		}
	}

	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO media_artifacts
                (id, content_hash, media_type, byte_size, original_name, created_at)
            VALUES (?, ?, ?, ?, ?, ?)
        `, row.ID, row.ContentHash, row.MediaType, row.ByteSize,
			row.OriginalName, row.CreatedAt.UnixNano())
		return err
	}); err != nil {
		return MediaArtifact{}, err
	}
	return row, nil
}

// ── metadata extraction helpers (WP06) ───────────────────────────────────

// extractImageDimensions decodes the image header of b (whose IANA mime type
// is mt) and returns the pixel dimensions. Returns (0, 0, err) on failure;
// callers treat zero dimensions as "unknown" and skip the pixel-cap check.
func extractImageDimensions(b []byte, mt string) (width, height int, _ error) {
	var cfg image.Config
	var err error
	if mt == "image/webp" {
		cfg, err = webp.DecodeConfig(bytes.NewReader(b))
	} else {
		// image.DecodeConfig auto-dispatches for jpeg/png/gif via the
		// side-effect imports at the top of this file.
		cfg, _, err = image.DecodeConfig(bytes.NewReader(b))
	}
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// extractPDFPageCount parses b as a PDF and returns the number of pages.
// Returns (0, *llm.ErrAttachmentEncrypted) for password-protected PDFs.
// Input is capped at 50 MiB by the sqlMediaStore.maxBytes guard in Put;
// passing an uncapped reader here is safe because the caller already checked.
//
// NOTE: ledongthuc/pdf can panic on malformed or encrypted PDFs during internal
// object resolution. We recover from any such panic and treat it as a non-fatal
// parse error so Put can succeed with PageCount=0. A panic that matches the
// "encrypt" / "password" keywords is still surfaced as ErrAttachmentEncrypted.
func extractPDFPageCount(b []byte) (pages int, rerr error) {
	defer func() {
		if p := recover(); p != nil {
			msg := strings.ToLower(fmt.Sprintf("%v", p))
			if strings.Contains(msg, "encrypt") || strings.Contains(msg, "password") {
				rerr = &llm.ErrAttachmentEncrypted{}
			} else {
				rerr = fmt.Errorf("attachments/media: pdf parse panic: %v", p)
			}
			pages = 0
		}
	}()

	r, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		// ledongthuc/pdf returns a string error for encrypted PDFs;
		// check by message since the library doesn't export a sentinel.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "encrypt") || strings.Contains(msg, "password") {
			return 0, &llm.ErrAttachmentEncrypted{}
		}
		return 0, err
	}
	return r.NumPage(), nil
}

// Get returns metadata + bytes by id.
func (s *sqlMediaStore) Get(ctx context.Context, id string) (MediaArtifact, []byte, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectMedia+" WHERE id = ?", id)
	art, err := scanMedia(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MediaArtifact{}, nil, ErrMediaNotFound
		}
		return MediaArtifact{}, nil, err
	}
	bytes, err := s.readFile(art.ContentHash)
	if err != nil {
		return MediaArtifact{}, nil, err
	}
	return art, bytes, nil
}

// GetByHash returns the latest-by-created-at row for a hash.
func (s *sqlMediaStore) GetByHash(ctx context.Context, contentHash string) (MediaArtifact, []byte, error) {
	row := s.db.Reader().QueryRow(ctx,
		sqlSelectMedia+" WHERE content_hash = ? ORDER BY created_at DESC LIMIT 1",
		contentHash)
	art, err := scanMedia(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MediaArtifact{}, nil, ErrMediaNotFound
		}
		return MediaArtifact{}, nil, err
	}
	bytes, err := s.readFile(art.ContentHash)
	if err != nil {
		return MediaArtifact{}, nil, err
	}
	return art, bytes, nil
}

// Delete removes the metadata row by id. Caller is responsible for the
// CAS sweep (see RefcountFor).
func (s *sqlMediaStore) Delete(ctx context.Context, id string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, "DELETE FROM media_artifacts WHERE id = ?", id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrMediaNotFound
		}
		return nil
	})
}

const sqlSelectMedia = `
    SELECT id, content_hash, media_type, byte_size, original_name, created_at
    FROM media_artifacts
`

// List returns rows matching filter, ordered by created_at DESC.
func (s *sqlMediaStore) List(ctx context.Context, filter MediaFilter) ([]MediaArtifact, error) {
	q := sqlSelectMedia
	args := make([]any, 0, 2)
	conditions := make([]string, 0, 2)
	if filter.ContentHash != "" {
		conditions = append(conditions, "content_hash = ?")
		args = append(args, filter.ContentHash)
	}
	if filter.MediaType != "" {
		conditions = append(conditions, "media_type = ?")
		args = append(args, filter.MediaType)
	}
	if len(conditions) > 0 {
		q += " WHERE "
		for i, c := range conditions {
			if i > 0 {
				q += " AND "
			}
			q += c
		}
	}
	q += " ORDER BY created_at DESC"
	rows, err := s.db.Reader().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaArtifact
	for rows.Next() {
		a, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RefcountFor sums references across every registered RefcountSource
// PLUS the count of media_artifacts rows pointing at the hash. The
// media_artifacts count is included because every metadata row "owns"
// the on-disk file in equal measure — a sweeper that ignores these
// would delete a file still referenced by a fresh upload's row.
func (s *sqlMediaStore) RefcountFor(ctx context.Context, contentHash string) (int, error) {
	row := s.db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM media_artifacts WHERE content_hash = ?", contentHash)
	var metaCount int
	if err := row.Scan(&metaCount); err != nil {
		return 0, err
	}

	s.sourcesMu.RLock()
	srcs := append([]RefcountSource(nil), s.sources...)
	s.sourcesMu.RUnlock()

	total := metaCount
	for _, rs := range srcs {
		n, err := rs.Refcount(ctx, contentHash)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// PruneOrphans walks the media dir and drops any file whose hash has no
// referencing rows in any source. Idempotent.
func (s *sqlMediaStore) PruneOrphans(ctx context.Context) (int, error) {
	if err := s.ensureMediaDir(); err != nil {
		return 0, err
	}
	dir := s.mediaDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !looksLikeContentHash(name) {
			continue
		}
		count, err := s.RefcountFor(ctx, name)
		if err != nil {
			return removed, err
		}
		if count > 0 {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// ensureMediaDir creates the on-disk media directory if absent.
func (s *sqlMediaStore) ensureMediaDir() error {
	if s.dataDir == "" {
		return errors.New("attachments/media: empty DataDir")
	}
	return os.MkdirAll(s.mediaDir(), 0o755)
}

// mediaDir returns the directory holding the CAS files.
func (s *sqlMediaStore) mediaDir() string {
	return filepath.Join(s.dataDir, "media")
}

// writeFileIfMissing performs the atomic-write half of CAS. Concurrent
// callers for the same hash serialize through a per-hash mutex; if the
// file already exists at the target path no write happens.
func (s *sqlMediaStore) writeFileIfMissing(hashHex string, body []byte) error {
	mu := s.lockForHash(hashHex)
	mu.Lock()
	defer mu.Unlock()

	dest := filepath.Join(s.mediaDir(), hashHex)
	if _, err := os.Stat(dest); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	tmp, err := os.CreateTemp(s.mediaDir(), ".tmp-"+hashHex+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		cleanup()
		return err
	}
	// fsync the parent so the rename is durable across power loss.
	if dir, err := os.Open(s.mediaDir()); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// readFile pulls the bytes for a content hash. ErrCorruptedStore when
// the metadata row points at a missing file.
func (s *sqlMediaStore) readFile(hashHex string) ([]byte, error) {
	f, err := os.Open(filepath.Join(s.mediaDir(), hashHex))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrCorruptedStore, hashHex)
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// lockForHash returns the per-hash mutex used by writeFileIfMissing.
func (s *sqlMediaStore) lockForHash(hashHex string) *sync.Mutex {
	s.putMu.Lock()
	defer s.putMu.Unlock()
	if s.hashLocks == nil {
		s.hashLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := s.hashLocks[hashHex]
	if !ok {
		mu = &sync.Mutex{}
		s.hashLocks[hashHex] = mu
	}
	return mu
}

// scanMedia hydrates a MediaArtifact from a row scanner.
func scanMedia(sc interface{ Scan(dest ...any) error }) (MediaArtifact, error) {
	var (
		a         MediaArtifact
		createdAt int64
	)
	if err := sc.Scan(
		&a.ID, &a.ContentHash, &a.MediaType, &a.ByteSize,
		&a.OriginalName, &createdAt,
	); err != nil {
		return MediaArtifact{}, err
	}
	a.CreatedAt = time.Unix(0, createdAt).UTC()
	return a, nil
}

// looksLikeContentHash reports whether name is a 64-char lowercase hex
// string. PruneOrphans uses this to ignore non-CAS files (tmpfiles,
// future sidecars) that may live alongside the hash files.
func looksLikeContentHash(name string) bool {
	if len(name) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := name[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ── Refcount sources ───────────────────────────────────────────────────

// AttachmentsRefcountSource counts context_attachments rows whose
// media_id references a MediaArtifact carrying the supplied content
// hash. WP02 registers this as the first refcount source; the
// artifacts-storage mission will register a parallel ArtifactsRefcountSource
// in the same shape.
type AttachmentsRefcountSource struct {
	DB storage.DB
}

// Refcount implements RefcountSource.
func (a AttachmentsRefcountSource) Refcount(ctx context.Context, contentHash string) (int, error) {
	if a.DB == nil {
		return 0, nil
	}
	row := a.DB.Reader().QueryRow(ctx, `
        SELECT COUNT(*)
        FROM context_attachments
        WHERE media_id IN (
            SELECT id FROM media_artifacts WHERE content_hash = ?
        )
    `, contentHash)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ── In-memory test double ──────────────────────────────────────────────

// memMediaStore is an in-memory MediaStore useful for unit tests that
// don't want to spin up a SQLite DB. Files live in tempdir for parity
// with the SQL store; metadata in a map. Refcount sources still wire
// in.
type memMediaStore struct {
	mu       sync.RWMutex
	dataDir  string
	rows     map[string]MediaArtifact // id -> row
	now      func() time.Time
	idGen    func() (string, error)
	maxBytes int64

	hashMu    sync.Mutex
	hashLocks map[string]*sync.Mutex

	sourcesMu sync.RWMutex
	sources   []RefcountSource
}

// NewMemoryMediaStore returns a MediaStore backed by an in-memory map
// and a tempdir on disk. Useful for tests.
func NewMemoryMediaStore(dataDir string, opts ...MediaStoreOption) MediaStore {
	s := &memMediaStore{
		dataDir:  dataDir,
		rows:     map[string]MediaArtifact{},
		now:      func() time.Time { return time.Now().UTC() },
		idGen:    defaultMediaID,
		maxBytes: DefaultMaxBytes,
	}
	// MediaStoreOption is parameterized over *sqlMediaStore; reuse the
	// shape via a tiny shim so tests can pass the same option set.
	shim := &sqlMediaStore{
		now:      s.now,
		idGen:    s.idGen,
		maxBytes: s.maxBytes,
	}
	for _, opt := range opts {
		opt(shim)
	}
	s.now = shim.now
	s.idGen = shim.idGen
	s.maxBytes = shim.maxBytes
	return s
}

func (s *memMediaStore) RegisterRefcountSource(rs RefcountSource) {
	if rs == nil {
		return
	}
	s.sourcesMu.Lock()
	s.sources = append(s.sources, rs)
	s.sourcesMu.Unlock()
}

func (s *memMediaStore) Put(ctx context.Context, b []byte, mediaType, originalName string) (MediaArtifact, error) {
	if s.maxBytes > 0 && int64(len(b)) > s.maxBytes {
		return MediaArtifact{}, fmt.Errorf("%w: %d > %d", ErrOversize, len(b), s.maxBytes)
	}
	sum := sha256.Sum256(b)
	hashHex := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(filepath.Join(s.dataDir, "media"), 0o755); err != nil {
		return MediaArtifact{}, err
	}
	mu := s.memLockForHash(hashHex)
	mu.Lock()
	dest := filepath.Join(s.dataDir, "media", hashHex)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			mu.Unlock()
			return MediaArtifact{}, err
		}
	}
	mu.Unlock()

	id, err := s.idGen()
	if err != nil {
		return MediaArtifact{}, err
	}
	row := MediaArtifact{
		ID:           id,
		ContentHash:  hashHex,
		MediaType:    mediaType,
		ByteSize:     int64(len(b)),
		OriginalName: originalName,
		CreatedAt:    s.now(),
	}
	s.mu.Lock()
	s.rows[id] = row
	s.mu.Unlock()
	return row, nil
}

func (s *memMediaStore) Get(_ context.Context, id string) (MediaArtifact, []byte, error) {
	s.mu.RLock()
	row, ok := s.rows[id]
	s.mu.RUnlock()
	if !ok {
		return MediaArtifact{}, nil, ErrMediaNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.dataDir, "media", row.ContentHash))
	if err != nil {
		if os.IsNotExist(err) {
			return MediaArtifact{}, nil, fmt.Errorf("%w: %s", ErrCorruptedStore, row.ContentHash)
		}
		return MediaArtifact{}, nil, err
	}
	return row, b, nil
}

func (s *memMediaStore) GetByHash(_ context.Context, contentHash string) (MediaArtifact, []byte, error) {
	s.mu.RLock()
	matches := make([]MediaArtifact, 0)
	for _, r := range s.rows {
		if r.ContentHash == contentHash {
			matches = append(matches, r)
		}
	}
	s.mu.RUnlock()
	if len(matches) == 0 {
		return MediaArtifact{}, nil, ErrMediaNotFound
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	row := matches[0]
	b, err := os.ReadFile(filepath.Join(s.dataDir, "media", row.ContentHash))
	if err != nil {
		if os.IsNotExist(err) {
			return MediaArtifact{}, nil, fmt.Errorf("%w: %s", ErrCorruptedStore, row.ContentHash)
		}
		return MediaArtifact{}, nil, err
	}
	return row, b, nil
}

func (s *memMediaStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[id]; !ok {
		return ErrMediaNotFound
	}
	delete(s.rows, id)
	return nil
}

func (s *memMediaStore) List(_ context.Context, filter MediaFilter) ([]MediaArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MediaArtifact, 0, len(s.rows))
	for _, r := range s.rows {
		if filter.ContentHash != "" && r.ContentHash != filter.ContentHash {
			continue
		}
		if filter.MediaType != "" && r.MediaType != filter.MediaType {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memMediaStore) RefcountFor(ctx context.Context, contentHash string) (int, error) {
	s.mu.RLock()
	count := 0
	for _, r := range s.rows {
		if r.ContentHash == contentHash {
			count++
		}
	}
	s.mu.RUnlock()
	s.sourcesMu.RLock()
	srcs := append([]RefcountSource(nil), s.sources...)
	s.sourcesMu.RUnlock()
	for _, rs := range srcs {
		n, err := rs.Refcount(ctx, contentHash)
		if err != nil {
			return 0, err
		}
		count += n
	}
	return count, nil
}

func (s *memMediaStore) PruneOrphans(ctx context.Context) (int, error) {
	dir := filepath.Join(s.dataDir, "media")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !looksLikeContentHash(name) {
			continue
		}
		count, err := s.RefcountFor(ctx, name)
		if err != nil {
			return removed, err
		}
		if count > 0 {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (s *memMediaStore) memLockForHash(hashHex string) *sync.Mutex {
	s.hashMu.Lock()
	defer s.hashMu.Unlock()
	if s.hashLocks == nil {
		s.hashLocks = map[string]*sync.Mutex{}
	}
	mu, ok := s.hashLocks[hashHex]
	if !ok {
		mu = &sync.Mutex{}
		s.hashLocks[hashHex] = mu
	}
	return mu
}

// ── ID generator ───────────────────────────────────────────────────────

// defaultMediaID returns a 26-char Crockford-base32 ULID. We use a
// process-monotonic generator so two Puts in the same millisecond
// produce strictly increasing ids — useful for List ordering.
func defaultMediaID() (string, error) {
	return mediaULIDGen.Next()
}

var mediaULIDGen = newULIDGenerator()

// ulidGenerator is a tiny stdlib-only ULID generator. The shape mirrors
// core/event/internal/idgen but lives here to honour the package
// boundary (internal packages aren't reachable from core/attachments).
// Crockford-base32 alphabet, 48-bit timestamp + 80-bit random tail.
type ulidGenerator struct {
	mu     sync.Mutex
	lastMs uint64
	tail   [10]byte
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newULIDGenerator() *ulidGenerator { return &ulidGenerator{} }

func (g *ulidGenerator) Next() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	if ms < g.lastMs {
		ms = g.lastMs
	}
	if ms == g.lastMs {
		// Increment the tail in-place.
		for i := len(g.tail) - 1; i >= 0; i-- {
			g.tail[i]++
			if g.tail[i] != 0 {
				break
			}
			if i == 0 {
				return "", errors.New("attachments/media: ULID tail overflow")
			}
		}
	} else {
		var t [10]byte
		if _, err := rand.Read(t[:]); err != nil {
			return "", fmt.Errorf("attachments/media: rand: %w", err)
		}
		g.tail = t
		g.lastMs = ms
	}
	return encodeULID(g.lastMs, g.tail), nil
}

// encodeULID renders ms (48-bit) + tail (80-bit) as 26-char Crockford-base32.
func encodeULID(ms uint64, tail [10]byte) string {
	var raw [16]byte
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], tail[:])

	out := make([]byte, 26)
	out[0] = crockford[(raw[0]>>5)&0x07]
	out[1] = crockford[raw[0]&0x1f]
	out[2] = crockford[(raw[1]>>3)&0x1f]
	out[3] = crockford[((raw[1]&0x07)<<2)|((raw[2]>>6)&0x03)]
	out[4] = crockford[(raw[2]>>1)&0x1f]
	out[5] = crockford[((raw[2]&0x01)<<4)|((raw[3]>>4)&0x0f)]
	out[6] = crockford[((raw[3]&0x0f)<<1)|((raw[4]>>7)&0x01)]
	out[7] = crockford[(raw[4]>>2)&0x1f]
	out[8] = crockford[((raw[4]&0x03)<<3)|((raw[5]>>5)&0x07)]
	out[9] = crockford[raw[5]&0x1f]
	out[10] = crockford[(raw[6]>>3)&0x1f]
	out[11] = crockford[((raw[6]&0x07)<<2)|((raw[7]>>6)&0x03)]
	out[12] = crockford[(raw[7]>>1)&0x1f]
	out[13] = crockford[((raw[7]&0x01)<<4)|((raw[8]>>4)&0x0f)]
	out[14] = crockford[((raw[8]&0x0f)<<1)|((raw[9]>>7)&0x01)]
	out[15] = crockford[(raw[9]>>2)&0x1f]
	out[16] = crockford[((raw[9]&0x03)<<3)|((raw[10]>>5)&0x07)]
	out[17] = crockford[raw[10]&0x1f]
	out[18] = crockford[(raw[11]>>3)&0x1f]
	out[19] = crockford[((raw[11]&0x07)<<2)|((raw[12]>>6)&0x03)]
	out[20] = crockford[(raw[12]>>1)&0x1f]
	out[21] = crockford[((raw[12]&0x01)<<4)|((raw[13]>>4)&0x0f)]
	out[22] = crockford[((raw[13]&0x0f)<<1)|((raw[14]>>7)&0x01)]
	out[23] = crockford[(raw[14]>>2)&0x1f]
	out[24] = crockford[((raw[14]&0x03)<<3)|((raw[15]>>5)&0x07)]
	out[25] = crockford[raw[15]&0x1f]
	return string(out)
}

