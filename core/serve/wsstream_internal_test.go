package serve

// wsstream_internal_test.go tests the backpressure policy over an in-memory
// net.Pipe instead of a real socket.
//
// # Why this exists alongside the end-to-end test
//
// TestServedChat_SlowConsumerIsToldItWasTruncated (chat_rpc_test.go) drives
// the same policy through a real TCP loopback connection, which is worth
// having — it is the actual production path. But it can only establish its
// central precondition, "this client is too slow to receive everything", by
// out-publishing the kernel's socket buffers, whose size the test does not
// control and cannot observe. That made its setup an argument about kernel
// tuning rather than a property of the code, and the margin was measured at
// one frame on a dev Mac.
//
// net.Pipe removes the kernel from the picture entirely: it is completely
// synchronous and unbuffered, so the server's very first frame write blocks
// until the client reads it. A client that does not read is therefore a
// client that is provably stalled, on every platform, on the first frame,
// with no sizing argument at all.
//
// The burst below is then sized off the real capacity constants rather than a
// magic number, so it stays correct if those constants change.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
)

// pipeListener is a net.Listener that hands out one pre-made net.Pipe end.
// It lets net/http run a real WebSocket handshake over an in-memory,
// zero-buffer connection.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, io.EOF
	}
}
func (l *pipeListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}
func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// newPipedStream runs s.streamSessions over an in-memory pipe and returns the
// client end of the WebSocket, already past the initial sessions:snapshot.
// sessionID is the session this pipe connection subscribes to — every test
// publishing an event through it must stamp the matching SessionID on the
// payload, or WP01's frameFor session-scoping will (correctly) drop it.
func newPipedStream(t *testing.T, s *Server, sessionID string) *websocket.Conn {
	t.Helper()

	serverEnd, clientEnd := net.Pipe()
	lis := &pipeListener{conns: make(chan net.Conn, 1), done: make(chan struct{})}
	lis.conns <- serverEnd

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	httpSrv := &http.Server{
		Handler: websocket.Handler(func(ws *websocket.Conn) {
			s.streamSessions(ctx, ws, sessionID)
		}),
	}
	served := make(chan struct{})
	go func() { defer close(served); _ = httpSrv.Serve(lis) }()
	t.Cleanup(func() {
		_ = lis.Close()
		_ = httpSrv.Close()
		<-served
	})

	cfg, err := websocket.NewConfig("ws://pipe/ws", "http://localhost")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	ws, err := websocket.NewClient(cfg, clientEnd)
	if err != nil {
		t.Fatalf("ws handshake over pipe: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	// The snapshot is written only after SubscribeTracked returns, so reading
	// it is what makes the subscription live — see Server.streamSessions.
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var snapshot wsFrame
	if err := websocket.JSON.Receive(ws, &snapshot); err != nil {
		t.Fatalf("initial snapshot over pipe: %v", err)
	}
	if snapshot.Event != "sessions:snapshot" {
		t.Fatalf("expected sessions:snapshot first, got %q", snapshot.Event)
	}
	return ws
}

func newPipeServer(t *testing.T, queueCap int) (*rpc.API, *Server) {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := rpc.New(c)
	return api, New(api, "127.0.0.1:0", "tok", nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithStreamQueueCap(queueCap))
}

// A client that never reads must be told it lost data — established here
// without any dependence on socket buffer sizing.
//
// The determinism argument, in full:
//
//   - net.Pipe is unbuffered, so the writer blocks on its FIRST frame and
//     cannot advance until the client reads. The client does not read during
//     the burst, so the whole path downstream of the bus holds at most
//     queueCap frames in the queue plus one stuck in the writer.
//   - The bus subscription holds at most busBufferSize more.
//   - Publishing more than busBufferSize+queueCap+1 events therefore leaves
//     events with nowhere to go, and rpc.EventBus.Publish drops what does not
//     fit. Those drops are counted (queue-level in streamPump.dropped,
//     bus-level in Subscription.Dropped) and both feed totalDropped.
//
// So at least (burst - busBufferSize - queueCap - 1) events are provably
// dropped before the client reads its first byte, on any platform.
func TestStreamSessions_StalledPipeClientIsToldItWasTruncated(t *testing.T) {
	const queueCap = 2
	const sessionID = "sess-pipe"
	api, s := newPipeServer(t, queueCap)
	ws := newPipedStream(t, s, sessionID)

	burst := busBufferSize + queueCap + 1 + 64 // + margin, all in-memory
	for i := range burst {
		api.EventBus().Publish(topicLLMStreamChunk, llmview.StreamChunkPayload{
			SubID:     "sub-pipe",
			SessionID: sessionID,
			Chunk:     corellm.StreamEvent{Kind: corellm.StreamText, Text: "tok"},
		})
		_ = i
	}

	// Now read. The notice must appear, and it must appear BEFORE the client
	// has read the whole burst — a notice that only shows up after everything
	// else would not warn anyone in time.
	var notice StreamTruncatedPayload
	sawNotice := false
	framesRead := 0
	for framesRead < burst && !sawNotice {
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var f wsFrame
		if err := websocket.JSON.Receive(ws, &f); err != nil {
			t.Fatalf("draining pipe after %d frames (published %d): %v", framesRead, burst, err)
		}
		framesRead++
		if f.Event == TopicStreamTruncated {
			raw, mErr := json.Marshal(f.Data)
			if mErr != nil {
				t.Fatalf("re-marshal notice: %v", mErr)
			}
			if err := json.Unmarshal(raw, &notice); err != nil {
				t.Fatalf("unmarshal truncation notice %s: %v", raw, err)
			}
			sawNotice = true
		}
	}

	if !sawNotice {
		t.Fatalf("a client that never read was silently starved: no truncation notice in %d frames "+
			"(published %d, pipeline capacity %d)", framesRead, burst, busBufferSize+queueCap+1)
	}
	if notice.Dropped == 0 {
		t.Error("truncation notice reported zero dropped frames")
	}
	if notice.Reason == "" {
		t.Error("truncation notice carries no machine-readable reason")
	}
	// Must stay actionable from inside a workbench — "run the desktop app" is
	// not an option for someone whose harness IS the workbench.
	if notice.Message == "" {
		t.Error("truncation notice carries no human-readable message")
	}
}

// The mirror property: a client that keeps up is never told it was truncated.
// Over a pipe the reader and writer are lock-step, so this is exact rather
// than probabilistic — every published event is delivered, in order, with no
// notice at all.
func TestStreamSessions_LockstepPipeClientIsNeverTruncated(t *testing.T) {
	const sessionID = "sess-pipe"
	api, s := newPipeServer(t, defaultStreamQueueCap)
	ws := newPipedStream(t, s, sessionID)

	const want = 200
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range want {
			api.EventBus().Publish(topicLLMStreamChunk, llmview.StreamChunkPayload{
				SubID:     "sub-lockstep",
				SessionID: sessionID,
				Chunk:     corellm.StreamEvent{Kind: corellm.StreamText, Text: "tok"},
			})
		}
	}()

	for got := 0; got < want; {
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var f wsFrame
		if err := websocket.JSON.Receive(ws, &f); err != nil {
			t.Fatalf("reading lock-step stream after %d/%d: %v", got, want, err)
		}
		if f.Event == TopicStreamTruncated {
			t.Fatalf("a client reading every frame as fast as it arrived was told it "+
				"was truncated after %d/%d frames: %+v", got, want, f.Data)
		}
		if f.Event == topicLLMStreamChunk {
			got++
		}
	}
	<-done
}

// A pipe client that hangs up mid-stream must not wedge the server: the
// writer's deadline and the closed connection tear the handler down instead
// of pinning a goroutine and a queue forever.
func TestStreamSessions_PipeClientHangupTearsDownCleanly(t *testing.T) {
	const sessionID = "sess-pipe"
	api, s := newPipeServer(t, 2)
	ws := newPipedStream(t, s, sessionID)

	for range 64 {
		api.EventBus().Publish(topicLLMStreamChunk, llmview.StreamChunkPayload{
			SubID:     "sub-hangup",
			SessionID: sessionID,
			Chunk:     corellm.StreamEvent{Kind: corellm.StreamText, Text: "tok"},
		})
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reading a closed pipe must fail promptly rather than hang.
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var f wsFrame
	err := websocket.JSON.Receive(ws, &f)
	if err == nil {
		return // a frame raced the close out; still not a hang
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read after client close hung until the deadline: %v", err)
	}
}
