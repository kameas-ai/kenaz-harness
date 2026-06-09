// Package stdio implements the stdio MCP transport — a child
// process spawned with stdin/stdout pipes carrying newline-
// delimited JSON-RPC 2.0. WP01 split the transport-agnostic types
// (Framer, Router, RingBuffer, ProgressForwarder, LogSink, etc.)
// into `core/mcp/transport`; this package now hosts only the
// stdio-specific lifecycle: Spawn → Initialize → Close, plus the
// health-pinger and supervisor restart machinery.
//
// The legacy import path `core/mcp/stdio` re-exports this
// package's symbols for one release while downstream callers
// migrate.
package stdio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Default deadlines used by the lifecycle code paths. These are
// re-exported aliases of the transport-package defaults so the
// existing stdio tests can reference them without importing the
// transport package directly.
const (
	defaultFirstByteTimeout = transport.DefaultFirstByteTimeout
	defaultInitTimeout      = transport.DefaultInitTimeout
	closeGrace              = transport.CloseGrace
	defaultPingPeriod       = transport.DefaultPingPeriod
	defaultPingTimeout      = transport.DefaultPingTimeout
	restartWindow           = transport.RestartWindow
)

// backoffSchedule encodes the FR-007 exponential backoff: 1 s, 2 s,
// 4 s — three attempts maximum inside any 5-minute window. Aliased
// from the transport package so the supervisor code below reads
// the way the original stdio package did.
var backoffSchedule = transport.BackoffSchedule

// SpawnSpec mirrors transport.SpawnSpec for backwards-source-
// compatibility with the existing stdio tests. The fields are
// identical; the legacy stdio package re-exports this type.
type SpawnSpec = transport.SpawnSpec

// State / state-name re-exports. Kept here so existing tests can
// reference `StateRunning` etc. without importing the transport
// package.
type State = transport.State

const (
	StateStopped    = transport.StateStopped
	StateStarting   = transport.StateStarting
	StateRunning    = transport.StateRunning
	StateRestarting = transport.StateRestarting
	StateFailed     = transport.StateFailed
)

// Wire-protocol re-exports kept for source compatibility with the
// original stdio package. Tests reference these directly.
const (
	SupportedProtocolVersion = transport.SupportedProtocolVersion
	JSONRPCVersion           = transport.JSONRPCVersion
	ClientName               = transport.ClientName
	ClientVersion            = transport.ClientVersion

	MethodInitialize         = transport.MethodInitialize
	MethodPing               = transport.MethodPing
	MethodToolsList          = transport.MethodToolsList
	MethodToolsCall          = transport.MethodToolsCall
	MethodResourcesList      = transport.MethodResourcesList
	MethodResourcesRead      = transport.MethodResourcesRead
	MethodResourcesSubscribe = transport.MethodResourcesSubscribe
	MethodPromptsList        = transport.MethodPromptsList
	MethodPromptsGet         = transport.MethodPromptsGet

	MethodRootsList             = transport.MethodRootsList
	MethodSamplingCreateMessage = transport.MethodSamplingCreateMessage

	NotificationInitialized          = transport.NotificationInitialized
	NotificationCancelled            = transport.NotificationCancelled
	NotificationProgress             = transport.NotificationProgress
	NotificationMessage              = transport.NotificationMessage
	NotificationToolsListChanged     = transport.NotificationToolsListChanged
	NotificationResourcesListChanged = transport.NotificationResourcesListChanged
	NotificationResourcesUpdated     = transport.NotificationResourcesUpdated
	NotificationPromptsListChanged   = transport.NotificationPromptsListChanged

	ErrCodeParseError     = transport.ErrCodeParseError
	ErrCodeInvalidRequest = transport.ErrCodeInvalidRequest
	ErrCodeMethodNotFound = transport.ErrCodeMethodNotFound
	ErrCodeInvalidParams  = transport.ErrCodeInvalidParams
	ErrCodeInternalError  = transport.ErrCodeInternalError
)

