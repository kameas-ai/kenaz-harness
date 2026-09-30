package mlsidecar

import (
	"encoding/json"
	"time"
)

// State is one of the honestly-surfaced lifecycle states the WP12 brief
// and design §6.2/§7 require, plus StateLegacyUnverified (added by a
// 2026-09-29 security-review ruling — see that state's own doc comment).
// Every state is reachable through a real code path exercised by this
// package's tests (manager_test.go's "every state distinctly reachable"
// table).
type State string

const (
	// StateNotInstalled: no verified engine exists under the shared
	// install root's current version dir, and nothing answers :7774.
	StateNotInstalled State = "not_installed"
	// StateInstalling: a download/verify/unpack/spawn sequence is in
	// flight. Reason/Detail distinguish the sub-phase (see ReasonNone
	// vs. Detail strings set by install.go).
	StateInstalling State = "installing"
	// StateHealthy: a verified engine is running, answering /health,
	// and its lease is fresh.
	StateHealthy State = "healthy"
	// StateInstalledUnhealthy: an engine is installed but the running
	// (or expected-to-be-running) process is not answering health
	// checks, crashed, or was refused for a reason short of "never
	// verified at all" (see Reason for which).
	StateInstalledUnhealthy State = "installed_unhealthy"
	// StateUnverified: a process is listening on :7774 but this client
	// could not verify it runs from a client-verified shared-root
	// install (design F2/§3.7 R2). Never adopted; never killed.
	StateUnverified State = "unverified"
	// StateContractUnsupported: the running engine's contract-major
	// exceeds what this client build understands (design §3.7 R7). This
	// client alone is stranded read-only; the shared instance is left
	// untouched for other, compatible clients.
	StateContractUnsupported State = "contract_unsupported"
	// StateLegacyUnverified: a pre-lease engine (design F5) is running —
	// adopt-only in the sense that this client never terminates it and
	// never double-spawns a second instance on the port, but it is NEVER
	// usable for recommendations and NEVER reported healthy.
	//
	// AMENDS design F5 (coordinator ruling, 2026-09-29 security review):
	// F5's original text said a pre-lease engine should be "use[d] if
	// contract-compatible" — i.e. treated as StateHealthy. That
	// contradicts the design's own universal-verification rule (§3.7 R2:
	// "Adoption requires client-side Go verification of the on-disk
	// artifact ... A process not running from a client-verified
	// shared-dir install is a port-conflict state, never an adoptee");
	// R2 wins. A legacy engine was, by construction, installed by
	// something other than this client (an old Kenaz build that predates
	// the shared install root, or any process this client never staged
	// into versions/) — there is no install.json record to re-verify it
	// against, ever. Contract-major compatibility is irrelevant to that
	// fact, which is why this state does not depend on
	// contractCompatible at all (adopt.go). Healthy() (probe.go) reports
	// false for this state, so the advisor ladder falls through to
	// RungNone — this was the security-review finding: the pre-amendment
	// code mapped a compatible-looking legacy engine straight to
	// StateHealthy, and a process answering /health with a bare `{}`
	// (LifecycleProtocol==0, no "api" contract entry, which
	// contractCompatible treats as compatible) would have been adopted
	// as a healthy sidecar. See
	// TestManager_BareEmptyHealth_NeverReachesStateHealthy for the
	// planted-style regression pin.
	StateLegacyUnverified State = "legacy_unverified"
)

// Reason refines StateInstalledUnhealthy / StateUnverified with WHY,
// without inflating the six-state enum the brief pins. Each names a
// distinct row from design §7's failure-mode table.
type Reason string

const (
	ReasonNone             Reason = ""
	ReasonCrash            Reason = "crash"
	ReasonPortConflict     Reason = "port_conflict"
	ReasonDigestMismatch   Reason = "digest_mismatch"
	ReasonLeaseStale       Reason = "lease_stale"
	ReasonUpdatePending    Reason = "update_pending"
	ReasonLegacyEngine     Reason = "legacy_engine"
	ReasonQuarantineFailed Reason = "quarantine_failed"
)

// Status is the Manager's honest, point-in-time snapshot. Every reader
// (the future settings panel, the advice ladder probe) consumes this
// shape rather than re-deriving state from raw HTTP calls.
type Status struct {
	State         State
	Reason        Reason
	Detail        string
	EngineVersion string
	// ContractVersion is the running engine's lifecycle-protocol major
	// (/health lifecycle_protocol) — see SupportedContractMajor.
	ContractVersion int
	UpdatedAt       time.Time
}

