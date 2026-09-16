package risk

import (
	"encoding/json"
	"testing"
)

// requiredBenchmarkModelFields is the exact key set every row in
// data/rater_benchmark.json must carry. Checked against the RAW JSON
// (map[string]json.RawMessage), not the typed struct — unmarshalling
// into BenchmarkModelRow would silently accept a row missing a key
// (Go just leaves the field at its zero value), which is precisely the
// half-filled-row failure mode this test exists to catch ("a future
// hand-edit can't half-fill a row").
var requiredBenchmarkModelFields = []string{
	"id",
	"label",
	"measured_via",
	"data_available",
	"bands_exact",
	"bands_adjacent",
	"bands_miss",
	"injection_raw_score",
	"injection_raw_note",
	"reliability_5s_pct",
	"reliability_30s_pct",
	"median_latency_ms",
	"p90_latency_ms",
	"cost_per_rating_usd",
	"notes",
}

var requiredProvenanceFields = []string{"benchmark_date", "calls", "method"}

func TestBenchmarkSchemaComplete(t *testing.T) {
	var raw struct {
		Provenance map[string]json.RawMessage   `json:"provenance"`
		Models     []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raterBenchmarkJSON, &raw); err != nil {
		t.Fatalf("raterBenchmarkJSON does not parse: %v", err)
	}

	for _, f := range requiredProvenanceFields {
		if _, ok := raw.Provenance[f]; !ok {
			t.Errorf("provenance missing required field %q", f)
		}
	}
	if len(raw.Provenance) == 0 {
		t.Fatal("provenance object is empty")
	}

	if len(raw.Models) == 0 {
		t.Fatal("models array is empty")
	}
	for i, row := range raw.Models {
		id := "<no id>"
		if idRaw, ok := row["id"]; ok {
			_ = json.Unmarshal(idRaw, &id)
		}
		for _, f := range requiredBenchmarkModelFields {
			if _, ok := row[f]; !ok {
				t.Errorf("model row %d (%s) missing required field %q", i, id, f)
			}
		}
	}
}

// TestBenchmarkParsesIntoTypedStruct is a lighter smoke test that the
// typed accessor (Benchmark()) round-trips without error and produces
// the same row count as the raw JSON — a belt-and-braces check that the
// Go struct tags actually match the vendored keys (a typo in a `json:"…"`
// tag would pass TestBenchmarkSchemaComplete but silently drop data here).
func TestBenchmarkParsesIntoTypedStruct(t *testing.T) {
	b, err := Benchmark()
	if err != nil {
		t.Fatalf("Benchmark() error: %v", err)
	}
	if b.Provenance.BenchmarkDate == "" {
		t.Error("Provenance.BenchmarkDate is empty after parse — tag mismatch?")
	}
	if b.Provenance.Calls == 0 {
		t.Error("Provenance.Calls is zero after parse — tag mismatch?")
	}
	if b.Provenance.Method == "" {
		t.Error("Provenance.Method is empty after parse — tag mismatch?")
	}
	if len(b.Models) == 0 {
		t.Fatal("Models is empty after parse")
	}
	for _, row := range b.Models {
		if row.ID == "" {
			t.Error("a model row has an empty ID after parse — tag mismatch?")
		}
		if row.Label == "" {
			t.Errorf("model %q has an empty Label after parse", row.ID)
		}
		if row.MeasuredVia == "" {
			t.Errorf("model %q has an empty MeasuredVia after parse", row.ID)
		}
		if row.Notes == "" {
			t.Errorf("model %q has an empty Notes after parse", row.ID)
		}
	}
}

func TestFindBenchmarkRow(t *testing.T) {
	b, err := Benchmark()
	if err != nil {
		t.Fatalf("Benchmark() error: %v", err)
	}

	t.Run("matches by exact id", func(t *testing.T) {
		row, ok := FindBenchmarkRow(b.Models, "qwen/qwen-2.5-7b-instruct")
		if !ok {
			t.Fatal("expected a match for the openrouter provider default")
		}
		if row.BandsExact != 4 {
			t.Errorf("bands_exact = %d, want 4", row.BandsExact)
		}
	})

	t.Run("matches by alias", func(t *testing.T) {
		row, ok := FindBenchmarkRow(b.Models, "deepseek-chat")
		if !ok {
			t.Fatal("expected the bare direct-provider id to alias to deepseek/deepseek-chat")
		}
		if row.ID != "deepseek/deepseek-chat" {
			t.Errorf("resolved row ID = %q, want deepseek/deepseek-chat", row.ID)
		}
	})

	t.Run("unknown model reports not found, never a zero-value match", func(t *testing.T) {
		_, ok := FindBenchmarkRow(b.Models, "some/totally-unknown-model")
		if ok {
			t.Fatal("expected no match for an unbenchmarked model id")
		}
	})
}