// Type aliases re-exporting transport types so existing call sites
// in this package and downstream tests keep their original spellings.
type (
	RawMessage           = transport.RawMessage
	RPCError             = transport.RPCError
	InitializeParams     = transport.InitializeParams
	InitializeResult     = transport.InitializeResult
	ClientCapabilities   = transport.ClientCapabilities
	ServerCapabilities   = transport.ServerCapabilities
	RootsCapability      = transport.RootsCapability
	SamplingCapability   = transport.SamplingCapability
	Implementation       = transport.Implementation
	ToolsCapability      = transport.ToolsCapability
	ResourcesCapability  = transport.ResourcesCapability
	PromptsCapability    = transport.PromptsCapability
	LoggingCapability    = transport.LoggingCapability
	ToolDefinition       = transport.ToolDefinition
	ToolsListResult      = transport.ToolsListResult
	ToolsCallParams      = transport.ToolsCallParams
	ToolsCallResult      = transport.ToolsCallResult
	ResourceDefinition   = transport.ResourceDefinition
	ResourcesListResult  = transport.ResourcesListResult
	ResourcesReadParams  = transport.ResourcesReadParams
	ResourceContent      = transport.ResourceContent
	ResourcesReadResult  = transport.ResourcesReadResult
	PromptDefinition     = transport.PromptDefinition
	PromptArgument       = transport.PromptArgument
	PromptsListResult    = transport.PromptsListResult
	PromptsGetParams     = transport.PromptsGetParams
	PromptsGetResult     = transport.PromptsGetResult
	PingResult           = transport.PingResult
	Root                 = transport.Root
	RootsListResult      = transport.RootsListResult
	SamplingMessage      = transport.SamplingMessage
	SamplingRequest      = transport.SamplingRequest
	SamplingResponse     = transport.SamplingResponse
	CancelledParams      = transport.CancelledParams
	ProgressParams       = transport.ProgressParams
	MessageParams        = transport.MessageParams
	ResourcesUpdatedParams = transport.ResourcesUpdatedParams

	SamplingHandler = transport.SamplingHandler
	RootsHandler    = transport.RootsHandler
	EventPublisher  = transport.EventPublisher

	Framer            = transport.Framer
	ResponseRouter    = transport.ResponseRouter
	RingBuffer        = transport.RingBuffer
	RecipeStatus      = transport.RecipeStatus
	Ticker            = transport.Ticker
	ProgressEvent     = transport.ProgressEvent
	ProgressForwarder = transport.ProgressForwarder
	LogSink           = transport.LogSink
)

// Function and constant re-exports used by the supervisor /
// progress / log paths inside this package and by tests.
var (
	NewFramer            = transport.NewFramer
	NewResponseRouter    = transport.NewResponseRouter
	NewRingBuffer        = transport.NewRingBuffer
	NewProgressForwarder = transport.NewProgressForwarder
	NewLogSink           = transport.NewLogSink
	mapLogLevel          = transport.MapLogLevel
	IsSkipped            = transport.IsSkipped
	ErrFrameTooLarge     = transport.ErrFrameTooLarge
	LLMSamplingHandler   = transport.LLMSamplingHandler
	DefaultRoots         = transport.DefaultRoots
	pruneHistory         = transport.PruneHistory
)

// Aliased capacities so tests can keep referencing them by short
// name.
const (
	MaxFrameBytes         = transport.MaxFrameBytes
	RingBufferSize        = transport.RingBufferSize
	StatusStderrTailBytes = transport.StatusStderrTailBytes
	ProgressTopic         = transport.ProgressTopic
)

// requestEnvelope / notificationEnvelope / responseEnvelope are
// thin internal aliases — the transport package owns the public
// type, but using a local lowercase alias keeps the call sites
// here readable and matches the original stdio source.
type (
	requestEnvelope      = transport.RequestEnvelope
	notificationEnvelope = transport.NotificationEnvelope
	responseEnvelope     = transport.ResponseEnvelope
)

// ServerInstance is the in-memory state for one stdio MCP server.
// WP02 re-hosts the lifecycle on top of a stdio.Connection — the
// `transport.Connection` impl that owns the bytes-layer (subprocess
// + framer + stderr ring buffer). The instance still drives the
// MCP-level handshake, the response router, and the supervisor
// restart machinery; the connection just supplies the wire.
type ServerInstance struct {
	id     string
	logger *slog.Logger

	stderr *RingBuffer

	router *ResponseRouter
	nextID atomic.Int64

	// closeOnce guards Close from running its sequence twice.
	closeOnce sync.Once
	closeErr  error

	// doneCh is closed after Close completes; supervisor / health
	// goroutines select on it to exit.
	doneCh chan struct{}

	// closing reports whether Close has begun. Reader/writer use it
	// to suppress crash signalling during planned shutdown.
	closing atomic.Bool

	// supervisorWG tracks the supervisor + healthPinger goroutines
	// so Close can wait for them.
	supervisorWG sync.WaitGroup

	// mu guards negotiated / tools / initialized fields read from
	// upstream callers (pool.Tools, status snapshots).
	mu          sync.RWMutex
	negotiated  InitializeResult
	tools       []coremcp.Tool
	initialized bool

	// lifecycleMu guards every spawn-mutated field (conn, cmd,
	// stdin, framer, readerWG, crashCh, state, restartHistory,
	// lastError) so the supervisor's restart cycle is atomic
	// relative to callers and to status snapshots.
	//
	// cmd / stdin / framer mirror the underlying *Connection's
	// handles. They're kept here so the existing test surface (and
	// the supervisor's tear-down path) can reach the live process
	// without going through the connection accessor on every read.
	lifecycleMu    sync.Mutex
	conn           *Connection
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	framer         *Framer
	readerWG       sync.WaitGroup
	crashCh        chan struct{}
	crashOnce      *sync.Once
	state          State
	restartHistory []time.Time
	lastError      string
	lastRestartAt  time.Time

	// spec / opts are captured at the first Spawn so the supervisor
	// can re-spawn with identical configuration.
	spec     SpawnSpec
	opts     instanceOptions
	hasSpawn bool

	// Handlers populated by the pool from PoolOptions. nil = WP01-
	// safe no-op. WP03 wires real implementations.
	sampling SamplingHandler
	roots    RootsHandler
	publish  EventPublisher

	// WP03 helpers built atop the handlers above. progress owns the
	// request-id ↔ progressToken correlation table; logSink folds
	// notifications/message frames onto slog.
	progress *ProgressForwarder
	logSink  *LogSink

	samplingOn bool
}

