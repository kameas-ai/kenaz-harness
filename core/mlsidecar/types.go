package mlsidecar

import "time"

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
	State           State
	Reason          Reason
	Detail          string
	EngineVersion   string
	ContractVersion int
	UpdatedAt       time.Time
}

// ModelHealth is one entry of HealthPayload.Models — per-kind serving
// health, keyed by kind id.
type ModelHealth struct {
	Status string `json:"status"` // "ok" | "refused"
	Reason string `json:"reason,omitempty"`
}

// HealthPayload mirrors the wire shape of GET /health (design §3.5): "The
// payload carries identity ... The client does not trust the
// self-report." LifecycleProtocol is the skew-window marker (design F5 /
// §3.7 R4): zero (or the field's absence, which json.Unmarshal leaves as
// the zero value) means a pre-lease legacy engine.
type HealthPayload struct {
	Product           string                 `json:"product"`
	SidecarVersion    string                 `json:"sidecar_version"`
	ContractVersions  map[string]int         `json:"contract_versions"`
	ExePath           string                 `json:"exe_path"`
	EngineSHA256      string                 `json:"engine_sha256"`
	Models            map[string]ModelHealth `json:"models"`
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
// /v1/contracts").
type KindContract struct {
	ContractVersion int      `json:"contract_version"`
	Features        []string `json:"features"`
	Backend         string   `json:"backend"` // "heuristic" | "classic" | "laya"
	Available       bool     `json:"available"`
}

// ContractsPayload mirrors GET /v1/contracts.
type ContractsPayload struct {
	Kinds map[string]KindContract `json:"kinds"`
}

// LeaseWireRequest mirrors the body of POST /v1/clients/lease.
type LeaseWireRequest struct {
	Client        string         `json:"client"`
	PID           int            `json:"pid"`
	ClientVersion string         `json:"client_version"`
	MinContracts  map[string]int `json:"min_contracts,omitempty"`
}

// LeaseWireResponse mirrors the response of POST /v1/clients/lease.
type LeaseWireResponse struct {
	LeaseID    string `json:"lease_id"`
	ExpiresInS int    `json:"expires_in_s"`
}

// RecommendRequest / RecommendResponse mirror POST /v1/recommend/{kind}
// (design §3.2) — included so the stub and any future caller share one
// wire shape; WP12 itself never calls this (no kind is sidecar-preferred
// yet, design §9 Phase 0's gating note).
type RecommendRequest struct {
	Features               map[string]any `json:"features"`
	FeatureContractVersion int            `json:"feature_contract_version"`
	SessionID              string         `json:"session_id"`
	KindID                 string         `json:"kind_id"`
}

type RecommendResponse struct {
	Decision               *bool  `json:"decision,omitempty"`
	Score                  *int   `json:"score,omitempty"`
	Confidence             int    `json:"confidence"`
	KindID                 string `json:"kind_id"`
	FeatureContractVersion int    `json:"feature_contract_version"`
	Model                  string `json:"model"`
	Rung                   string `json:"rung"`
	Backend                string `json:"backend"` // "heuristic" | "classic" | "laya"
	ModelIDSha8            string `json:"model_id_sha8"`
	CheckpointProvenance   string `json:"checkpoint_provenance"` // "local" | "org" | "base"
	Generation             int    `json:"generation"`
	Unbenchmarked          bool   `json:"unbenchmarked"`
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
