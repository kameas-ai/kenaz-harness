package trust

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// sqliteAnchorStore is the persisted [AnchorStore] backing the trust
// anchors table (migration bundle/700 — see migrations.go). It is a
// thin database/sql layer with no migration or connection-pool logic
// of its own; the caller is responsible for registering and applying
// migrations before constructing one (see [NewSQLiteAnchorStore]).
//
// UNIT-3 (bundle-download-and-verify-01PMZ909): closes spec §1.7 F-1 —
// engine.go previously hardcoded newMemAnchorStore() on every
// NewEngineWithEmitter call, so an anchor installed before a relaunch
// was unobservable on the next boot (confirmed by execution in
// research/corrections.md). This store round-trips through the
// harness's unified data.db.
type sqliteAnchorStore struct {
	db *sql.DB
}

// NewSQLiteAnchorStore wraps db (a stdlib *sql.DB opened against the
// harness's unified data.db, typically obtained via the storage.DB
// "SQL() *sql.DB" structural interface — see core/rpc/api.go's
// buildJournalWriter for the established pattern) as an [AnchorStore].
// Callers must have already registered and applied migrations.Registry
// with [RegisterMigrations] — this constructor performs no DDL.
func NewSQLiteAnchorStore(db *sql.DB) (AnchorStore, error) {
	if db == nil {
		return nil, fmt.Errorf("trust: NewSQLiteAnchorStore: nil db")
	}
	return &sqliteAnchorStore{db: db}, nil
}

// anchorRow mirrors the trust_anchors table shape.
type anchorRow struct {
	anchorID              string
	kind                  int
	orgID                 string
	peerID                string
	algorithm             string
	installedAt           int64
	installedBy           string
	metadata              string
	pubKeyAlgorithm       string
	pubKeyBytes           []byte
	pubKeyFingerprint     string
	prevKeyAlgorithm      sql.NullString
	prevKeyBytes          []byte
	prevKeyFingerprint    sql.NullString
	overlapEndsUnixMillis sql.NullInt64
	removed               bool
}

func anchorToRow(a Anchor) (anchorRow, error) {
	metaJSON := "{}"
	if len(a.Metadata) > 0 {
		b, err := json.Marshal(a.Metadata)
		if err != nil {
			return anchorRow{}, fmt.Errorf("trust: marshal anchor metadata: %w", err)
		}
		metaJSON = string(b)
	}
	installedAt := a.InstalledAt
	if installedAt.IsZero() {
		installedAt = time.Now().UTC()
	}
	r := anchorRow{
		anchorID:          a.AnchorID,
		kind:              int(a.Kind),
		orgID:             a.OrgID,
		peerID:            a.PeerID,
		algorithm:         string(a.Algorithm),
		installedAt:       installedAt.UnixMilli(),
		installedBy:       a.InstalledBy,
		metadata:          metaJSON,
		pubKeyAlgorithm:   string(a.PublicKey.Algorithm),
		pubKeyBytes:       append([]byte(nil), a.PublicKey.Bytes...),
		pubKeyFingerprint: a.PublicKey.Fingerprint,
		removed:           a.Removed,
	}
	if a.PreviousKey != nil {
		r.prevKeyAlgorithm = sql.NullString{String: string(a.PreviousKey.Algorithm), Valid: true}
		r.prevKeyBytes = append([]byte(nil), a.PreviousKey.Bytes...)
		r.prevKeyFingerprint = sql.NullString{String: a.PreviousKey.Fingerprint, Valid: true}
	}
	if a.OverlapEnds != nil {
		r.overlapEndsUnixMillis = sql.NullInt64{Int64: a.OverlapEnds.UnixMilli(), Valid: true}
	}
	return r, nil
}