// instanceOptions are the WP02 hooks the pool injects into each
// ServerInstance. Defaults wire to the real time package; tests
// override them to fast-forward without wallclock waits.
type instanceOptions struct {
	// Now returns the current time. Used for restartHistory pruning
	// and LastRestartAt timestamps.
	Now func() time.Time

	// Sleep blocks for d. Used by the supervisor's backoff between
	// restart attempts so tests can swap in a no-op or a recordable
	// stub.
	Sleep func(d time.Duration)

	// NewTicker returns a ticker that fires at the requested period.
	// Used by healthPinger; tests can return a manually-driven
	// ticker to control the cadence deterministically.
	NewTicker func(d time.Duration) Ticker
}

// defaultInstanceOptions returns options wired to wallclock time.
func defaultInstanceOptions() instanceOptions {
	return instanceOptions{
		Now:       time.Now,
		Sleep:     time.Sleep,
		NewTicker: transport.NewRealTicker,
	}
}

// newServerInstance builds an unspawned instance. The pool calls
// this then Spawn(). opts is the WP02 clock-injection bundle; pass
// the zero value (or nil-fielded value) and the constructor fills
// in the wallclock defaults.
func newServerInstance(id string, logger *slog.Logger, sampling SamplingHandler, roots RootsHandler, publish EventPublisher, opts instanceOptions) *ServerInstance {
	if logger == nil {
		logger = slog.Default()
	}
	scoped := logger.With("mcp.recipe", id)
	def := defaultInstanceOptions()
	if opts.Now == nil {
		opts.Now = def.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = def.Sleep
	}
	if opts.NewTicker == nil {
		opts.NewTicker = def.NewTicker
	}
	return &ServerInstance{
		id:       id,
		logger:   scoped,
		router:   NewResponseRouter(logger),
		stderr:   NewRingBuffer(RingBufferSize),
		doneCh:   make(chan struct{}),
		sampling: sampling,
		roots:    roots,
		publish:  publish,
		progress: NewProgressForwarder(id, publish, scoped),
		logSink:  NewLogSink(scoped),
		state:    StateStopped,
		opts:     opts,
	}
}

// Spawn starts the child process, performs the initialize
// handshake, and (on success) leaves reader/stderr-pump goroutines
// running plus the WP02 supervisor + healthPinger. On any failure
// the process is reaped and Spawn returns the error — the instance
// is unusable past that point. First-init failure does NOT trigger
// auto-restart (per spec.md §9 edge case 2); only post-init
// crashes do.
func (s *ServerInstance) Spawn(ctx context.Context, spec SpawnSpec) error {
	if len(spec.Command) == 0 {
		return errors.New("stdio: empty command")
	}

	s.lifecycleMu.Lock()
	s.state = StateStarting
	s.spec = spec
	s.hasSpawn = true
	s.samplingOn = spec.SamplingEnabled
	s.lifecycleMu.Unlock()

	if err := s.doSpawn(ctx, spec); err != nil {
		s.lifecycleMu.Lock()
		s.state = StateFailed
		s.lastError = err.Error()
		s.lifecycleMu.Unlock()
		return err
	}

	s.lifecycleMu.Lock()
	s.state = StateRunning
	s.lifecycleMu.Unlock()

	// Launch the supervisor + healthPinger. Both run for the life
	// of the instance, surviving (in fact driving) restart cycles.
	s.supervisorWG.Add(1)
	go s.runSupervisor()
	if s.pingPeriod() > 0 {
		s.supervisorWG.Add(1)
		go s.healthPinger()
	}
	return nil
}

