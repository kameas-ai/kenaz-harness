package stdio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// Connection is the stdio implementation of transport.Connection.
// It wraps the subprocess + Framer + stderr ring buffer behind the
// generic bytes-layer interface so the per-transport pool, the
// supervisor, and WP07's Test-Connection RPC can drive a stdio
// recipe without reaching for exec.Cmd directly.
//
// Lifecycle:
//
//   - NewConnection captures the SpawnSpec; nothing has been
//     allocated yet.
//   - Open spawns the child, wires stdin/stdout/stderr, starts the
//     stderr pump goroutine, and installs the first-byte deadline on
//     the inbound side. After Open returns, the connection is ready
//     for Send/Recv. Open is one-shot — pair it with Close, then
//     call NewConnection again for a fresh attempt.
//   - Send writes a JSON-RPC envelope through the Framer. Safe for
//     concurrent use.
//   - Recv reads the next inbound envelope via the Framer. Exactly
//     one goroutine should own the read side, per the Framer
//     contract.
//   - Close is idempotent. It nudges the child via stdin EOF, then
//     SIGKILLs after CloseGrace if the process is still alive,
//     waits for the stderr pump to drain, and reaps the cmd.
//
// PID and StderrTail return real values (process pid + ring-buffer
// snapshot) so the WP02 acceptance check holds: the supervisor's
// status snapshots and WP07's Test-Connection result both surface
// non-zero data for stdio recipes.
type Connection struct {
	spec   transport.SpawnSpec
	logger ConnectionLogger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	framer *transport.Framer
	stderr *transport.RingBuffer

	stderrWG sync.WaitGroup

	mu       sync.Mutex
	opened   bool
	closing  atomic.Bool
	closed   atomic.Bool
	closeErr error

	// pidSnapshot captures cmd.Process.Pid the moment Open returns
	// successfully. Reading it via PID() avoids touching cmd after
	// Close has reaped the process.
	pidSnapshot atomic.Int32
}

// ConnectionLogger is the minimal logger surface Connection uses
// so the spawn-failure paths emit structured diagnostics. Pass a
// *slog.Logger or nil; nil silences the warnings.
type ConnectionLogger interface {
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

// noopLogger satisfies ConnectionLogger with empty writes; used
// when callers pass nil.
type noopLogger struct{}

func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}

// NewConnection builds an unopened stdio Connection backed by the
// given SpawnSpec. The spec's Command must have at least one
// element; Open returns an error otherwise.
func NewConnection(spec transport.SpawnSpec, logger ConnectionLogger) *Connection {
	if logger == nil {
		logger = noopLogger{}
	}
	return &Connection{
		spec:   spec,
		logger: logger,
		stderr: transport.NewRingBuffer(transport.RingBufferSize),
	}
}

// compile-time witness that *Connection satisfies the cross-
// transport interface.
var _ transport.Connection = (*Connection)(nil)

// Open spawns the child, wires the framer + stderr pump, and
// applies the first-byte deadline. After Open returns nil, Send/
// Recv are usable; on error the caller should not call Close (the
// process has already been reaped).
func (c *Connection) Open(ctx context.Context) error {
	c.mu.Lock()
	if c.opened {
		c.mu.Unlock()
		return errors.New("stdio: connection already opened")
	}
	if len(c.spec.Command) == 0 {
		c.mu.Unlock()
		return errors.New("stdio: empty command")
	}
	c.opened = true
	spec := c.spec
	c.mu.Unlock()

	firstByte := spec.FirstByteTimeout
	if firstByte <= 0 {
		firstByte = transport.DefaultFirstByteTimeout
	}

	// exec.Command (not CommandContext) detaches the process
	// lifetime from ctx — the spawned server should outlive the
	// caller's context. The handshake above us still respects ctx
	// via doInitialize / one-shot deadlines.
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Env = childEnv(spec)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("stdio: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("stdio: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("stdio: start %q: %w", spec.Command[0], err)
	}

	// Wire bytes layer: framer with first-byte deadline guard.
	framer := transport.NewFramer(newFirstByteReader(stdout, firstByte), stdin)

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.framer = framer
	c.mu.Unlock()
	if cmd.Process != nil {
		c.pidSnapshot.Store(int32(cmd.Process.Pid))
	}

	// Pump stderr eagerly so initialization failures still leave
	// useful context in StderrTail.
	c.stderrWG.Add(1)
	go c.pumpStderr(stderrPipe)

	return nil
}

// Send writes v through the Framer. Returns an error if the
// connection has not been opened (or has been closed).
func (c *Connection) Send(v any) error {
	c.mu.Lock()
	framer := c.framer
	closed := c.closed.Load()
	c.mu.Unlock()
	if framer == nil || closed {
		return errors.New("stdio: connection not open")
	}
	return framer.Write(v)
}

