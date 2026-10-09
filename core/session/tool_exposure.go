package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// Persistence for the session layer of tool exposure (migration
// sessions/0347-tool-exposure): the override layer in
// sessions.tool_exposure and the activated set in
// sessions.tool_activations, both JSON, both NULL when empty.

func encodeToolExposure(e toolexposure.Exposure) (any, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.IsZero() {
		return nil, nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("session: marshal tool_exposure: %w", err)
	}
	return string(b), nil
}

func decodeToolExposure(raw sql.NullString) (toolexposure.Exposure, error) {
	if !raw.Valid || raw.String == "" {
		return toolexposure.Exposure{}, nil
	}
	var e toolexposure.Exposure
	if err := json.Unmarshal([]byte(raw.String), &e); err != nil {
		return toolexposure.Exposure{}, fmt.Errorf("session: decode tool_exposure: %w", err)
	}
	return e, nil
}

func encodeToolActivations(as []toolexposure.Activation) (any, error) {
	if err := toolexposure.ValidateActivations(as); err != nil {
		return nil, err
	}
	if len(as) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(as)
	if err != nil {
		return nil, fmt.Errorf("session: marshal tool_activations: %w", err)
	}
	return string(b), nil
}

func decodeToolActivations(raw sql.NullString) ([]toolexposure.Activation, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var as []toolexposure.Activation
	if err := json.Unmarshal([]byte(raw.String), &as); err != nil {
		return nil, fmt.Errorf("session: decode tool_activations: %w", err)
	}
	return as, nil
}

func (s *sqlStore) SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error {
	arg, err := encodeToolExposure(e)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx, "UPDATE sessions SET tool_exposure = ? WHERE id = ?", arg, id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) GetToolExposure(ctx context.Context, id string) (toolexposure.Exposure, error) {
	var raw sql.NullString
	if err := s.db.Reader().QueryRow(ctx,
		"SELECT tool_exposure FROM sessions WHERE id = ?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolexposure.Exposure{}, ErrSessionNotFound
		}
		return toolexposure.Exposure{}, err
	}
	return decodeToolExposure(raw)
}

func (s *sqlStore) SetToolActivations(ctx context.Context, id string, as []toolexposure.Activation) error {
	arg, err := encodeToolActivations(as)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx WriteTx) error {
		res, err := tx.Exec(ctx, "UPDATE sessions SET tool_activations = ? WHERE id = ?", arg, id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) GetToolActivations(ctx context.Context, id string) ([]toolexposure.Activation, error) {
	var raw sql.NullString
	if err := s.db.Reader().QueryRow(ctx,
		"SELECT tool_activations FROM sessions WHERE id = ?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return decodeToolActivations(raw)
}

// memStore keeps the same validation and empty-means-cleared semantics
// as the SQL columns; it is not a substitute for the SQL round trip.

func (s *memStore) SetToolExposure(_ context.Context, id string, e toolexposure.Exposure) error {
	if err := e.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrSessionNotFound
	}
	if e.IsZero() {
		delete(s.toolExposure, id)
		return nil
	}
	s.toolExposure[id] = e.Clone()
	return nil
}

func (s *memStore) GetToolExposure(_ context.Context, id string) (toolexposure.Exposure, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.records[id]; !ok {
		return toolexposure.Exposure{}, ErrSessionNotFound
	}
	return s.toolExposure[id].Clone(), nil
}

func (s *memStore) SetToolActivations(_ context.Context, id string, as []toolexposure.Activation) error {
	if err := toolexposure.ValidateActivations(as); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrSessionNotFound
	}
	if len(as) == 0 {
		delete(s.toolActs, id)
		return nil
	}
	s.toolActs[id] = append([]toolexposure.Activation(nil), as...)
	return nil
}

func (s *memStore) GetToolActivations(_ context.Context, id string) ([]toolexposure.Activation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.records[id]; !ok {
		return nil, ErrSessionNotFound
	}
	return append([]toolexposure.Activation(nil), s.toolActs[id]...), nil
}

// SetToolExposure persists the session's tool-exposure override layer;
// a zero Exposure clears it.
func (m *Manager) SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error {
	return m.store.SetToolExposure(ctx, id, e)
}

// SetToolActivations replaces the session's activated tool set.
func (m *Manager) SetToolActivations(ctx context.Context, id string, as []toolexposure.Activation) error {
	return m.store.SetToolActivations(ctx, id, as)
}

// SessionToolExposure returns what the exposure resolver needs from the
// session: its project, override layer and activated set. *Manager
// satisfies toolexposure.SessionSource with it.
func (m *Manager) SessionToolExposure(ctx context.Context, id string) (toolexposure.SessionState, error) {
	r, err := m.store.Get(ctx, id)
	if err != nil {
		return toolexposure.SessionState{}, err
	}
	override, err := m.store.GetToolExposure(ctx, id)
	if err != nil {
		return toolexposure.SessionState{}, err
	}
	acts, err := m.store.GetToolActivations(ctx, id)
	if err != nil {
		return toolexposure.SessionState{}, err
	}
	st := toolexposure.SessionState{Override: override, Activations: acts}
	if r.ProjectID != nil {
		st.ProjectID = *r.ProjectID
	}
	return st, nil
}

var _ toolexposure.SessionSource = (*Manager)(nil)
