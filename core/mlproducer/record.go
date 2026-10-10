package mlproducer

import "encoding/json"

// Event kinds this producer emits (spec §12 A-10, owner ruling
// 2026-10-09: agent actions ship as the engine's own kinds so kenaz-ml
// features them; `agent.*` remains only for what has no engine kind).
// Separation from the daemon rests on producer=harness, the `agent-`
// task-id prefix and the KENAZ_ACTOR / KENAZ_SESSION process markers,
// not on kind names. Names confirmed by kenaz-ml 2026-10-09.
const (
	// KindFile: kenaz__write_file / kenaz__edit_file with outcome ok, and
	// (WP07, A-15) each file a successful read / grep / glob / list_dir
	// touched, up to 20 per call, next to that call's agent.tool row.
	KindFile = "file"
	// KindTerminal: a kenaz__bash command that actually ran (outcome ok or
	// error), and (WP07, A-15) a background job's exit as a follow-up.
	// bash's own gate refusals are agent.tool / denied instead.
	KindTerminal = "terminal"
	// KindCommit: an agent `git commit` that exited 0 (its terminal event
	// ships as well).
	KindCommit = "commit"
	// KindPhaseChange: the inferred phase changed.
	KindPhaseChange = "phase_change"
	// KindTool: every other call that reached dispatch, including denied /
	// cancelled writes and bash.
	KindTool = "agent.tool"
	// KindTurn: a chat turn ended.
	KindTurn = "agent.turn"
)

// KindTable is THE kind allowlist: every kind this package can emit and
// its complete payload key set (spec §12 A-10). The recorder writes
// nothing outside it; WP04's minimisation gate reads it as an exact
// allowlist. Order is the spec table's.
var KindTable = []struct {
	Kind        string
	PayloadKeys []string
}{
	{KindFile, []string{"task", "path", "file"}},
	{KindTerminal, []string{"task", "cmd", "exit_code"}},
	{KindCommit, []string{"task"}},
	{KindPhaseChange, []string{"task", "phase"}},
	{KindTool, []string{"task", "tool", "outcome", "dur_ms"}},
	{KindTurn, []string{"task", "dur_ms", "model_calls", "tool_calls", "outcome"}},
}

// Kinds returns every kind this package can emit, from KindTable.
func Kinds() []string {
	out := make([]string, len(KindTable))
	for i, k := range KindTable {
		out[i] = k.Kind
	}
	return out
}

// PayloadKeys returns the allowed payload keys for kind (nil if unknown),
// from KindTable.
func PayloadKeys(kind string) []string {
	for _, k := range KindTable {
		if k.Kind == kind {
			return k.PayloadKeys
		}
	}
	return nil
}

// Wire constants WP03's shipper stamps on each record.
const (
	// ProducerSource is kameas.ml.source.
	ProducerSource = "harness"
	// SchemaVersion is kameas.ml.schema_version.
	SchemaVersion = 1
	// EventSource is the event body's `source` (collector name).
	EventSource = "harness.agent"
	// TaskIDPrefix prefixes every task id: agent-<h(root session)>.
	TaskIDPrefix = "agent-"
)

// Phase is fleet's task phase enum (spec §3.1).
type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhaseExploring Phase = "exploring"
	PhaseCoding    Phase = "coding"
	PhaseTesting   Phase = "testing"
	PhaseReviewing Phase = "reviewing"
)

// eventBody is the events-table record body.
type eventBody struct {
	ID      int64          `json:"id"`
	Kind    string         `json:"kind"`
	Source  string         `json:"source"`
	TS      int64          `json:"ts"`
	Payload map[string]any `json:"payload"`
}

// taskBody is the tasks-table record body (spec §3.1).
type taskBody struct {
	ID          string         `json:"id"`
	RepoRoot    string         `json:"repo_root"`
	Branch      string         `json:"branch"`
	Phase       string         `json:"phase"`
	Files       map[string]int `json:"files"`
	StartedAt   int64          `json:"started_at"`
	LastActive  int64          `json:"last_active"`
	CompletedAt *int64         `json:"completed_at,omitempty"`
	CommitCount int            `json:"commit_count"`
	TestRuns    int            `json:"test_runs"`
	TestFails   int            `json:"test_fails"`
}

func encodeEvent(seq int64, kind string, ts int64, payload map[string]any) ([]byte, error) {
	return json.Marshal(eventBody{ID: seq, Kind: kind, Source: EventSource, TS: ts, Payload: payload})
}
