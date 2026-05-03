package autonomy

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Layer is one rung in the global → project → session override hierarchy.
//
//   - Level == nil means "do not contribute a tier preset at this layer."
//   - Level != nil means "use this tier's preset for any knob this layer
//     does not explicitly override."
//   - Overrides holds knob-specific values that win over Level at the same
//     layer.
//
// The wire shape is stable: an empty Overrides map serialises as `{}`, not
// omitted. A nil Level serialises as JSON null. This means the parser can
// always treat the field as present.
type Layer struct {
	Level     *Tier
	Overrides map[Knob]any
}

// DefaultLayer returns Level=&TierDefault with an empty (non-nil) overrides
// map. Useful as a fallback when no layer state is available.
func DefaultLayer() Layer {
	t := TierDefault
	return Layer{
		Level:     &t,
		Overrides: map[Knob]any{},
	}
}

// IsZero reports whether the layer contributes nothing to resolution: nil
// Level and no overrides.
func (l Layer) IsZero() bool {
	return l.Level == nil && len(l.Overrides) == 0
}

// jsonLayer is the on-the-wire shape: level is a tier name string or null,
// overrides is always present (possibly empty).
type jsonLayer struct {
	Level     *string         `json:"level"`
	Overrides json.RawMessage `json:"overrides"`
}

// MarshalJSON emits the canonical wire shape. Empty overrides serialise as
// `{}` so the parser doesn't have to distinguish "missing" from "empty."
func (l Layer) MarshalJSON() ([]byte, error) {
	out := jsonLayer{}
	if l.Level != nil {
		s := l.Level.String()
		out.Level = &s
	}
	overridesJSON, err := marshalOverrides(l.Overrides)
	if err != nil {
		return nil, err
	}
	out.Overrides = overridesJSON
	return json.Marshal(out)
}

// UnmarshalJSON parses the canonical wire shape. Missing or null `overrides`
// is tolerated and produces an empty (non-nil) map.
func (l *Layer) UnmarshalJSON(data []byte) error {
	var raw jsonLayer
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := Layer{Overrides: map[Knob]any{}}
	if raw.Level != nil {
		t, err := ParseTier(*raw.Level)
		if err != nil {
			return err
		}
		out.Level = &t
	}
	if len(bytes.TrimSpace(raw.Overrides)) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Overrides), []byte("null")) {
		ov, err := unmarshalOverrides(raw.Overrides)
		if err != nil {
			return err
		}
		out.Overrides = ov
	}
	*l = out
	return nil
}

// marshalOverrides serialises an overrides map. Knob values are stored as
// their concrete typed values (int, AskMode, FamilySet, etc.) and the typed
// MarshalJSON methods make them round-trip stable.
func marshalOverrides(in map[Knob]any) ([]byte, error) {
	if in == nil {
		return []byte("{}"), nil
	}
	stringly := make(map[string]any, len(in))
	for k, v := range in {
		stringly[string(k)] = v
	}
	return json.Marshal(stringly)
}

// unmarshalOverrides decodes the overrides map, coercing each knob's JSON
// value into its declared concrete type. Unknown knobs return an error so
// silent typos don't get persisted as no-ops.
func unmarshalOverrides(data []byte) (map[Knob]any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[Knob]any, len(raw))
	for k, v := range raw {
		knob := Knob(k)
		val, err := decodeKnobValue(knob, v)
		if err != nil {
			return nil, fmt.Errorf("autonomy: override %q: %w", k, err)
		}
		out[knob] = val
	}
	return out, nil
}

// decodeKnobValue parses a single knob's JSON value into its concrete type.
func decodeKnobValue(k Knob, raw json.RawMessage) (any, error) {
	switch k {
	case KnobMaxIterations, KnobTokenCeilingPerTurn:
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return n, nil
	case KnobAskOnAmbiguity:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return AskMode(s), nil
	case KnobRecapStyle:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return RecapMode(s), nil
	case KnobContinueOnError:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return ErrorMode(s), nil
	case KnobDestructiveActionPosture:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return DestructivePosture(s), nil
	case KnobAutoApproveFamilies:
		var fs FamilySet
		if err := json.Unmarshal(raw, &fs); err != nil {
			return nil, err
		}
		if fs == nil {
			fs = NewFamilySet()
		}
		return fs, nil
	default:
		return nil, fmt.Errorf("unknown knob %q", string(k))
	}
}