// doSpawn performs one spawn attempt: open a fresh Connection
// (which executes the command, wires pipes, and starts the stderr
// pump), then drives the MCP initialize handshake and starts the
// reader loop. Caller holds responsibility for state transitions;
// doSpawn does not touch the supervisor goroutines.
//
// The Connection is the WP02 abstraction the per-transport pools
// share — when WP03/WP04 land, the same handshake-and-router code
// can drive an HTTP or SSE bytes layer simply by swapping the
// Connection implementation.
func (s *ServerInstance) doSpawn(ctx context.Context, spec SpawnSpec) error {
	initTimeout := spec.InitTimeout
	if initTimeout <= 0 {
		initTimeout = defaultInitTimeout
	}

	// Build and open the bytes-layer Connection. The Connection
	// owns the subprocess, the framer, and the stderr pump; once
	// Open returns the wire is ready for an MCP initialize.
	conn := NewConnection(spec, s.logger)
	// Reuse the instance-scoped ring buffer so the legacy
	// StderrTail accessor keeps returning the same buffer the
	// stderr pump fills, even across restart cycles.
	conn.stderr = s.stderr
	if err := conn.Open(ctx); err != nil {
		return err
	}

	// Mirror the Connection's handles into the lifecycle fields
	// under lifecycleMu so the supervisor / status snapshots / test
	// hooks (Cmd, killProcessForTest) observe a coherent
	// generation. cmd / stdin / framer all derive from conn — they
	// are pointer mirrors, not independent state.
	s.lifecycleMu.Lock()
	s.conn = conn
	s.cmd = conn.Cmd()
	s.stdin = conn.Stdin()
	s.framer = conn.Framer()
	s.crashCh = make(chan struct{})
	s.crashOnce = &sync.Once{}
	s.lifecycleMu.Unlock()

	// Run initialize before the reader loop so we can surface the
	// handshake error directly. doInitialize starts the reader
	// loop on success so post-init bookkeeping (tools/list refresh)
	// can rely on the router.
	if err := s.doInitialize(ctx, initTimeout, spec.SamplingEnabled); err != nil {
		// Tear the connection down. Connection.Close handles the
		// stdin-close / SIGKILL / Wait + stderr-pump drain so we
		// don't have to replay the sequence here.
		_ = conn.Close()
		s.readerWG.Wait()
		return err
	}
	return nil
}

// doInitialize performs the initialize handshake synchronously.
// initialize is unique among methods: we have to read the first
// response off the wire before the reader loop can take ownership.
func (s *ServerInstance) doInitialize(ctx context.Context, timeout time.Duration, sampling bool) error {
	caps := ClientCapabilities{
		Roots: &RootsCapability{ListChanged: false},
	}
	if sampling {
		caps.Sampling = &SamplingCapability{}
	}
	id := s.nextID.Add(1)
	req := requestEnvelope{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  MethodInitialize,
		Params: InitializeParams{
			ProtocolVersion: SupportedProtocolVersion,
			Capabilities:    caps,
			ClientInfo:      Implementation{Name: ClientName, Version: ClientVersion},
		},
	}
	if err := s.framer.Write(req); err != nil {
		return fmt.Errorf("stdio: write initialize: %w", err)
	}

	// Read responses synchronously until we see one matching our id
	// or hit the deadline. Skip non-JSON banners and any rogue
	// notifications the server emits before the handshake completes.
	// The reader goroutine and select-on-deadline pair below avoids
	// blocking indefinitely on a server that silently swallows
	// initialize. On timeout/cancel, the caller of Spawn closes
	// stdin and kills the child, which unblocks the goroutine on
	// io.EOF and lets it exit cleanly.
	deadline := time.Now().Add(timeout)
	type readResult struct {
		msg RawMessage
		err error
	}
	resCh := make(chan readResult, 1)
	go func() {
		for {
			msg, err := s.framer.Read()
			if err != nil {
				if IsSkipped(err) {
					continue
				}
				select {
				case resCh <- readResult{err: err}:
				default:
				}
				return
			}
			if !msg.IsResponse() {
				s.logger.Debug("stdio.preinit_message", "method", msg.Method)
				continue
			}
			select {
			case resCh <- readResult{msg: msg}:
			default:
			}
			return
		}
	}()

	var msg RawMessage
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(deadline)):
		return fmt.Errorf("stdio: initialize timeout after %s", timeout)
	case r := <-resCh:
		if r.err != nil {
			return fmt.Errorf("stdio: read initialize: %w", r.err)
		}
		msg = r.msg
	}

	// Validate id matches.
	if !idMatches(msg.ID, id) {
		return fmt.Errorf("stdio: initialize id mismatch: %s", string(*msg.ID))
	}
	if msg.Error != nil {
		return fmt.Errorf("stdio: initialize error: %s", msg.Error.Message)
	}

	var result InitializeResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return fmt.Errorf("stdio: decode initialize result: %w", err)
	}

	// Send notifications/initialized to complete the handshake.
	if err := s.framer.Write(notificationEnvelope{
		JSONRPC: JSONRPCVersion,
		Method:  NotificationInitialized,
	}); err != nil {
		return fmt.Errorf("stdio: write initialized: %w", err)
	}

	s.mu.Lock()
	s.negotiated = result
	s.initialized = true
	s.mu.Unlock()

	// Hand the framer over to the reader loop now that the
	// handshake response is consumed; subsequent inbound frames
	// (including the response to our initial tools/list) flow
	// through the router. The reader captures the framer pointer
	// at goroutine start so a restart that swaps in a new framer
	// doesn't stall the previous reader on the wrong descriptor.
	s.readerWG.Add(1)
	go s.readLoop(s.framer)

	// Eagerly fetch tools/list when the server advertises tools, so
	// Pool.Tools is non-blocking. Failures are logged but do not
	// fail the spawn.
	if result.Capabilities.Tools != nil {
		toolsCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := s.refreshTools(toolsCtx); err != nil {
			s.logger.Warn("stdio.initial_tools_list", "err", err.Error())
		}
	}
	return nil
}

