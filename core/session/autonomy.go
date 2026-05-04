package session

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
)

// autonomy.go — helpers shared between the memstore and sqlstore for the
// autonomy-dial-01KR3M2A WP02 persistence layer. The autonomy.Layer type
// owns its own JSON wire shape (autonomy.Layer.MarshalJSON/UnmarshalJSON);
// these helpers translate between that shape, the (level, overrides)
// pair stored on the Record struct, and the two SQLite columns
// (autonomy_level INTEGER, autonomy_overrides TEXT).
//
// Empty layer (Level=nil, no overrides) round-trips as both columns NULL
// so the upstream resolver sees the layer as "contributes nothing."

// autonomyLayerFromRecord rebuilds a Layer from the (level, overrides)
// pair stored on Record. A nil level + empty overrides yields an empty
// Layer, matching autonomy.Layer.IsZero().
func autonomyLayerFromRecord(level *autonomy.Tier, overrides map[autonomy.Knob]any) autonomy.Layer {
	out := autonomy.Layer{}
	if level != nil {
		t := *level
		out.Level = &t
	}
	if len(overrides) > 0 {
		out.Overrides = make(map[autonomy.Knob]any, len(overrides))
		for k, v := range overrides {
			out.Overrides[k] = v
		}
	}
	return out
}

// cloneAutonomyLayer returns the (level, overrides) pair the memstore
// persists on Record. nil pointers + nil maps mean "no value at this
// layer." A non-empty Overrides map is cloned so the caller's mutations
// after Set don't bleed into the stored copy.
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

// encodeAutonomySQL marshals a Layer into the (level, overrides) pair
// the SQL store writes to the autonomy_level + autonomy_overrides
// columns. Empty Level + empty Overrides yield two NULLs — the upstream
// resolver fold then treats this layer as "inherit." Non-nil Level
// encodes as the int Tier value; non-empty Overrides encode as the
// canonical wire-shape JSON object Layer's marshaller already produces
// (the {"level":..., "overrides":...} envelope is unwrapped here so the
// column only carries the inner overrides map).
func encodeAutonomySQL(layer autonomy.Layer) (any, any, error) {
	var levelArg any
	if layer.Level != nil {
		levelArg = int64(int(*layer.Level))
	}
	var overridesArg any
	if len(layer.Overrides) > 0 {
		// Use the autonomy package's own marshaller so the wire shape
		// stays in lockstep with the Layer type. We marshal the full
		// Layer and pluck the overrides field out so SQL stores only
		// the inner map (level lives in its own column).
		blob, err := layer.MarshalJSON()
		if err != nil {
			return nil, nil, fmt.Errorf("session: marshal autonomy layer: %w", err)
		}
		var envelope struct {
			Overrides json.RawMessage `json:"overrides"`
		}
		if err := json.Unmarshal(blob, &envelope); err != nil {
			return nil, nil, fmt.Errorf("session: unwrap autonomy overrides: %w", err)
		}
		overridesArg = string(envelope.Overrides)
	}
	return levelArg, overridesArg, nil
}

// decodeAutonomySQL is the inverse of encodeAutonomySQL: the (level,
// overrides) pair from the SQL columns become a Layer. Two NULLs yield
// the zero Layer.
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

// decodeAutonomyOverrides parses the inner overrides JSON map by
// hopping through autonomy.Layer's unmarshaller (the canonical wire
// shape parser). We synthesize the {"level":null, "overrides":...}
// envelope so the parser's field-by-field type coercion runs.
func decodeAutonomyOverrides(raw string) (map[autonomy.Knob]any, error) {
	envelope := []byte(`{"level":null,"overrides":` + raw + `}`)
	var l autonomy.Layer
	if err := l.UnmarshalJSON(envelope); err != nil {
		return nil, err
	}
	return l.Overrides, nil
}
