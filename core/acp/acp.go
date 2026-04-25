package acp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// ProtocolVersion is the A2A protocol revision the harness implements.
// FR-001.
const ProtocolVersion = "1.0"

// TransportKind enumerates supported transports. FR-007, FR-016.
type TransportKind string

const (
	// TransportUDS is the default transport on macOS/Linux.
	TransportUDS TransportKind = "uds"
	// TransportLoopback is the default transport on Windows; opt-in elsewhere.
	TransportLoopback TransportKind = "http_loopback"
	// TransportLAN binds to a non-loopback local interface; opt-in only.
	TransportLAN TransportKind = "http_lan"
	// TransportPublic is WAN-bound; v1.x; refuses to load without auth_ref.
	TransportPublic TransportKind = "http_public"
)

// Valid reports whether t is a recognised transport kind.
func (t TransportKind) Valid() bool {
	switch t {
	case TransportUDS, TransportLoopback, TransportLAN, TransportPublic:
		return true
	}
	return false
}

// CardSource enumerates how a peer's AgentCard is obtained.
type CardSource string

const (
	// CardSourceInline carries the card inline in the bundle artifact.
	CardSourceInline CardSource = "inline"
	// CardSourceWellKnown fetches the card from /.well-known/agent.json.
	CardSourceWellKnown CardSource = "well_known"
	// CardSourceManualURL fetches the card from a configured URL.
	CardSourceManualURL CardSource = "manual_url"
)

// Valid reports whether c is a recognised card source.
func (c CardSource) Valid() bool {
	switch c {
	case CardSourceInline, CardSourceWellKnown, CardSourceManualURL:
		return true
	}
	return false
}

// Skill is a named capability advertised by an agent. FR-005.
type Skill struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	InputSchema  json.RawMessage   `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage   `json:"output_schema,omitempty"`
	Examples     []json.RawMessage `json:"examples,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
}

// AgentCard is A2A's self-description document. FR-008.
//
// v1 cards are unsigned (Signed=false). Signing arrives via the
// a2a-signed-cards-trust-01KQ18P9 mission, which swaps in a real Verifier
// implementation in core/acp/verify/. FR-020.
type AgentCard struct {
	AgentID         string   `json:"agent_id"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocol_version"`
	EndpointURL     string   `json:"endpoint_url"`
	Skills          []Skill  `json:"skills"`
	AuthSchemes     []string `json:"auth_schemes,omitempty"`
	Signed          bool     `json:"signed"`
}

// PeerProfile is a bundle-declared remote A2A peer. FR-004, FR-006, FR-007.
type PeerProfile struct {
	PeerID       string
	EndpointURL  string
	Transport    TransportKind
	CardSource   CardSource
	InlineCard   *AgentCard
	AuthRef      *secrets.Reference
	CardCacheTTL time.Duration
}

// TaskRole is the harness's role on a Task. FR-009.
type TaskRole string

const (
	// RoleCaller is the outbound role.
	RoleCaller TaskRole = "caller"
	// RoleCallee is the inbound role.
	RoleCallee TaskRole = "callee"
)

// TaskState mirrors A2A states. FR-009. Terminal states (Completed, Failed,
// Cancelled) cannot transition further.
type TaskState string

const (
	StatePending       TaskState = "pending"
	StateRunning       TaskState = "running"
	StateAwaitingInput TaskState = "awaiting_input"
	StateCompleted     TaskState = "completed"
	StateFailed        TaskState = "failed"
	StateCancelled     TaskState = "cancelled"
)

// Terminal reports whether s is a terminal state.
func (s TaskState) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	}
	return false
}

// validTransitions describes the legal forward transitions in the Task
// lifecycle. State never moves backwards. Terminal states are immutable.
var validTransitions = map[TaskState]map[TaskState]bool{
	StatePending: {
		StateRunning:   true,
		StateFailed:    true,
		StateCancelled: true,
	},
	StateRunning: {
		StateAwaitingInput: true,
		StateCompleted:     true,
		StateFailed:        true,
		StateCancelled:     true,
	},
	StateAwaitingInput: {
		StateRunning:   true,
		StateCompleted: true,
		StateFailed:    true,
		StateCancelled: true,
	},
	// Terminal states have no outgoing transitions.
	StateCompleted: {},
	StateFailed:    {},
	StateCancelled: {},
}

