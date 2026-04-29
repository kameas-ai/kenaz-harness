// Package stdio is a one-release source-compatibility alias for
// `core/mcp/transport/stdio`. Every public symbol the legacy
// package exposed is re-exported here so existing callers
// (`core/rpc/...`, downstream tools) keep compiling during the
// WP01..WP04 transport refactor without churning their import
// paths in lockstep.
//
// New code should import `core/mcp/transport/stdio` directly. This
// alias is scheduled for removal one release after the transport
// refactor lands.
package stdio

import (
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
	stdiopkg "github.com/sigil-tech/kaneaz-harness/core/mcp/transport/stdio"
)

// Pool / PoolOptions / ServerInstance — the production pool types.
type (
	Pool           = stdiopkg.Pool
	PoolOptions    = stdiopkg.PoolOptions
	ServerInstance = stdiopkg.ServerInstance
	SpawnSpec      = stdiopkg.SpawnSpec
)

// NewPool constructs an empty stdio pool. Re-exported alias of
// `transport/stdio.NewPool`.
var NewPool = stdiopkg.NewPool

// Sentinels and lifecycle state names.
var (
	ErrServerExists   = stdiopkg.ErrServerExists
	ErrServerNotFound = stdiopkg.ErrServerNotFound
)

// State / state-name re-exports.
type State = transport.State

const (
	StateStopped    = transport.StateStopped
	StateStarting   = transport.StateStarting
	StateRunning    = transport.StateRunning
	StateRestarting = transport.StateRestarting
	StateFailed     = transport.StateFailed
)

// RecipeStatus is the live status snapshot type.
type RecipeStatus = transport.RecipeStatus

// Status field width caps + topic constants exposed for callers
// that build status snapshots (the rpc views layer).
const (
	StatusStderrTailBytes = transport.StatusStderrTailBytes
	ProgressTopic         = transport.ProgressTopic
)

// Handler interfaces shared with the transport package.
type (
	SamplingHandler = transport.SamplingHandler
	RootsHandler    = transport.RootsHandler
	EventPublisher  = transport.EventPublisher

	ProgressEvent = transport.ProgressEvent
)

// Sampling + Roots constructors.
var (
	LLMSamplingHandler = transport.LLMSamplingHandler
	DefaultRoots       = transport.DefaultRoots
)

// Wire-protocol constants.
const (
	SupportedProtocolVersion = transport.SupportedProtocolVersion
	JSONRPCVersion           = transport.JSONRPCVersion
	ClientName               = transport.ClientName
	ClientVersion            = transport.ClientVersion
)
