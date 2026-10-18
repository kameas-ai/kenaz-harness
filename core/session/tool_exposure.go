package session

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// Persistence for the session layer of tool exposure (migration
// sessions/0347-tool-exposure): the override layer in
// sessions.tool_exposure and the activated set in
// sessions.tool_activations, both JSON via toolexposure's column codec,
// both NULL when empty. Validation happens once, in Manager.

func (s *sqlStore) SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error {
	arg, err := toolexposure.MarshalExposureColumn(e)
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

func (s *sqlStore) SetToolActivations(ctx context.Context, id string, as []toolexposure.Activation) error {
	arg, err := toolexposure.MarshalActivationsColumn(as)
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

func (s *sqlStore) ToolExposureState(ctx context.Context, id string) (toolexposure.SessionState, error) {
	var project, exposure, acts sql.NullString
	if err := s.db.Reader().QueryRow(ctx,
		"SELECT project_id, tool_exposure, tool_activations FROM sessions WHERE id = ?", id).
		Scan(&project, &exposure, &acts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return toolexposure.SessionState{}, ErrSessionNotFound
		}
		return toolexposure.SessionState{}, err
	}
	e, err := toolexposure.ParseExposureColumn(exposure)
	if err != nil {
		return toolexposure.SessionState{}, err
	}
	as, err := toolexposure.ParseActivationsColumn(acts)
	if err != nil {
		return toolexposure.SessionState{}, err
	}
	return toolexposure.SessionState{ProjectID: project.String, Override: e, Activations: as}, nil
}

// memStore keeps the SQL columns' empty-means-cleared semantics; it is
// not a substitute for the SQL round trip.

func (s *memStore) SetToolExposure(_ context.Context, id string, e toolexposure.Exposure) error {
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

func (s *memStore) SetToolActivations(_ context.Context, id string, as []toolexposure.Activation) error {
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

func (s *memStore) ToolExposureState(_ context.Context, id string) (toolexposure.SessionState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	if !ok {
		return toolexposure.SessionState{}, ErrSessionNotFound
	}
	st := toolexposure.SessionState{Override: s.toolExposure[id].Clone()}
	if acts := s.toolActs[id]; len(acts) > 0 {
		st.Activations = append([]toolexposure.Activation(nil), acts...)
	}
	if r.ProjectID != nil {
		st.ProjectID = *r.ProjectID
	}
	return st, nil
}

// SetToolExposure validates and persists the session's tool-exposure
// override layer; a zero Exposure clears it.
func (m *Manager) SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error {
	if err := e.Validate(); err != nil {
		return err
	}
	return m.store.SetToolExposure(ctx, id, e)
}

// SetToolActivations validates and replaces the session's activated
// tool set; an empty set clears it.
func (m *Manager) SetToolActivations(ctx context.Context, id string, as []toolexposure.Activation) error {
	if err := toolexposure.ValidateActivations(as); err != nil {
		return err
	}
	return m.store.SetToolActivations(ctx, id, as)
}

// InheritToolExposure copies the parent session's tool-exposure state
// onto a newly forked child (spec Q-F): the session override layer and
// the activated set.
//
// The override is a consent surface — a tool the user turned off for the
// parent must stay off in the child — so a failed override copy fails
// the call. A failed activations copy only costs the child some loaded
// schemas: it is logged and the call succeeds.
//
// The child counts its own turns from zero, so each copied activation is
// stamped LastUsedTurn 0: a non-sticky inherited activation gets a full
// TTL in the child. Sticky stays sticky.
func (m *Manager) InheritToolExposure(ctx context.Context, parentID, childID string) error {
	st, err := m.store.ToolExposureState(ctx, parentID)
	if err != nil {
		return err
	}
	if !st.Override.IsZero() {
		if err := m.SetToolExposure(ctx, childID, st.Override.Clone()); err != nil {
			return err
		}
	}
	if len(st.Activations) == 0 {
		return nil
	}
	out := make([]toolexposure.Activation, len(st.Activations))
	for i, a := range st.Activations {
		a.LastUsedTurn = 0
		out[i] = a
	}
	if err := m.SetToolActivations(ctx, childID, out); err != nil {
		logging.L().Warn("session.fork.inherit_activations_failed",
			"parent_session_id", parentID, "child_session_id", childID, "err", err.Error())
	}
	return nil
}

// SessionToolExposure returns what the exposure resolver needs from the
// session in one read: its project, override layer and activated set.
// *Manager satisfies toolexposure.SessionSource with it.
func (m *Manager) SessionToolExposure(ctx context.Context, id string) (toolexposure.SessionState, error) {
	return m.store.ToolExposureState(ctx, id)
}

var _ toolexposure.SessionSource = (*Manager)(nil)
