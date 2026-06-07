package custom

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// Ensure migrations package is used (DB interface uses migrations.WriteTx).
var _ migrations.WriteTx = nil

// Store persists probed capability matrices to SQLite via the session
// migration registry. Implements CapabilityStore.
type Store struct {
	db DB
}

// DB is the minimal database interface the Store needs. The concrete
// implementation uses the harness's libSQL connection via WriteTx /
// QueryRow.
type DB interface {
	WriteTx(ctx context.Context, fn func(tx migrations.WriteTx) error) error
	QueryRow(ctx context.Context, query string, args ...any) migrations.Row
}

// NewStore constructs a Store backed by db. The table must already exist
// (created by migration 0331 registered at boot via RegisterMigrations).
func NewStore(db DB) *Store {
	return &Store{db: db}
}

// Put upserts the capability matrix for the given endpoint.
func (s *Store) Put(ctx context.Context, m *CapabilityMatrix) error {
	if m == nil {
		return errors.New("custom-openai store: nil matrix")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("custom-openai store: marshal matrix: %w", err)
	}
	return s.db.WriteTx(ctx, func(tx migrations.WriteTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO custom_endpoint_capabilities (endpoint, probed_at, matrix_json)
			 VALUES (?, ?, ?)
			 ON CONFLICT(endpoint) DO UPDATE SET
			   probed_at   = excluded.probed_at,
			   matrix_json = excluded.matrix_json`,
			m.Endpoint,
			m.ProbedAt,
			string(payload),
		)
		return err
	})
}

// Get returns the stored capability matrix for the given endpoint URL,
// or nil if no probe has been stored. Implements CapabilityStore.
func (s *Store) Get(ctx context.Context, endpoint string) (*CapabilityMatrix, error) {
	row := s.db.QueryRow(ctx,
		`SELECT matrix_json FROM custom_endpoint_capabilities WHERE endpoint = ?`,
		endpoint,
	)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		// Some storage impls return a wrapped not-found; treat it as nil.
		return nil, nil
	}
	var m CapabilityMatrix
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return nil, fmt.Errorf("custom-openai store: parse matrix: %w", err)
	}
	return &m, nil
}

// Delete removes the stored matrix for the given endpoint.
func (s *Store) Delete(ctx context.Context, endpoint string) error {
	return s.db.WriteTx(ctx, func(tx migrations.WriteTx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM custom_endpoint_capabilities WHERE endpoint = ?`,
			endpoint,
		)
		return err
	})
}

// CapabilityTTL is the maximum age of a probed matrix before it is
// considered stale and a re-probe is triggered.
const CapabilityTTL = 7 * 24 * time.Hour

// IsStale reports whether the matrix's probed-at timestamp is older
// than CapabilityTTL.
func (m *CapabilityMatrix) IsStale(now time.Time) bool {
	if m == nil {
		return true
	}
	age := now.Unix() - m.ProbedAt
	return age < 0 || age >= int64(CapabilityTTL.Seconds())
}

// splitSQL splits multi-statement SQL on semicolons.
func splitSQL(src string) []string {
	var out []string
	var cur []byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ';' {
			s := trimSpace(string(cur))
			if s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, c)
	}
	if s := trimSpace(string(cur)); s != "" {
		out = append(out, s)
	}
	return out
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	end := len(s)
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}
