package risk

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// raterBenchmarkJSON vendors the risk-rater model comparison of record
// (owner ruling 2026-09-15, mission risk-rated-autonomy-01PMRA01, task
// #109: 234 calls across 9 models against the live dev profile's
// OpenRouter credential). Full write-up:
// rater-comparison/RESULTS.md (not committed — this file is the
// committed, load-bearing distillation the picker UI renders numbers
// from).
//
// Every row here MUST carry every BenchmarkModelRow field (see that
// type's doc) plus the shared BenchmarkProvenance — a half-filled row is
// exactly the "silent lie" this mission's spec calls out (models the UI
// shows without data must show an explicit "unbenchmarked" badge,
// never a blank cell). TestBenchmarkSchemaComplete pins this.
//
//go:embed data/rater_benchmark.json
var raterBenchmarkJSON []byte

// BenchmarkProvenance records how and when the vendored benchmark data
// was collected, so a reader (or the UI) never has to take the numbers
// on faith.
type BenchmarkProvenance struct {
	BenchmarkDate string `json:"benchmark_date"`
	Calls         int    `json:"calls"`
	Method        string `json:"method"`
}

// BenchmarkModelRow is one measured model's risk-rater performance
// against the FR-002 band anchors, the WP06 injection fixture, and a
// representative tool-call latency payload.
//
// InjectionRawScore is a pointer because one measured model
// (llama-3.2-1b-instruct) returned a response that failed to parse as
// JSON on its injection call — there is no numeric score to report, and
// coercing that to 0 would misreport a parse failure as "rated this
// call as harmless," which is the opposite of what happened.
// InjectionRawNote carries the human-readable reason whenever the score
// is nil (or otherwise noteworthy, e.g. "1 success out of 26 calls").
type BenchmarkModelRow struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	MeasuredVia string `json:"measured_via"`
	// ApproximateForDirectProvider marks rows measured through OpenRouter
	// whose id/pricing is an approximation when the SAME underlying model
	// is reached through its direct provider (haiku via anthropic,
	// gpt-4o-mini via openai) rather than through OpenRouter's routing.
	ApproximateForDirectProvider bool `json:"approximate_for_direct_provider,omitempty"`
	// Aliases lists the model id(s) a DIRECT (non-OpenRouter) provider
	// profile would use for this same model, so the picker can match a
	// direct provider's AvailableModels() entry back to this row.
	Aliases []string `json:"aliases,omitempty"`
	// DataAvailable is false only for a model that produced no usable
	// quality data at all (a live provider outage during the benchmark
	// run, not an architectural verdict) — see Notes.
	DataAvailable     bool    `json:"data_available"`
	BandsExact        int     `json:"bands_exact"`
	BandsAdjacent     int     `json:"bands_adjacent"`
	BandsMiss         int     `json:"bands_miss"`
	InjectionRawScore *int    `json:"injection_raw_score"`
	InjectionRawNote  string  `json:"injection_raw_note"`
	Reliability5sPct  float64 `json:"reliability_5s_pct"`
	Reliability30sPct float64 `json:"reliability_30s_pct"`
	MedianLatencyMs   float64 `json:"median_latency_ms"`
	P90LatencyMs      float64 `json:"p90_latency_ms"`
	CostPerRatingUSD  float64 `json:"cost_per_rating_usd"`
	Notes             string  `json:"notes"`
}

// RiskRaterBenchmark is the full vendored comparison: provenance plus
// every measured model row. Served to the frontend verbatim via
// Settings_GetRiskRaterBenchmark so the picker UI and this package stay
// on exactly one source of truth.
type RiskRaterBenchmark struct {
	Provenance BenchmarkProvenance `json:"provenance"`
	Models     []BenchmarkModelRow `json:"models"`
}

// Benchmark parses and returns the vendored rater_benchmark.json data.
// Parsed fresh on every call (the file is a few KB) rather than cached
// behind a package-level singleton, so a future hot-reload story is not
// blocked by one.
func Benchmark() (RiskRaterBenchmark, error) {
	var b RiskRaterBenchmark
	if err := json.Unmarshal(raterBenchmarkJSON, &b); err != nil {
		return RiskRaterBenchmark{}, fmt.Errorf("risk: parse vendored rater_benchmark.json: %w", err)
	}
	return b, nil
}

// FindBenchmarkRow looks up modelID (or profileKind-prefixed variants) in
// rows by ID first, then by Aliases — the match a direct-provider profile
// needs when the benchmark was only measured through OpenRouter's
// "vendor/model" id shape. Returns ok=false when no row matches, which
// callers (the picker UI, and any Go-side caller wanting the same
// behaviour) must render as an explicit "unbenchmarked" state, never a
// blank/zero row.
func FindBenchmarkRow(rows []BenchmarkModelRow, modelID string) (BenchmarkModelRow, bool) {
	for _, r := range rows {
		if r.ID == modelID {
			return r, true
		}
	}
	for _, r := range rows {
		for _, a := range r.Aliases {
			if a == modelID {
				return r, true
			}
		}
	}
	return BenchmarkModelRow{}, false
}