// refreshTools issues tools/list and caches the result. WP02
// extends this to react to notifications/tools/list_changed.
func (s *ServerInstance) refreshTools(ctx context.Context) error {
	resp, err := s.callRaw(ctx, MethodToolsList, nil)
	if err != nil {
		return err
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("stdio: decode tools/list: %w", err)
	}
	tools := make([]coremcp.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, coremcp.Tool{
			Server:      s.id,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()
	return nil
}

// Tools returns the cached tool list. Returns nil before the
// handshake completes.
func (s *ServerInstance) Tools() []coremcp.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]coremcp.Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// CallTool issues tools/call and returns the raw result envelope
// (already unwrapped from the JSON-RPC response). ctx cancellation
// sends notifications/cancelled and returns ctx.Err().
//
// Before serialising the request, any @secret: references in the args
// JSON are substituted via refs.ResolverFromContext (WP09). When no
// resolver is wired in ctx the args pass through unchanged.
func (s *ServerInstance) CallTool(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
	// ── @secret: reference substitution (WP09) ──────────────────────────
	// Walk the args JSON string values and substitute any @secret: tokens.
	// We operate on the raw JSON text; the substitution is string-level.
	resolver := refs.ResolverFromContext(ctx)
	if resolver != nil && refs.HasReference(string(args)) {
		rctx := cedar.ResolveContext{ToolName: "mcp:" + tool}
		sub, _, err := resolver.Substitute(ctx, string(args), rctx)
		if err != nil {
			return nil, fmt.Errorf("mcp.CallTool: secret resolution failed for tool %q: %w", tool, err)
		}
		args = json.RawMessage(sub)
	}
	return s.CallToolWithProgress(ctx, tool, args, "")
}

// CallToolWithProgress is the progress-aware variant of CallTool.
// When progressToken is non-empty the pool injects `_meta.progressToken`
// into the outbound tools/call params and registers the token with
// the ProgressForwarder so subsequent notifications/progress frames
// are correlated to this request id.
func (s *ServerInstance) CallToolWithProgress(ctx context.Context, tool string, args json.RawMessage, progressToken string) (json.RawMessage, error) {
	type toolsCallParamsWithMeta struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Meta      map[string]any  `json:"_meta,omitempty"`
	}
	params := toolsCallParamsWithMeta{Name: tool, Arguments: args}
	if progressToken != "" {
		params.Meta = map[string]any{"progressToken": progressToken}
	}
	return s.callRawWithProgress(ctx, MethodToolsCall, params, progressToken, tool)
}

// callRaw is the inner request/response primitive. Assigns a fresh
// id, registers a router channel, writes the envelope, blocks on
// the channel or ctx.Done, and on cancellation emits
// notifications/cancelled before returning.
func (s *ServerInstance) callRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return s.callRawWithProgress(ctx, method, params, "", "")
}