func rowToAnchor(r anchorRow) (Anchor, error) {
	var meta map[string]string
	if r.metadata != "" && r.metadata != "{}" {
		if err := json.Unmarshal([]byte(r.metadata), &meta); err != nil {
			return Anchor{}, fmt.Errorf("trust: unmarshal anchor metadata: %w", err)
		}
	}
	a := Anchor{
		AnchorID:    r.anchorID,
		Kind:        AnchorKind(r.kind),
		OrgID:       r.orgID,
		PeerID:      r.peerID,
		Algorithm:   Algorithm(r.algorithm),
		InstalledAt: time.UnixMilli(r.installedAt).UTC(),
		InstalledBy: r.installedBy,
		Metadata:    meta,
		PublicKey: PublicKey{
			Algorithm:   Algorithm(r.pubKeyAlgorithm),
			Bytes:       append([]byte(nil), r.pubKeyBytes...),
			Fingerprint: r.pubKeyFingerprint,
		},
		Removed: r.removed,
	}
	if r.prevKeyFingerprint.Valid {
		a.PreviousKey = &PublicKey{
			Algorithm:   Algorithm(r.prevKeyAlgorithm.String),
			Bytes:       append([]byte(nil), r.prevKeyBytes...),
			Fingerprint: r.prevKeyFingerprint.String,
		}
	}
	if r.overlapEndsUnixMillis.Valid {
		t := time.UnixMilli(r.overlapEndsUnixMillis.Int64).UTC()
		a.OverlapEnds = &t
	}
	return a, nil
}

const anchorSelectColumns = `anchor_id, kind, org_id, peer_id, algorithm, installed_at, installed_by,
	metadata, pubkey_algorithm, pubkey_bytes, pubkey_fingerprint,
	prevkey_algorithm, prevkey_bytes, prevkey_fingerprint, overlap_ends, removed`

func scanAnchorRow(scan func(dest ...any) error) (anchorRow, error) {
	var r anchorRow
	var removedInt int
	if err := scan(
		&r.anchorID, &r.kind, &r.orgID, &r.peerID, &r.algorithm, &r.installedAt, &r.installedBy,
		&r.metadata, &r.pubKeyAlgorithm, &r.pubKeyBytes, &r.pubKeyFingerprint,
		&r.prevKeyAlgorithm, &r.prevKeyBytes, &r.prevKeyFingerprint, &r.overlapEndsUnixMillis, &removedInt,
	); err != nil {
		return anchorRow{}, err
	}
	r.removed = removedInt != 0
	return r, nil
}

// Get implements [AnchorStore].
func (s *sqliteAnchorStore) Get(ctx context.Context, anchorID string) (Anchor, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+anchorSelectColumns+` FROM trust_anchors WHERE anchor_id = ?`, anchorID)
	r, err := scanAnchorRow(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Anchor{}, ErrAnchorNotFound
		}
		return Anchor{}, fmt.Errorf("trust: get anchor: %w", err)
	}
	return rowToAnchor(r)
}

// FindByKeyID implements [AnchorStore]. Matches either the current or
// the previous public-key fingerprint — mirrors memAnchorStore's
// byKeyID index, which maps both to the owning anchor so rotation
// overlap (verify.go step 8) can resolve a signature made with the
// previous key.
func (s *sqliteAnchorStore) FindByKeyID(ctx context.Context, keyID string) (Anchor, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+anchorSelectColumns+` FROM trust_anchors
		 WHERE pubkey_fingerprint = ? OR prevkey_fingerprint = ?
		 LIMIT 1`, keyID, keyID)
	r, err := scanAnchorRow(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Anchor{}, ErrAnchorNotFound
		}
		return Anchor{}, fmt.Errorf("trust: find anchor by key id: %w", err)
	}
	return rowToAnchor(r)
}

