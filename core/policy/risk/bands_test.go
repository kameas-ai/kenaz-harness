package risk_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/risk"
)

// jsonBand mirrors testdata/bands.json's shape for decoding.
type jsonBand struct {
	Band     string   `json:"band"`
	Min      int      `json:"min"`
	Max      int      `json:"max"`
	Meaning  string   `json:"meaning"`
	Examples []string `json:"examples"`
}

type jsonFixture struct {
	Bands []jsonBand `json:"bands"`
}

// TestBandAnchorsMatchFixture cross-checks core/policy/risk/bands.go's
// BandAnchors against the committed testdata/bands.json fixture. The
// mission spec requires the bands be "committed fixtures" so the
// rater's behaviour is "pinned by tests rather than prose" — this test
// is what makes the Go table and the on-disk fixture unable to drift
// apart silently.
func TestBandAnchorsMatchFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/bands.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx jsonFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fx.Bands) != len(risk.BandAnchors) {
		t.Fatalf("fixture has %d bands, BandAnchors has %d", len(fx.Bands), len(risk.BandAnchors))
	}
	for i, want := range fx.Bands {
		got := risk.BandAnchors[i]
		if string(got.Band) != want.Band {
			t.Errorf("band[%d]: Go=%q fixture=%q", i, got.Band, want.Band)
		}
		if got.Min != want.Min || got.Max != want.Max {
			t.Errorf("band[%d] (%s): Go=[%d,%d] fixture=[%d,%d]", i, want.Band, got.Min, got.Max, want.Min, want.Max)
		}
		if got.Meaning != want.Meaning {
			t.Errorf("band[%d] (%s): Go meaning=%q fixture meaning=%q", i, want.Band, got.Meaning, want.Meaning)
		}
	}
}

// TestBandAnchorsCoverFullRange pins that the five bands partition
// 0-100 with no gaps and no overlaps — a gap would mean some score
// resolves to no band at all (BandFor would still fall back to an edge,
// silently miscategorising it), and an overlap would mean two different
// bands both claim the same score.
func TestBandAnchorsCoverFullRange(t *testing.T) {
	covered := make([]bool, 101)
	for _, a := range risk.BandAnchors {
		for s := a.Min; s <= a.Max; s++ {
			if covered[s] {
				t.Fatalf("score %d is covered by more than one band (overlap)", s)
			}
			covered[s] = true
		}
	}
	for s, ok := range covered {
		if !ok {
			t.Fatalf("score %d is not covered by any band (gap)", s)
		}
	}
}

// TestBandFor pins the score->band mapping table-driven against the
// spec FR-002 boundaries, including the edges of each band.
func TestBandFor(t *testing.T) {
	cases := []struct {
		score int
		want  risk.Band
	}{
		{0, risk.BandLow}, {19, risk.BandLow},
		{20, risk.BandModerate}, {39, risk.BandModerate},
		{40, risk.BandElevated}, {59, risk.BandElevated},
		{60, risk.BandHigh}, {79, risk.BandHigh},
		{80, risk.BandCritical}, {100, risk.BandCritical},
	}
	for _, c := range cases {
		if got := risk.BandFor(c.score); got != c.want {
			t.Errorf("BandFor(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestValidateScore(t *testing.T) {
	if err := risk.ValidateScore(0); err != nil {
		t.Errorf("ValidateScore(0) = %v, want nil", err)
	}
	if err := risk.ValidateScore(100); err != nil {
		t.Errorf("ValidateScore(100) = %v, want nil", err)
	}
	if err := risk.ValidateScore(-1); err == nil {
		t.Error("ValidateScore(-1) = nil, want an error")
	}
	if err := risk.ValidateScore(101); err == nil {
		t.Error("ValidateScore(101) = nil, want an error")
	}
}
