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
	// cancelled writes and bash. Its `tool` is a BuiltinToolNames entry by
	// exact name, or mcp:h("<server>__<tool>") for anything else
	// (ToolWireName). A Rev2Kinds event.
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

// BuiltinToolNames is the CLOSED list of first-party tool names that
// agent.tool's `tool` may carry in clear (counsel D43/C3): the harness's
// own builtins (server "kenaz") and the tools of its in-process
// first-party MCP servers ("harness-self", "fleet-sites"), each as the
// dispatched "<server>__<tool>" name. Every other name — any MCP tool,
// shipped recipe or not, and any model-invented string — goes out as
// "mcp:" + h(name) (ToolWireName). This governs the WIRE form; WP11's
// local store uses the same minimised form.
//
// Kept sorted. Generated from the registered builtins: the pin test
// core/rpc/mlproducer_builtin_tools_test.go boots the real API, lists
// every registered builtin and every harness-self / fleet-sites tool, and
// fails (printing the list to paste here) on any difference.
var BuiltinToolNames = []string{
	"fleet-sites__sites_delete",
	"fleet-sites__sites_deploy",
	"fleet-sites__sites_init",
	"fleet-sites__sites_list",
	"fleet-sites__sites_logs",
	"fleet-sites__sites_status",
	"harness-self__harness_read_get_onboarding_recommendations",
	"harness-self__harness_read_list_mcp_recipes",
	"harness-self__harness_read_list_models",
	"harness-self__harness_read_list_providers",
	"harness-self__harness_read_list_sessions",
	"harness-self__harness_read_list_settings",
	"harness-self__harness_read_materialize_run",
	"harness-self__harness_write_add_provider",
	"harness-self__harness_write_create_project",
	"harness-self__harness_write_create_scheduled_run",
	"harness-self__harness_write_create_session",
	"harness-self__harness_write_draft_agent_graph",
	"harness-self__harness_write_remove_provider",
	"kenaz__ask_user_question",
	"kenaz__bash",
	"kenaz__build_knowledge_site",
	"kenaz__edit_file",
	"kenaz__enter_plan_mode",
	"kenaz__exit_plan_mode",
	"kenaz__fork_conversation",
	"kenaz__glob",
	"kenaz__grep",
	"kenaz__list_dir",
	"kenaz__list_open_worklist",
	"kenaz__list_secrets",
	"kenaz__load_tools",
	"kenaz__monitor",
	"kenaz__read_context_file",
	"kenaz__read_file",
	"kenaz__request_filesystem_access",
	"kenaz__save_artifact",
	"kenaz__save_document",
	"kenaz__skill",
	"kenaz__sleep",
	"kenaz__subagent_dispatch",
	"kenaz__todo_write",
	"kenaz__update_artifact",
	"kenaz__update_document",
	"kenaz__web_fetch",
	"kenaz__web_search",
	"kenaz__write_file",
}

// MCPToolPrefix prefixes the hashed wire form of every tool name not in
// BuiltinToolNames: "mcp:" + h("<server>__<tool>").
const MCPToolPrefix = "mcp:"

// IsBuiltinToolName reports whether name is in BuiltinToolNames.
func IsBuiltinToolName(name string) bool {
	for _, n := range BuiltinToolNames {
		if n == name {
			return true
		}
	}
	return false
}

// Rev2Kinds are the kinds Fleet gates on the org's AI-usage instruction
// (its "rev-2" gate; owner decision 2026-10-10 after counsel ruled that
// agent.turn's model_calls / tool_calls are AI-usage metadata, and
// counsel D43/C3 that agent.tool is too). A Rev2Kinds event enters the
// SHIP outbox only while the gate's latest consent has AIUsageAllowed
// (GET /me/ml ai_usage_allowed, absent = false; Recorder.shipEligible),
// and the shipper drops it unsent if, at ship time, that is no longer
// true. This is a ship gate, not an observation rule: a local-only store
// (spec A-16 / WP11) may keep them. Task state (phase, files, counters)
// never depends on a Rev2Kinds event being emitted. file, terminal,
// commit and phase_change are not Rev2Kinds. The WP08 / WP09 AI-usage
// kinds join this list when they land.
var Rev2Kinds = []string{KindTool, KindTurn}

// IsRev2Kind reports whether kind is in Rev2Kinds.
func IsRev2Kind(kind string) bool {
	for _, k := range Rev2Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Bound is an inclusive integer range a payload value is clamped to
// before it is recorded.
type Bound struct{ Min, Max int64 }

// PayloadBounds are the value bounds sent to Fleet for numeric payload
// keys, by kind. The recorder clamps into them (ClampPayload); the
// minimisation gate (scripts/ci/cmd/checkmlminimisation) checks this table
// against its own copy of the contract and every recorded value against
// it. This is the one place they are defined.
var PayloadBounds = map[string]map[string]Bound{
	KindTool: {
		"dur_ms": {0, 86_400_000}, // one day
	},
	KindTurn: {
		"dur_ms":      {0, 86_400_000}, // one day
		"model_calls": {0, 10_000},
		"tool_calls":  {0, 10_000},
	},
}

// ClampPayload clamps every bounded key of kind's payload into
// PayloadBounds, in place, and returns payload. Unbounded keys and
// non-integer values are left alone.
func ClampPayload(kind string, payload map[string]any) map[string]any {
	for key, b := range PayloadBounds[kind] {
		var v int64
		switch x := payload[key].(type) {
		case int:
			v = int64(x)
		case int64:
			v = x
		default:
			continue
		}
		v = min(max(v, b.Min), b.Max)
		payload[key] = v
	}
	return payload
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