// Install implements [AnchorStore]. Preserves memAnchorStore's
// identity-collision semantics: a *live* anchor already bound to the
// same PeerID with a different fingerprint refuses the write.
func (s *sqliteAnchorStore) Install(ctx context.Context, a Anchor) error {
	if a.PeerID != "" {
		rows, err := s.db.QueryContext(ctx,
			`SELECT pubkey_fingerprint FROM trust_anchors
			 WHERE peer_id = ? AND removed = 0 AND anchor_id != ?`, a.PeerID, a.AnchorID)
		if err != nil {
			return fmt.Errorf("trust: install anchor: collision check: %w", err)
		}
		var existingFPs []string
		for rows.Next() {
			var fp string
			if err := rows.Scan(&fp); err != nil {
				_ = rows.Close()
				return fmt.Errorf("trust: install anchor: scan collision row: %w", err)
			}
			existingFPs = append(existingFPs, fp)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("trust: install anchor: collision rows: %w", err)
		}
		_ = rows.Close()
		for _, fp := range existingFPs {
			if fp != a.PublicKey.Fingerprint {
				return ErrIdentityCollision
			}
		}
	}

	r, err := anchorToRow(a)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO trust_anchors (
			anchor_id, kind, org_id, peer_id, algorithm, installed_at, installed_by,
			metadata, pubkey_algorithm, pubkey_bytes, pubkey_fingerprint,
			prevkey_algorithm, prevkey_bytes, prevkey_fingerprint, overlap_ends, removed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(anchor_id) DO UPDATE SET
			kind = excluded.kind,
			org_id = excluded.org_id,
			peer_id = excluded.peer_id,
			algorithm = excluded.algorithm,
			installed_at = excluded.installed_at,
			installed_by = excluded.installed_by,
			metadata = excluded.metadata,
			pubkey_algorithm = excluded.pubkey_algorithm,
			pubkey_bytes = excluded.pubkey_bytes,
			pubkey_fingerprint = excluded.pubkey_fingerprint,
			prevkey_algorithm = excluded.prevkey_algorithm,
			prevkey_bytes = excluded.prevkey_bytes,
			prevkey_fingerprint = excluded.prevkey_fingerprint,
			overlap_ends = excluded.overlap_ends,
			removed = 0
	`,
		r.anchorID, r.kind, r.orgID, r.peerID, r.algorithm, r.installedAt, r.installedBy,
		r.metadata, r.pubKeyAlgorithm, r.pubKeyBytes, r.pubKeyFingerprint,
		r.prevKeyAlgorithm, r.prevKeyBytes, r.prevKeyFingerprint, r.overlapEndsUnixMillis,
	)
	if err != nil {
		return fmt.Errorf("trust: install anchor: %w", err)
	}
	return nil
}

// Tombstone implements [AnchorStore]. The row survives with removed=1
// so a subsequent FindByKeyID still resolves it — verify.go step 4
// distinguishes RejAnchorRemoved from RejAnchorMissing on exactly this
// signal.
func (s *sqliteAnchorStore) Tombstone(ctx context.Context, anchorID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE trust_anchors SET removed = 1 WHERE anchor_id = ?`, anchorID)
	if err != nil {
		return fmt.Errorf("trust: tombstone anchor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("trust: tombstone anchor: rows affected: %w", err)
	}
	if n == 0 {
		return ErrAnchorNotFound
	}
	return nil
}

// Update implements [AnchorStore]. Used by BeginRotation /
// CompleteRotation to mutate PreviousKey / OverlapEnds.
func (s *sqliteAnchorStore) Update(ctx context.Context, a Anchor) error {
	r, err := anchorToRow(a)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE trust_anchors SET
			kind = ?, org_id = ?, peer_id = ?, algorithm = ?, installed_at = ?, installed_by = ?,
			metadata = ?, pubkey_algorithm = ?, pubkey_bytes = ?, pubkey_fingerprint = ?,
			prevkey_algorithm = ?, prevkey_bytes = ?, prevkey_fingerprint = ?, overlap_ends = ?, removed = ?
		WHERE anchor_id = ?
	`,
		r.kind, r.orgID, r.peerID, r.algorithm, r.installedAt, r.installedBy,
		r.metadata, r.pubKeyAlgorithm, r.pubKeyBytes, r.pubKeyFingerprint,
		r.prevKeyAlgorithm, r.prevKeyBytes, r.prevKeyFingerprint, r.overlapEndsUnixMillis,
		boolToInt(r.removed), r.anchorID,
	)
	if err != nil {
		return fmt.Errorf("trust: update anchor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("trust: update anchor: rows affected: %w", err)
	}
	if n == 0 {
		return ErrAnchorNotFound
	}
	return nil
}

// List implements [AnchorStore]. Returns every live (removed=0) anchor,
// sorted by AnchorID for a stable order (matches memAnchorStore).
func (s *sqliteAnchorStore) List(ctx context.Context) ([]Anchor, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+anchorSelectColumns+` FROM trust_anchors WHERE removed = 0`)
	if err != nil {
		return nil, fmt.Errorf("trust: list anchors: %w", err)
	}
	defer rows.Close()
	var out []Anchor
	for rows.Next() {
		r, err := scanAnchorRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("trust: list anchors: scan: %w", err)
		}
		a, err := rowToAnchor(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trust: list anchors: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AnchorID < out[j].AnchorID })
	return out, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Compile-time assertion: *sqliteAnchorStore satisfies AnchorStore.
var _ AnchorStore = (*sqliteAnchorStore)(nil)