// callRawWithProgress is callRaw with optional progress-token
// registration. Empty token means "no correlation" — the path is
// otherwise identical to callRaw, including the cancellation
// notification on ctx.Done.
func (s *ServerInstance) callRawWithProgress(ctx context.Context, method string, params any, progressToken, tool string) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	ch := s.router.Register(id)
	if progressToken != "" {
		s.progress.Register(id, progressToken, tool)
	}
	cleanup := func() {
		s.router.Cancel(id)
		if progressToken != "" {
			s.progress.Forget(id)
		}
	}
	req := requestEnvelope{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}
	// Snapshot the framer so a concurrent restart that swaps in a
	// new framer can't have us write to a freshly-opened stdin
	// after the router cancelled us.
	s.lifecycleMu.Lock()
	framer := s.framer
	s.lifecycleMu.Unlock()
	if framer == nil {
		cleanup()
		return nil, errors.New("stdio: server not running")
	}
	if err := framer.Write(req); err != nil {
		cleanup()
		s.signalCrash("write " + method + ": " + errString(err))
		return nil, fmt.Errorf("stdio: write %s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		cleanup()
		// Best-effort cancellation notification; failure to write
		// (server already gone) shouldn't override ctx.Err().
		_ = framer.Write(notificationEnvelope{
			JSONRPC: JSONRPCVersion,
			Method:  NotificationCancelled,
			Params:  CancelledParams{RequestID: idAsRaw(id), Reason: "ctx cancelled"},
		})
		return nil, ctx.Err()
	case <-s.doneCh:
		cleanup()
		return nil, errors.New("stdio: server closed")
	case env, ok := <-ch:
		if !ok {
			// Channel closed by Cancel/CancelAll — caller should see
			// ctx.Err or doneCh on the next select pass, but cover
			// the race for safety.
			if progressToken != "" {
				s.progress.Forget(id)
			}
			return nil, errors.New("stdio: call cancelled")
		}
		cleanup()
		if env.Error != nil {
			return nil, env.Error
		}
		return env.Result, nil
	}
}

// readLoop pulls envelopes off the framer and dispatches them.
// Responses go to the router. Notifications are logged. Server-
// initiated requests get a -32601 stub today; WP03 wires real
// dispatch to PoolOptions handlers. EOF / read errors close the
// generation's crashCh so the supervisor can decide whether to
// restart (T011).
func (s *ServerInstance) readLoop(framer *Framer) {
	defer s.readerWG.Done()
	for {
		msg, err := framer.Read()
		if err != nil {
			if IsSkipped(err) {
				continue
			}
			if errors.Is(err, io.EOF) {
				s.logger.Debug("stdio.reader.eof")
			} else {
				s.logger.Warn("stdio.reader.error", "err", err.Error())
			}
			s.router.CancelAll()
			s.signalCrash("reader: " + errString(err))
			return
		}
		switch {
		case msg.IsResponse():
			id, ok := parseInt64ID(msg.ID)
			if !ok {
				s.logger.Warn("stdio.reader.non_numeric_response_id", "id", string(*msg.ID))
				continue
			}
			s.router.Deliver(id, msg)
		case msg.IsRequest():
			s.handleServerRequest(msg)
		default:
			// Notification: id absent, method present.
			s.handleNotification(msg)
		}
	}
}

// handleServerRequest is the WP01 stub for roots/list and
// sampling/createMessage. Real handler dispatch lands in WP03; for
// now we respond with -32601 (Method not found) when no handler is
// registered, or pass through to the handler and reflect its
// response shape onto the wire.
func (s *ServerInstance) handleServerRequest(msg RawMessage) {
	respond := func(result any, rpcErr *RPCError) {
		if msg.ID == nil {
			return
		}
		_ = s.framer.Write(responseEnvelope{
			JSONRPC: JSONRPCVersion,
			ID:      *msg.ID,
			Result:  result,
			Error:   rpcErr,
		})
	}

	switch msg.Method {
	case MethodRootsList:
		if s.roots == nil {
			respond(RootsListResult{Roots: []Root{}}, nil)
			return
		}
		roots, err := s.roots.Roots(context.Background(), s.id)
		if err != nil {
			respond(nil, &RPCError{Code: ErrCodeInternalError, Message: err.Error()})
			return
		}
		if roots == nil {
			roots = []Root{}
		}
		respond(RootsListResult{Roots: roots}, nil)
	case MethodSamplingCreateMessage:
		// Per-server gate. Default is OFF (cost-amplification risk:
		// see sampling.go for the consent-boundary note). When the
		// gate is off we MUST NOT invoke the handler — the test path
		// asserts the handler counter stays at 0.
		s.mu.RLock()
		on := s.samplingOn
		s.mu.RUnlock()
		if !on {
			respond(nil, &RPCError{Code: ErrCodeMethodNotFound, Message: "sampling disabled for this server"})
			return
		}
		if s.sampling == nil {
			respond(nil, &RPCError{Code: ErrCodeMethodNotFound, Message: "sampling disabled for this server"})
			return
		}
		var req SamplingRequest
		if len(msg.Params) > 0 {
			if err := json.Unmarshal(msg.Params, &req); err != nil {
				respond(nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()})
				return
			}
		}
		resp, err := s.sampling.CreateMessage(context.Background(), s.id, req)
		if err != nil {
			respond(nil, &RPCError{Code: ErrCodeInternalError, Message: err.Error()})
			return
		}
		respond(resp, nil)
	default:
		respond(nil, &RPCError{Code: ErrCodeMethodNotFound, Message: "unknown method: " + msg.Method})
	}
}

