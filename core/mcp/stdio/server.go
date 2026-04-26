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

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
)

// Default deadlines used by the lifecycle code paths.
const (
	// defaultFirstByteTimeout caps the wait for the first byte on
	// stdout after exec, covering an `npx -y …` cold-fetch. Distinct
	// from initialize's response deadline — research §"npx -y cold-
	// spawn behavior".
	defaultFirstByteTimeout = 30 * time.Second

	// defaultInitTimeout is the response deadline once the
	// initialize request is on the wire.
	defaultInitTimeout = 5 * time.Second

	// closeGrace is how long Close waits between SIGTERM and SIGKILL.
	closeGrace = 2 * time.Second
)

// SpawnSpec is the input to ServerInstance.Spawn. It mirrors the
// subset of recipe fields the lifecycle needs without pulling
// core/mcp/recipes (WP04) into the WP01 dependency graph.
type SpawnSpec struct {
	// ID identifies the server in logs and on Tool.Server. The pool
	// uses ServerSpec.Name, which the recipe maps to its ID.
	ID string

	// Command is the argv vector of the child process. Command[0]
	// is resolved against PATH.
	Command []string

	// Env is the additional environment merged on top of the
	// process's existing environment. Empty values clear a key.
	Env map[string]string

	// FirstByteTimeout overrides defaultFirstByteTimeout when > 0.
	FirstByteTimeout time.Duration

	// InitTimeout overrides defaultInitTimeout when > 0.
	InitTimeout time.Duration

	// SamplingEnabled tells the pool whether to advertise the
	// sampling capability during initialize. WP03 wires the actual
	// handler dispatch.
	SamplingEnabled bool
}

// ServerInstance is the in-memory state for one stdio MCP server.
// WP01 implements Spawn → Initialize → Close + tool calls. WP02
// adds restart/health, WP03 adds server-initiated request handling.
type ServerInstance struct {
	id     string
	logger *slog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	framer *Framer
	stderr *RingBuffer

	router *ResponseRouter
	nextID atomic.Int64

	// readerWG waits for the reader and stderr-pump goroutines.
	readerWG sync.WaitGroup
	doneCh   chan struct{}

	// closeOnce guards Close from running its sequence twice.
	closeOnce sync.Once
	closeErr  error

	// negotiated is the server's response to initialize, captured
	// after a successful handshake.
	mu          sync.RWMutex
	negotiated  InitializeResult
	tools       []coremcp.Tool
	initialized bool

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

// newServerInstance builds an unspawned instance. The pool calls
// this then Spawn().
func newServerInstance(id string, logger *slog.Logger, sampling SamplingHandler, roots RootsHandler, publish EventPublisher) *ServerInstance {
	if logger == nil {
		logger = slog.Default()
	}
	scoped := logger.With("mcp.recipe", id)
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
	}
}

// Spawn starts the child process, performs the initialize
// handshake, and (on success) leaves reader/stderr-pump goroutines
// running. On any failure the process is reaped and Spawn returns
// the error — the instance is unusable past that point.
func (s *ServerInstance) Spawn(ctx context.Context, spec SpawnSpec) error {
	if len(spec.Command) == 0 {
		return errors.New("stdio: empty command")
	}
	firstByte := spec.FirstByteTimeout
	if firstByte <= 0 {
		firstByte = defaultFirstByteTimeout
	}
	initTimeout := spec.InitTimeout
	if initTimeout <= 0 {
		initTimeout = defaultInitTimeout
	}
	s.samplingOn = spec.SamplingEnabled

	// exec.Command (not CommandContext) detaches the process
	// lifetime from the caller's ctx — the spawned server should
	// outlive Open and only die via the explicit Close path.
	// Cancellation of the spawn ctx still reaches the handshake via
	// the doInitialize select.
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Env = mergeEnv(spec.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdio: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stdio: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("stdio: start %q: %w", spec.Command[0], err)
	}
	s.cmd = cmd
	s.stdin = stdin
	s.framer = NewFramer(newFirstByteReader(stdout, firstByte), stdin)

	// Pump stderr into the ring buffer eagerly so initialize
	// failures still show useful context.
	s.readerWG.Add(1)
	go s.pumpStderr(stderr)

	// Run initialize before the reader loop so we can surface the
	// handshake error directly. doInitialize starts the reader
	// loop on success so post-init bookkeeping (tools/list refresh)
	// can rely on the router.
	if err := s.doInitialize(ctx, initTimeout, spec.SamplingEnabled); err != nil {
		// Tear the process down; we still need stderr-pump and Wait.
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		s.readerWG.Wait()
		close(s.doneCh)
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
	// through the router.
	s.readerWG.Add(1)
	go s.readLoop()

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
func (s *ServerInstance) CallTool(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
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
	if err := s.framer.Write(req); err != nil {
		cleanup()
		return nil, fmt.Errorf("stdio: write %s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		cleanup()
		// Best-effort cancellation notification; failure to write
		// (server already gone) shouldn't override ctx.Err().
		_ = s.framer.Write(notificationEnvelope{
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
// dispatch to PoolOptions handlers.
func (s *ServerInstance) readLoop() {
	defer s.readerWG.Done()
	for {
		msg, err := s.framer.Read()
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

// pumpStderr forwards every byte of the child's stderr into the
// ring buffer. Returns when the pipe is closed (process exit).
func (s *ServerInstance) pumpStderr(rc io.ReadCloser) {
	defer s.readerWG.Done()
	buf := make([]byte, 4*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			_, _ = s.stderr.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// StderrTail returns up to maxBytes of the most recent stderr
// content. Used by RecipeStatus in WP04.
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
// goroutines have exited by the time Close returns. Idempotent.
func (s *ServerInstance) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		// Closing stdin first asks the server to shut down via the
		// framing convention (EOF on stdin = "we're done").
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		// Cancel pending requests so callers unblock immediately.
		s.router.CancelAll()

		exitCh := make(chan error, 1)
		if s.cmd != nil {
			go func() { exitCh <- s.cmd.Wait() }()
		} else {
			close(exitCh)
		}

		// Use ctx deadline if it's tighter than closeGrace; else
		// fall back to closeGrace.
		grace := closeGrace
		if dl, ok := ctx.Deadline(); ok {
			if d := time.Until(dl); d < grace {
				grace = d
			}
		}
		select {
		case err := <-exitCh:
			s.closeErr = err
		case <-time.After(grace):
			if s.cmd != nil && s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			s.closeErr = <-exitCh
		}
		s.readerWG.Wait()
		close(s.doneCh)
	})
	return s.closeErr
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