// Recv reads the next inbound envelope. Returns io.EOF when the
// child has exited or Close has fired.
func (c *Connection) Recv() (transport.RawMessage, error) {
	c.mu.Lock()
	framer := c.framer
	c.mu.Unlock()
	if framer == nil {
		return transport.RawMessage{}, io.EOF
	}
	return framer.Read()
}

// Close shuts the connection down. Idempotent: the second and
// further calls return the first call's error.
func (c *Connection) Close() error {
	if c.closed.Load() {
		return c.closeErr
	}
	c.mu.Lock()
	if c.closed.Load() {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closing.Store(true)
	stdin := c.stdin
	cmd := c.cmd
	c.mu.Unlock()

	// Closing stdin first asks the server to shut down via the
	// framing convention (EOF on stdin = "we're done").
	if stdin != nil {
		_ = stdin.Close()
	}

	exitCh := make(chan error, 1)
	if cmd != nil {
		go func() { exitCh <- cmd.Wait() }()
	} else {
		close(exitCh)
	}

	var err error
	select {
	case err = <-exitCh:
	case <-time.After(transport.CloseGrace):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		err = <-exitCh
	}
	c.stderrWG.Wait()

	c.mu.Lock()
	c.closeErr = err
	c.closed.Store(true)
	// Drop the framer pointer so any post-Close Send returns the
	// "not open" error instead of writing into a dead pipe.
	c.framer = nil
	c.mu.Unlock()

	return err
}

// PID returns the child's process id when the connection has been
// opened; 0 otherwise. The snapshot is captured at Open and
// preserved across Close so callers can still attribute stderr
// content to a known pid post-mortem.
func (c *Connection) PID() int { return int(c.pidSnapshot.Load()) }

// StderrTail returns up to maxBytes of the most recent stderr
// content. Always returns a (possibly empty) string — never panics
// even if Open has not been called.
func (c *Connection) StderrTail(maxBytes int) string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.Snapshot(maxBytes)
}

// Framer exposes the wire-level framer so a higher-level driver
// (ServerInstance) can hand it to a reader goroutine without going
// through Recv. The returned framer is the same object referenced
// by Send/Recv; it remains valid until Close.
//
// This is an internal escape hatch used by the stdio ServerInstance
// — external callers should stick to Send/Recv.
func (c *Connection) Framer() *transport.Framer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.framer
}

// Cmd exposes the underlying *exec.Cmd. Internal accessor used by
// the ServerInstance restart machinery to surface PID + reap the
// process from outside the connection's own Close path.
func (c *Connection) Cmd() *exec.Cmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd
}

// Stdin exposes the writable end of the child. Internal accessor
// used by ServerInstance's restart path to nudge the previous
// generation closed before reaping.
func (c *Connection) Stdin() io.WriteCloser {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdin
}

// Stderr exposes the ring buffer. Internal accessor used by the
// ServerInstance so its existing StderrTail / RecipeStatus paths
// keep returning the same buffer they always did.
func (c *Connection) Stderr() *transport.RingBuffer { return c.stderr }

// pumpStderr forwards every byte of the child's stderr into the
// ring buffer. Returns when the pipe is closed (process exit).
func (c *Connection) pumpStderr(rc io.ReadCloser) {
	defer c.stderrWG.Done()
	buf := make([]byte, 4*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			_, _ = c.stderr.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// ConnectionFactory builds a stdio Connection bound to a recipe
// id. The factory carries the recipe's SpawnSpec so the per-recipe
// pool can spawn a fresh Connection on each restart cycle.
//
// WP02 leaves the factory single-recipe; WP07 plugs in a
// recipe-keyed factory for the Test-Connection one-shot path.
type ConnectionFactory struct {
	Spec   transport.SpawnSpec
	Logger ConnectionLogger
}

// NewConnection returns a fresh, unopened Connection bound to
// f.Spec. The id parameter is currently unused — the factory's
// SpawnSpec already names the recipe — but matches the
// transport.ConnectionFactory contract so future multi-recipe
// factories can dispatch by id.
func (f *ConnectionFactory) NewConnection(id string) (transport.Connection, error) {
	if len(f.Spec.Command) == 0 {
		return nil, errors.New("stdio: factory spec has empty command")
	}
	spec := f.Spec
	if spec.ID == "" {
		spec.ID = id
	}
	return NewConnection(spec, f.Logger), nil
}

// compile-time witness for the cross-transport factory contract.
var _ transport.ConnectionFactory = (*ConnectionFactory)(nil)