// handleNotification dispatches inbound notifications to their
// side-effect handlers. tools/list_changed triggers a tool-cache
// refresh; progress and message notifications flow through the WP03
// helpers so they reach the broker / slog. Best-effort: a malformed
// payload is logged at debug and dropped, never crashes the reader.
func (s *ServerInstance) handleNotification(msg RawMessage) {
	switch msg.Method {
	case NotificationToolsListChanged:
		// Refresh tools cache asynchronously so the reader loop
		// keeps draining. ctx.Background is fine — the request will
		// be cancelled by the server going away or the pool closing.
		go func() {
			if err := s.refreshTools(context.Background()); err != nil {
				s.logger.Warn("stdio.tools_refresh", "err", err.Error())
			}
		}()
	case NotificationProgress:
		s.progress.Handle(json.RawMessage(msg.Params))
	case NotificationMessage:
		s.logSink.Handle(context.Background(), s.id, json.RawMessage(msg.Params))
	default:
		s.logger.Debug("stdio.notification", "method", msg.Method)
	}
}

// StderrTail returns up to maxBytes of the most recent stderr
// content. The Connection's stderr pump fills the same RingBuffer
// referenced here (set on doSpawn via conn.stderr = s.stderr) so
// the snapshot stays consistent across restart cycles.
func (s *ServerInstance) StderrTail(maxBytes int) string {
	return s.stderr.Snapshot(maxBytes)
}

// Negotiated returns the post-handshake server info. Empty before
// initialize completes.
func (s *ServerInstance) Negotiated() InitializeResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.negotiated
}

// Close closes stdin (graceful exit nudge), waits up to 2 s for the
// process to exit on its own, then SIGKILLs and reaps. All reader
// goroutines (including the WP02 supervisor + healthPinger) have
// exited by the time Close returns. Idempotent.
//
// The actual subprocess teardown is delegated to Connection.Close,
// which owns the stdin-close → grace-wait → SIGKILL → stderr-pump
// drain sequence. ServerInstance.Close adds the MCP-level
// bookkeeping: cancel pending router calls, signal supervisor /
// healthPinger to exit via doneCh, then wait for both.
func (s *ServerInstance) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		// Mark the instance as closing so the reader/writer paths
		// don't fire crash signals as the process tears down.
		s.closing.Store(true)

		// Closing the doneCh unblocks the supervisor's main select
		// (and the healthPinger's). Doing this BEFORE killing the
		// process ensures the supervisor doesn't race to start a
		// restart while we're in the middle of tearing down.
		close(s.doneCh)

		// Snapshot the live Connection under the lifecycle lock so
		// we don't race with an in-flight restart.
		s.lifecycleMu.Lock()
		conn := s.conn
		s.lifecycleMu.Unlock()

		// Cancel pending requests so callers unblock immediately.
		s.router.CancelAll()

		// Connection.Close owns the stdin-close + SIGKILL + Wait +
		// stderr-pump drain. We respect a tighter ctx deadline by
		// racing Close against a deadline ticker.
		closeErrCh := make(chan error, 1)
		if conn != nil {
			go func() { closeErrCh <- conn.Close() }()
		} else {
			close(closeErrCh)
		}

		grace := closeGrace + time.Second // slack over Connection's own grace
		if dl, ok := ctx.Deadline(); ok {
			if d := time.Until(dl); d < grace {
				grace = d
			}
		}
		select {
		case err := <-closeErrCh:
			s.closeErr = err
		case <-time.After(grace):
			// Connection.Close hasn't returned in time — force the
			// process down and wait. Connection's internal timer
			// will SIGKILL on the next tick; this branch just
			// surfaces the error.
			if conn != nil {
				cmd := conn.Cmd()
				if cmd != nil && cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
			s.closeErr = <-closeErrCh
		}
		s.readerWG.Wait()
		s.supervisorWG.Wait()

		s.lifecycleMu.Lock()
		s.state = StateStopped
		s.lifecycleMu.Unlock()
	})
	return s.closeErr
}

