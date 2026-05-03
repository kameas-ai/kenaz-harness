package projects

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
)

// autonomy.go — helpers shared between memstore and sqlstore for the
// autonomy-dial-01KR3M2A WP02 persistence layer at the project tier.
// Mirrors core/session/autonomy.go's translation logic between
// autonomy.Layer and the (level INTEGER, overrides TEXT) column pair
// added by migration 0316.

func cloneAutonomyLayer(layer autonomy.Layer) (*autonomy.Tier, map[autonomy.Knob]any) {
	var levelOut *autonomy.Tier
	if layer.Level != nil {
		t := *layer.Level
		levelOut = &t
	}
	var overridesOut map[autonomy.Knob]any
	if len(layer.Overrides) > 0 {
		overridesOut = make(map[autonomy.Knob]any, len(layer.Overrides))
		for k, v := range layer.Overrides {
			overridesOut[k] = v
		}
	}
	return levelOut, overridesOut
}

func autonomyLayerFromProject(p Project) autonomy.Layer {
	out := autonomy.Layer{}
	if p.AutonomyLevel != nil {
		t := *p.AutonomyLevel
		out.Level = &t
	}
	if len(p.AutonomyOverrides) > 0 {
		out.Overrides = make(map[autonomy.Knob]any, len(p.AutonomyOverrides))
		for k, v := range p.AutonomyOverrides {
			out.Overrides[k] = v
		}
	}
	return out
}

func encodeAutonomySQL(layer autonomy.Layer) (any, any, error) {
	var levelArg any
	if layer.Level != nil {
		levelArg = int64(int(*layer.Level))
	}
	var overridesArg any
	if len(layer.Overrides) > 0 {
		blob, err := layer.MarshalJSON()
		if err != nil {
			return nil, nil, fmt.Errorf("projects: marshal autonomy layer: %w", err)
		}
		var envelope struct {
			Overrides json.RawMessage `json:"overrides"`
		}
		if err := json.Unmarshal(blob, &envelope); err != nil {
			return nil, nil, fmt.Errorf("projects: unwrap autonomy overrides: %w", err)
		}
		overridesArg = string(envelope.Overrides)
	}
	return levelArg, overridesArg, nil
}

func decodeAutonomySQL(level sql.NullInt64, overrides sql.NullString) (autonomy.Layer, error) {
	out := autonomy.Layer{}
	if level.Valid {
		t := autonomy.Tier(int(level.Int64))
		out.Level = &t
	}
	if overrides.Valid && overrides.String != "" {
		ov, err := decodeAutonomyOverrides(overrides.String)
		if err != nil {
			return autonomy.Layer{}, err
		}
		out.Overrides = ov
	}
	return out, nil
}

func decodeAutonomyOverrides(raw string) (map[autonomy.Knob]any, error) {
	envelope := []byte(`{"level":null,"overrides":` + raw + `}`)
	var l autonomy.Layer
	if err := l.UnmarshalJSON(envelope); err != nil {
		return nil, err
	}
	return l.Overrides, nil
}