// CanTransition reports whether the lifecycle permits from -> to.
// Terminal states reject every transition (including idempotent self-loops)
// to enforce the append-only state-machine invariant.
func CanTransition(from, to TaskState) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// Task is the harness-side persisted view of an A2A task. FR-009, FR-019.
type Task struct {
	TaskID          string
	A2ATaskID       string
	LocalAgentID    string
	RemotePeerID    string
	SkillID         string
	Role            TaskRole
	State           TaskState
	ParentSessionID string
	SnapshotID      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MessageDirection enumerates message direction from the harness's POV.
type MessageDirection string

const (
	DirInbound  MessageDirection = "inbound"
	DirOutbound MessageDirection = "outbound"
)

// MessageKind enumerates the shape hint of a Message. Mirrors A2A.
type MessageKind string

const (
	KindRequest    MessageKind = "request"
	KindResponse   MessageKind = "response"
	KindChunk      MessageKind = "chunk"
	KindError      MessageKind = "error"
	KindToolCall   MessageKind = "tool_call"
	KindToolResult MessageKind = "tool_result"
)

// Message is one entry in a Task's message stream. Append-only; redacted
// at persistence time per FR-012.
type Message struct {
	MessageID string
	TaskID    string
	Direction MessageDirection
	Kind      MessageKind
	Payload   json.RawMessage
	Sequence  int
	EmittedAt time.Time
}

// PreflightResult reports the outcome of preflighting a single peer.
// FR-018.
type PreflightResult struct {
	PeerID  string
	Success bool
	Error   error
}

// PeerRegistry materialises the bundle-declared peer set. FR-004, FR-018.
type PeerRegistry interface {
	Load(profiles []PeerProfile) error
	Lookup(peerID string) (PeerProfile, error)
	All() []PeerProfile
	PreflightAll(ctx context.Context) []PreflightResult
}

// DispatchOption configures a single Dispatch call. FR-017, FR-019.
type DispatchOption func(*DispatchOptions)

// DispatchOptions carries optional Dispatch knobs.
type DispatchOptions struct {
	ParentSessionID string
	SnapshotID      string
	Timeout         time.Duration
}

// WithParentSession sets the parent session id for the dispatched Task.
func WithParentSession(id string) DispatchOption {
	return func(o *DispatchOptions) { o.ParentSessionID = id }
}

// WithSnapshotID pins the resolver-graph snapshot id for replay (FR-019).
func WithSnapshotID(id string) DispatchOption {
	return func(o *DispatchOptions) { o.SnapshotID = id }
}

// WithTimeout sets a per-call timeout.
func WithTimeout(d time.Duration) DispatchOption {
	return func(o *DispatchOptions) { o.Timeout = d }
}

// A2AClient is the outbound surface. FR-002, FR-010, FR-017.
type A2AClient interface {
	Dispatch(ctx context.Context, peerID, skillID string, body json.RawMessage,
		opts ...DispatchOption) (Task, <-chan Message, error)
	Cancel(ctx context.Context, taskID string) error
}

// SkillDispatcher is what the inbound server calls when a Task arrives.
type SkillDispatcher interface {
	Dispatch(ctx context.Context, task Task, in json.RawMessage) (out json.RawMessage, err error)
}

// A2AServer is the inbound surface. FR-003, FR-014.
type A2AServer interface {
	Expose(card AgentCard, dispatcher SkillDispatcher) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// VerifyResult carries the outcome of verifying an AgentCard.
type VerifyResult struct {
	// Accepted is true when the card was verified and may be used.
	Accepted bool
	// Reason carries a stable machine-friendly reason code; empty when
	// Accepted is true.
	Reason string
}

// Verifier is the FR-020 stable seam for signed-card verification. v1
// ships with verify.UnsignedAcceptVerifier as the default. The
// a2a-signed-cards-trust-01KQ18P9 mission swaps in a real verifier.
type Verifier interface {
	Verify(ctx context.Context, card AgentCard) (VerifyResult, error)
}
