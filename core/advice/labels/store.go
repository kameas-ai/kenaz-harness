package labels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// UserAction is the spec §4 enum for how a captured recommendation was
// ultimately acted on. The zero value is intentionally invalid — every
// caller of Store.Insert must state one explicitly (mirrors
// advice.SafetyClass's "no safe default for unspecified" contract).
type UserAction string

const (
	ActionAccepted  UserAction = "accepted"
	ActionDismissed UserAction = "dismissed"
	ActionIgnored   UserAction = "ignored"
	ActionAutoActed UserAction = "auto_acted"
)

// Valid reports whether a is one of the four known actions.
func (a UserAction) Valid() bool {
	switch a {
	case ActionAccepted, ActionDismissed, ActionIgnored, ActionAutoActed:
		return true
	default:
		return false
	}
}

// Row is one advice_labels row — spec §4's schema plus design §5.2's
// propensity field (Shown). FeaturesJSON is the raw marshaled feature
// vector (advice.Features run through encoding/json — the same bytes
// advice.FeaturesHash hashes), stored as text so the corpus is queryable
// without a second schema per kind.
type Row struct {
	KindID        string
	PromptVersion string
	FeaturesHash  string
	FeaturesJSON  string
	ModelID       string
	Rung          string
	Decision      bool
	Confidence    int
	// Shown is design §5.2's propensity field: whether the ≥75 chip gate
	// actually rendered this recommendation to the user (spec §2d).
	// false for every below-threshold or decision=false recommendation —
	// captured anyway, per spec: "the filter shapes UX, not the training
	// corpus."
	Shown bool
	// FeaturesComplete is the placeholder-row discriminator (review
	// promotion, laya-advisors-01LAYA001 WP07/WP08 review round,
	// 2026-09-29): true unless the kind that produced Features reported
	// (via advice.FeaturesCompleteness) that it was extracted from
	// placeholder/zero-value data rather than a real wired source. A
	// caller that constructs a Row directly (rather than through
	// CaptureAdvisor.Recommend, which always sets this field explicitly)
	// MUST set it too — Store deliberately does not default an unset bool
	// here the way it defaults UserAction/CreatedAt below, because "did
	// the caller forget to set this" and "the caller explicitly means
	// false" are indistinguishable for a bool, and silently defaulting to
	// the SAFE value (true) would hide exactly the bug this field exists
	// to catch.
	FeaturesComplete bool
	UserAction       UserAction
	LatencyMS        int64
	SessionID        string
	CreatedAt        time.Time
}

// ErrCaptureDisabled is returned by Store methods that choose to no-op
// rather than write when a caller forgot to gate on the capture toggle
// itself. Not used by SQLStore (which always writes when called —
// gating is CaptureAdvisor's job, see capture.go) but kept as a shared
// sentinel other Store implementations may want.
var ErrCaptureDisabled = errors.New("labels: capture disabled")

// Store is the label-capture persistence port. Insert lands one row per
// captured recommendation; UpdateAction updates the most recent
// matching row's UserAction when the user (or an auto-act call site)
// later acts on it. Both are plain methods (not gated internally) —
// AC-06's "capture toggle OFF => zero rows written, call-count proof"
// requires that the toggle be enforced by NEVER CALLING these methods,
// not by a no-op body (see capture.go's CaptureAdvisor, which is the
// only production caller and holds the gate).
type Store interface {
	Insert(ctx context.Context, row Row) error
	// UpdateAction sets the UserAction column on the most recently
	// inserted row matching (sessionID, kindID, featuresHash) — the SAME
	// three-part key Recommend's own session cache uses (cache.go's
	// cacheKey), so an action always lands on the exact recommendation
	// row it corresponds to. A no-op (not an error) when no matching row
	// exists — e.g. a Dismiss call for a recommendation whose Insert was
	// itself skipped because capture was off at Recommend time.
	UpdateAction(ctx context.Context, sessionID, kindID, featuresHash string, action UserAction) error
}

// SQLStore implements Store against a *sql.DB opened with the
// modernc.org/sqlite driver, mirroring core/tasks's sqliteStore pattern.
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore wraps an open *sql.DB. The caller is responsible for
// ensuring the advice_labels table exists — the harness migration
// framework runs laya-advisors/1600-advice-labels at boot as long as
// RegisterMigrations was called before storage.Open (see
// core/storage/sqlite/sqlite.go).
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

// compile-time witness that *SQLStore satisfies Store.
var _ Store = (*SQLStore)(nil)

func (s *SQLStore) Insert(ctx context.Context, row Row) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("labels: nil store")
	}
	action := row.UserAction
	if action == "" {
		action = ActionIgnored
	}
	if !action.Valid() {
		return fmt.Errorf("labels: invalid user_action %q", action)
	}
	featuresJSON := row.FeaturesJSON
	if featuresJSON == "" {
		featuresJSON = "{}"
	}
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO advice_labels
		    (kind, prompt_version, features_hash, features_json, features_complete, model_id, rung,
		     decision, confidence, shown, user_action, latency_ms, session_id, created_at)
		VALUES
		    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.KindID, row.PromptVersion, row.FeaturesHash, featuresJSON, boolToInt(row.FeaturesComplete), row.ModelID, row.Rung,
		boolToInt(row.Decision), row.Confidence, boolToInt(row.Shown), string(action),
		row.LatencyMS, row.SessionID, createdAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("labels: insert: %w", err)
	}
	return nil
}

func (s *SQLStore) UpdateAction(ctx context.Context, sessionID, kindID, featuresHash string, action UserAction) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("labels: nil store")
	}
	if !action.Valid() {
		return fmt.Errorf("labels: invalid user_action %q", action)
	}
	// The most recent matching row is the one this action applies to —
	// a session may accumulate several rows for the same
	// (session, kind, features_hash) key only across distinct,
	// non-cache-hit Recommend calls (capture.go skips cache hits), so
	// "most recent" is unambiguous in the overwhelming common case and
	// the safest available tie-break otherwise.
	_, err := s.db.ExecContext(ctx, `
		UPDATE advice_labels SET user_action = ?
		WHERE id = (
		    SELECT id FROM advice_labels
		    WHERE session_id = ? AND kind = ? AND features_hash = ?
		    ORDER BY id DESC LIMIT 1
		)`,
		string(action), sessionID, kindID, featuresHash,
	)
	if err != nil {
		return fmt.Errorf("labels: update action: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// MarshalFeatures is a small helper capture.go uses to derive
// FeaturesJSON from an advice.Features value — kept here (rather than
// inlined) so a test can assert the marshaled shape directly. Mirrors
// advice.FeaturesHash's own "hash the marshaled bytes" contract, using
// the SAME json.Marshal call so FeaturesJSON and the hash advice already
// computed are always derived from identical bytes.
func MarshalFeatures(features any) (string, error) {
	b, err := json.Marshal(features)
	if err != nil {
		return "", fmt.Errorf("labels: marshal features: %w", err)
	}
	return string(b), nil
}