// signalCrash closes the current generation's crashCh exactly once.
// The reader, writer, and healthPinger all funnel through here.
// Idempotent across the lifetime of one spawn — once the supervisor
// reacts and respawns, a fresh crashCh is allocated under
// lifecycleMu so the next crash hits a different channel.
//
// Crash signals raised during Close are dropped: the closing flag
// keeps the supervisor from racing to restart a process the user
// just asked us to kill. Likewise, signals raised while the
// supervisor is already mid-restart (state=restarting) are
// dropped — those are spurious writer-failures against the dying
// pipe of the previous generation.
func (s *ServerInstance) signalCrash(reason string) {
	if s.closing.Load() {
		return
	}
	s.lifecycleMu.Lock()
	if s.state == StateRestarting || s.state == StateFailed || s.state == StateStopped {
		s.lifecycleMu.Unlock()
		return
	}
	once := s.crashOnce
	ch := s.crashCh
	s.lastError = reason
	s.lifecycleMu.Unlock()
	if once == nil || ch == nil {
		return
	}
	once.Do(func() {
		close(ch)
	})
}

// pingPeriod returns the configured health-ping cadence, defaulting
// to defaultPingPeriod when the spec leaves it zero. A negative
// value disables health pings entirely (used by tests that drive
// the ping path manually).
func (s *ServerInstance) pingPeriod() time.Duration {
	if s.spec.PingPeriod < 0 {
		return -1
	}
	if s.spec.PingPeriod == 0 {
		return defaultPingPeriod
	}
	return s.spec.PingPeriod
}

// pingTimeout returns the per-ping deadline, defaulting to
// defaultPingTimeout when the spec leaves it zero.
func (s *ServerInstance) pingTimeout() time.Duration {
	if s.spec.PingTimeout > 0 {
		return s.spec.PingTimeout
	}
	return defaultPingTimeout
}

// errString returns err.Error() or "<nil>" for nil.
func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// firstByteReader wraps an io.Reader and applies a deadline to the
// FIRST Read call only. Subsequent reads are unbounded — the
// reader loop has its own logic for handling EOF/error.
//
// Used to enforce the npx cold-spawn first-byte ceiling without
// holding a deadline over the entire session.
type firstByteReader struct {
	inner    io.ReadCloser
	timeout  time.Duration
	firstMu  sync.Mutex
	firstHit bool
}

func newFirstByteReader(inner io.ReadCloser, timeout time.Duration) io.Reader {
	return &firstByteReader{inner: inner, timeout: timeout}
}

func (r *firstByteReader) Read(p []byte) (int, error) {
	r.firstMu.Lock()
	first := !r.firstHit
	r.firstHit = true
	r.firstMu.Unlock()
	if !first {
		return r.inner.Read(p)
	}
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	go func() {
		n, err := r.inner.Read(p)
		ch <- res{n, err}
	}()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-time.After(r.timeout):
		// Closing inner unblocks the goroutine on the next read; the
		// pending result is dropped.
		_ = r.inner.Close()
		<-ch
		return 0, fmt.Errorf("stdio: first-byte timeout after %s", r.timeout)
	}
}

// idMatches compares an on-wire id (raw JSON) against an int64.
func idMatches(raw *json.RawMessage, want int64) bool {
	got, ok := parseInt64ID(raw)
	return ok && got == want
}

// parseInt64ID extracts an int64 out of a raw JSON id. Returns
// false on string ids (the spec allows them but our pool only
// generates numeric ones).
func parseInt64ID(raw *json.RawMessage) (int64, bool) {
	if raw == nil {
		return 0, false
	}
	v, err := strconv.ParseInt(string(*raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// idAsRaw renders an int64 id as a json.RawMessage for use in
// notifications/cancelled.
func idAsRaw(id int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(id, 10))
}

// mergeEnv merges spec env over the inherited process env. An
// empty value clears the key from the inherited set.
func mergeEnv(extra map[string]string) []string {
	base := append([]string(nil), os.Environ()...)
	if len(extra) == 0 {
		return base
	}
	indexed := make(map[string]int, len(base))
	for i, kv := range base {
		eq := -1
		for j := 0; j < len(kv); j++ {
			if kv[j] == '=' {
				eq = j
				break
			}
		}
		if eq < 0 {
			continue
		}
		indexed[kv[:eq]] = i
	}
	for k, v := range extra {
		entry := k + "=" + v
		if i, ok := indexed[k]; ok {
			if v == "" {
				base[i] = "" // mark for removal
				continue
			}
			base[i] = entry
		} else if v != "" {
			base = append(base, entry)
		}
	}
	// Compact empty entries.
	out := base[:0]
	for _, kv := range base {
		if kv != "" {
			out = append(out, kv)
		}
	}
	return out
}
