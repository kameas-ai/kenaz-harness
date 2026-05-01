package autonomy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultLayer(t *testing.T) {
	l := DefaultLayer()
	if l.Level == nil || *l.Level != TierDefault {
		t.Errorf("DefaultLayer().Level = %v, want &TierDefault", l.Level)
	}
	if l.Overrides == nil {
		t.Error("DefaultLayer().Overrides should be non-nil empty map")
	}
	if len(l.Overrides) != 0 {
		t.Errorf("DefaultLayer().Overrides len = %d, want 0", len(l.Overrides))
	}
}

func TestLayerIsZero(t *testing.T) {
	cases := []struct {
		name  string
		layer Layer
		want  bool
	}{
		{"nil-level-nil-overrides", Layer{}, true},
		{"nil-level-empty-overrides", Layer{Overrides: map[Knob]any{}}, true},
		{"level-set", func() Layer {
			t := TierBold
			return Layer{Level: &t}
		}(), false},
		{"override-set", Layer{Overrides: map[Knob]any{KnobMaxIterations: 10}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.layer.IsZero(); got != c.want {
				t.Errorf("IsZero = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLayerJSONRoundTrip(t *testing.T) {
	tier := TierBold
	cases := []struct {
		name      string
		layer     Layer
		wantJSON  string // exact match for stability
		wantBack  Layer
		stableKey bool
	}{
		{
			name:     "nil-level-empty-overrides",
			layer:    Layer{Overrides: map[Knob]any{}},
			wantJSON: `{"level":null,"overrides":{}}`,
			wantBack: Layer{Overrides: map[Knob]any{}},
		},
		{
			name:     "default-via-helper",
			layer:    DefaultLayer(),
			wantJSON: `{"level":"default","overrides":{}}`,
			wantBack: DefaultLayer(),
		},
		{
			name: "level-and-sparse-overrides",
			layer: Layer{
				Level: &tier,
				Overrides: map[Knob]any{
					KnobMaxIterations: 75,
				},
			},
			wantJSON: `{"level":"bold","overrides":{"maxIterations":75}}`,
			wantBack: Layer{
				Level:     &tier,
				Overrides: map[Knob]any{KnobMaxIterations: 75},
			},
			stableKey: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.layer)
			if err != nil {
				t.Fatalf("Marshal err = %v", err)
			}
			if string(got) != c.wantJSON {
				t.Errorf("Marshal:\n  got:  %s\n  want: %s", got, c.wantJSON)
			}

			var back Layer
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("Unmarshal err = %v", err)
			}
			if !layersEqual(back, c.wantBack) {
				t.Errorf("round-trip:\n  got:  %#v\n  want: %#v", back, c.wantBack)
			}
		})
	}
}

func TestLayerJSONFamilySetOverride(t *testing.T) {
	// FamilySet override values must round-trip as a sorted JSON array.
	tier := TierDefault
	in := Layer{
		Level: &tier,
		Overrides: map[Knob]any{
			KnobAutoApproveFamilies: NewFamilySet(FamilyWrite, FamilyRead),
		},
	}
	got, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal err = %v", err)
	}
	want := `{"level":"default","overrides":{"autoApproveFamilies":["read","write"]}}`
	if string(got) != want {
		t.Errorf("Marshal:\n  got:  %s\n  want: %s", got, want)
	}

	var back Layer
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal err = %v", err)
	}
	fs, ok := back.Overrides[KnobAutoApproveFamilies].(FamilySet)
	if !ok {
		t.Fatalf("override type = %T, want FamilySet", back.Overrides[KnobAutoApproveFamilies])
	}
	if !fs.Equal(NewFamilySet(FamilyRead, FamilyWrite)) {
		t.Errorf("round-tripped FamilySet = %v", fs.Sorted())
	}
}

func TestLayerJSONAllKnobOverrides(t *testing.T) {
	// Every knob must round-trip with its declared concrete type intact.
	in := Layer{
		Overrides: map[Knob]any{
			KnobMaxIterations:            42,
			KnobAskOnAmbiguity:           AskHard,
			KnobAutoApproveFamilies:      NewFamilySet(FamilyRead),
			KnobTokenCeilingPerTurn:      9999,
			KnobRecapStyle:               RecapFull,
			KnobContinueOnError:          ErrorAdapt,
			KnobDestructiveActionPosture: DestructiveCedarOnly,
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal err = %v", err)
	}
	var back Layer
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal err = %v", err)
	}
	if back.Overrides[KnobMaxIterations] != 42 {
		t.Errorf("MaxIterations round-trip = %v", back.Overrides[KnobMaxIterations])
	}
	if back.Overrides[KnobAskOnAmbiguity] != AskHard {
		t.Errorf("AskOnAmbiguity round-trip = %v", back.Overrides[KnobAskOnAmbiguity])
	}
	if back.Overrides[KnobTokenCeilingPerTurn] != 9999 {
		t.Errorf("TokenCeiling round-trip = %v", back.Overrides[KnobTokenCeilingPerTurn])
	}
	if back.Overrides[KnobRecapStyle] != RecapFull {
		t.Errorf("RecapStyle round-trip = %v", back.Overrides[KnobRecapStyle])
	}
	if back.Overrides[KnobContinueOnError] != ErrorAdapt {
		t.Errorf("ContinueOnError round-trip = %v", back.Overrides[KnobContinueOnError])
	}
	if back.Overrides[KnobDestructiveActionPosture] != DestructiveCedarOnly {
		t.Errorf("DestructivePosture round-trip = %v", back.Overrides[KnobDestructiveActionPosture])
	}
}

func TestLayerUnmarshalErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"bad-tier", `{"level":"extreme","overrides":{}}`},
		{"unknown-knob", `{"level":null,"overrides":{"unknownKnob":1}}`},
		{"bad-int", `{"level":null,"overrides":{"maxIterations":"five"}}`},
		{"bad-families", `{"level":null,"overrides":{"autoApproveFamilies":"read"}}`},
		{"bad-json", `{"level":`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var l Layer
			err := json.Unmarshal([]byte(c.in), &l)
			if err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}

func TestLayerUnmarshalNullOverrides(t *testing.T) {
	// `overrides: null` is tolerated and produces an empty (non-nil) map.
	var l Layer
	if err := json.Unmarshal([]byte(`{"level":"default","overrides":null}`), &l); err != nil {
		t.Fatalf("err = %v", err)
	}
	if l.Overrides == nil {
		t.Error("Overrides should be non-nil after null unmarshal")
	}
	if len(l.Overrides) != 0 {
		t.Errorf("Overrides len = %d, want 0", len(l.Overrides))
	}
}

func TestLayerMarshalNilOverrides(t *testing.T) {
	// A Layer with a nil Overrides map should still serialize as `{}`.
	l := Layer{}
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(string(data), `"overrides":{}`) {
		t.Errorf("marshal = %s, want overrides:{} present", data)
	}
}

func layersEqual(a, b Layer) bool {
	switch {
	case a.Level == nil && b.Level != nil:
		return false
	case a.Level != nil && b.Level == nil:
		return false
	case a.Level != nil && b.Level != nil && *a.Level != *b.Level:
		return false
	}
	if len(a.Overrides) != len(b.Overrides) {
		return false
	}
	for k, va := range a.Overrides {
		vb, ok := b.Overrides[k]
		if !ok {
			return false
		}
		if fa, isFS := va.(FamilySet); isFS {
			fb, ok := vb.(FamilySet)
			if !ok || !fa.Equal(fb) {
				return false
			}
			continue
		}
		if va != vb {
			return false
		}
	}
	return true
}