// ModelHealth is one entry of HealthPayload.ModelDetails — per-model
// serving detail, keyed by model name (the engine's /health
// `model_details`). Slot/Refusal are JSON null when the engine has
// nothing to say; a null decodes to "".
type ModelHealth struct {
	Status  string `json:"status"`
	Slot    string `json:"slot,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// HealthPayload mirrors the wire shape of GET /health (design §3.5): "The
// payload carries identity ... The client does not trust the
// self-report." Shapes are the kenaz-ml engine's (two-client-engine-
// 01MSK2EN, routes.py HealthResponse; interop ruling 2026-09-30 — the
// engine's shapes won every contested point):
//
//   - LifecycleProtocol is the skew-window marker (design F5 / §3.7 R4):
//     the integer lease-protocol version, 1 today; zero (or the field's
//     absence, which json.Unmarshal leaves as the zero value) means a
//     pre-lease legacy engine (the engine also reports 0 in cloud mode).
//   - ContractVersions is PER KIND: the 16-hex feature-contract hashes
//     /v1/recommend accepts for that kind (N, and N-1 during a
//     transition). There is no API-wide contract major on /health; the
//     lifecycle protocol is the only protocol version the engine
//     publishes (see contractCompatible).
//   - Models is name -> status string ("ready" | "untrained" | ...), a
//     pre-existing field the engine may not reshape (its C-008); the
//     per-model detail lives in ModelDetails.
//   - EngineSHA256 is optional (absence tolerated): a cross-check only,
//     never a trust root (F2).
type HealthPayload struct {
	Status            string                 `json:"status,omitempty"`
	Mode              string                 `json:"mode,omitempty"`
	Product           string                 `json:"product"`
	SidecarVersion    string                 `json:"sidecar_version"`
	ContractVersions  map[string][]string    `json:"contract_versions"`
	ExePath           string                 `json:"exe_path"`
	EngineSHA256      string                 `json:"engine_sha256,omitempty"`
	Models            map[string]string      `json:"models"`
	ModelDetails      map[string]ModelHealth `json:"model_details,omitempty"`
	Device            string                 `json:"device"`
	LifecycleProtocol int                    `json:"lifecycle_protocol"`
}

// StatusPayload mirrors GET /status — a superset of /health. Design
// §3.7 R4: "/status carries lifecycle_protocol" is satisfied by
// embedding HealthPayload rather than duplicating the field.
type StatusPayload struct {
	HealthPayload
	UptimeSeconds float64 `json:"uptime_s"`
}

// KindContract is one entry of ContractsPayload.Kinds (design §3.3: "The
// sidecar publishes each kind's ordered feature contract at
// /v1/contracts"). Version is the kind's 16-hex feature-contract hash
// (the engine's service_version) — the value a client echoes back as
// RecommendRequest.FeatureContractVersion. Available is true only when a
// backend actually serves the kind; it is what routing gates on.
// Reason/Detail explain an unavailable kind (e.g. kind_not_served).
type KindContract struct {
	Features          []string `json:"features"`
	DTypes            []string `json:"dtypes,omitempty"`
	Version           string   `json:"version"`
	SupportedVersions []string `json:"supported_versions,omitempty"`
	Available         bool     `json:"available"`
	Backend           string   `json:"backend,omitempty"` // "heuristic" | "classic" | "laya"; "" when none
	Reason            string   `json:"reason,omitempty"`
	Detail            string   `json:"detail,omitempty"`
}

// ContractsPayload mirrors GET /v1/contracts.
type ContractsPayload struct {
	Kinds map[string]KindContract `json:"kinds"`
}

// LeaseWireRequest mirrors the body of POST /v1/clients/lease.
// MinContracts maps a kind to the 16-hex feature-contract hash this client
// requires; the engine only REPORTS an unmet one back (IncompatibleKinds)
// and never stops serving other clients over it.
type LeaseWireRequest struct {
	Client        string            `json:"client"`
	PID           int               `json:"pid"`
	ClientVersion string            `json:"client_version"`
	MinContracts  map[string]string `json:"min_contracts,omitempty"`
}

// LeaseWireResponse mirrors the response of POST /v1/clients/lease — the
// registration/compatibility handshake (leases themselves stay file-based
// on the harness side; the engine keeps its explicit leases in memory).
type LeaseWireResponse struct {
	LifecycleProtocol int                 `json:"lifecycle_protocol"`
	SidecarVersion    string              `json:"sidecar_version"`
	ContractVersions  map[string][]string `json:"contract_versions"`
	IncompatibleKinds map[string]string   `json:"incompatible_kinds"`
	Client            string              `json:"client"`
	PID               int                 `json:"pid"`
	LiveLeases        int                 `json:"live_leases"`
	ImplicitLeaseSec  float64             `json:"implicit_lease_sec"`
	IdleExitSec       float64             `json:"idle_exit_sec"`
	Managed           bool                `json:"managed"`
}

// RecommendRequest / RecommendResponse mirror POST /v1/recommend/{kind}
// (design §3.2). FeatureContractVersion is the kind's 16-hex contract
// hash, echoed from /v1/contracts (KindContract.Version). Generation is
// the serving manifest's version STRING ("0" when no artifact).
type RecommendRequest struct {
	Features               map[string]any `json:"features"`
	FeatureContractVersion string         `json:"feature_contract_version"`
	SessionID              string         `json:"session_id"`
	KindID                 string         `json:"kind_id"`
}

type RecommendResponse struct {
	Decision               *bool  `json:"decision,omitempty"`
	Score                  *int   `json:"score,omitempty"`
	Confidence             int    `json:"confidence"`
	KindID                 string `json:"kind_id"`
	FeatureContractVersion string `json:"feature_contract_version"`
	Model                  string `json:"model"`
	Rung                   string `json:"rung"`
	Backend                string `json:"backend"` // "heuristic" | "classic" | "laya"
	ModelIDSha8            string `json:"model_id_sha8"`
	CheckpointProvenance   string `json:"checkpoint_provenance"` // "local" | "org" | "base"
	Generation             string `json:"generation"`
	Unbenchmarked          bool   `json:"unbenchmarked"`
}

// LabelWireRow is one row of POST /v1/labels/{kind} (WP14; design §5.2 +
// Amendment A3.3, CONTRACT FROZEN 2026-09-30). The engine upserts on
// (client, kind, features_hash, ts) and a HIGHER Revision replaces —
// that is how a post-push user_action change lands. TS is the row's
// created_at in epoch milliseconds and never changes across revisions.
//
// FeaturesComplete rides verbatim (features_complete=false rows are
// pushed; the engine never trains them). Deliberately ABSENT: the local
// session_id — the engine's flip-back joins by (features_hash, ts), and
// a session identifier has no consumer on the ingest side.
type LabelWireRow struct {
	Kind             string          `json:"kind"`
	PromptVersion    string          `json:"prompt_version"`
	FeaturesHash     string          `json:"features_hash"`
	TS               int64           `json:"ts"`
	Revision         int64           `json:"revision"`
	Features         json.RawMessage `json:"features"`
	FeaturesComplete bool            `json:"features_complete"`
	Model            string          `json:"model"`
	Rung             string          `json:"rung"`
	Decision         bool            `json:"decision"`
	Confidence       int             `json:"confidence"`
	Shown            bool            `json:"shown"`
	UserAction       string          `json:"user_action"`
	LatencyMS        int64           `json:"latency_ms"`
}

// LabelPushRequest is the body of POST /v1/labels/{kind}. Client is the
// idempotency key's first component (the harness always sends "harness").
type LabelPushRequest struct {
	Client string         `json:"client"`
	Rows   []LabelWireRow `json:"rows"`
}

// LabelAck is the engine's durable ack position — a cursor over
// (ts, revision) (Amendment A3.3): every pushed row up to and including
// it is applied. The harness's own cursor only ever advances to an ack.
type LabelAck struct {
	TS       int64 `json:"ts"`
	Revision int64 `json:"revision"`
}

// LabelPushResponse is the response of POST /v1/labels/{kind}. Applied
// counts brand-new (client, kind, features_hash, ts) keys; Replaced
// counts higher-revision upserts; Stale counts rows whose revision was
// not higher than the stored one (a harmless duplicate delivery).
//
// Acked is JSON null when the batch's FIRST row was refused (the engine
// acks only the leading run of rows it applied, replaced or found stale,
// in the order sent); a null decodes to the zero LabelAck. Refused /
// Refusals report per-row refusals (client/kind mismatch, unknown
// user_action, non-finite features) — rows after a refused one are not
// acked, so the client re-sends them.
type LabelPushResponse struct {
	Acked    LabelAck          `json:"acked"`
	Applied  int               `json:"applied"`
	Replaced int               `json:"replaced"`
	Stale    int               `json:"stale"`
	Refused  int               `json:"refused"`
	Refusals []LabelRowRefusal `json:"refusals,omitempty"`
}

// LabelRowRefusal is one entry of LabelPushResponse.Refusals.
type LabelRowRefusal struct {
	Index        int    `json:"index"`
	FeaturesHash string `json:"features_hash"`
	TS           int64  `json:"ts"`
	Revision     int64  `json:"revision"`
	Reason       string `json:"reason"`
}

// SystemOneRequest / SystemOneResponse mirror POST /v1/systemone — raw
// laya pass-through, per the owner ruling that /v1/systemone exists for
// laya-serve without every backend wearing laya's wire shape (design
// §3.2). WP12's stub speaks this shape only so the endpoint's mere
// existence is provable against a fake server; nothing in this package
// calls it yet.
type SystemOneRequest struct {
	State        string   `json:"state"`
	Question     string   `json:"question"`
	Options      []string `json:"options,omitempty"`
	QuestionType string   `json:"question_type"` // "choice" | "score" | "noul"
}

type SystemOneResponse struct {
	Answer           string  `json:"answer"`
	AnswerConfidence float64 `json:"answer_confidence"`
	Confidence       float64 `json:"confidence"`
}
