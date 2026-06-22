// Package serve implements the harness HTTP/WebSocket served mode.
//
// When the harness is launched with --serve (or HARNESS_SERVE_LISTEN is set),
// it skips the Wails desktop path and instead exposes a subset of the
// core/rpc API surface over HTTP so a browser in the same VM can reach it.
//
// # Protocol
//
// POST /rpc   — JSON-RPC-style body {method: string, params: any}
//
//	→ 200 {result: any} or {error: string}
//
// GET  /ws    — WebSocket; connect then send {method: string, params: any}
//
//	→ server streams {event: string, data: any} frames until close
//
// GET  /healthz — unauthenticated; returns 200 {"ok":true}
//
// # Auth
//
// All endpoints except /healthz require:
//
//	Authorization: Bearer <token>
//
// where <token> matches HARNESS_SERVE_TOKEN.  Comparison is constant-time.
// A missing or wrong token yields 401.  When HARNESS_SERVE_TOKEN is empty the
// server still starts but every request is allowed through — local-dev only.
package serve

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
)

const (
	// DefaultListenAddr is the default bind address for serve mode.
	DefaultListenAddr = "0.0.0.0:7880"

	// EnvListenAddr is the env var that overrides the default listen address.
	EnvListenAddr = "HARNESS_SERVE_LISTEN"

	// EnvToken is the env var holding the bearer token (constant-time compared).
	EnvToken = "HARNESS_SERVE_TOKEN"
)

// Server is the harness HTTP/WS server for served mode.  Construct with New
// and call Serve to block until the context is cancelled.
type Server struct {
	api    *rpc.API
	token  string
	log    *slog.Logger
	srv    *http.Server
}

// New constructs a Server backed by the given *rpc.API.
// If token is empty, auth is disabled (local dev).
func New(api *rpc.API, addr, token string, log *slog.Logger) *Server {
	if addr == "" {
		addr = DefaultListenAddr
		if v := os.Getenv(EnvListenAddr); v != "" {
			addr = v
		}
	}
	if token == "" {
		token = os.Getenv(EnvToken)
	}
	if log == nil {
		log = slog.Default()
	}

	s := &Server{
		api:   api,
		token: token,
		log:   log,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/ws", s.authMiddleware(websocket.Handler(s.handleWS)))
	mux.Handle("/rpc", s.authMiddleware(http.HandlerFunc(s.handleRPC)))

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Serve starts listening and blocks until ctx is cancelled or the listener
// encounters a fatal error.  It always returns a non-nil error; a context
// cancellation surfaces as context.Canceled.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.log.Info("harness.serve.listening",
		"addr", s.srv.Addr,
		"auth", s.token != "",
	)

	// Shut down when the context is cancelled.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
	}()

	err = s.srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return context.Canceled
	}
	return err
}

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.srv.Addr }

// ─── auth middleware ───────────────────────────────────────────────────────

// authMiddleware wraps h with bearer-token validation.  When no token is
// configured it is a pass-through (local dev).
func (s *Server) authMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAuth(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// checkAuth returns true when auth is satisfied.  Constant-time token compare.
func (s *Server) checkAuth(r *http.Request) bool {
	if s.token == "" {
		return true // auth disabled — local dev
	}
	hdr := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(hdr, prefix) {
		return false
	}
	provided := hdr[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

// ─── /healthz ─────────────────────────────────────────────────────────────

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}

// ─── /rpc ─────────────────────────────────────────────────────────────────

// RPCRequest is the JSON body for POST /rpc.
type RPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// RPCResponse is the JSON body returned by POST /rpc.
type RPCResponse struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req RPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, RPCResponse{Error: "bad request: " + err.Error()})
		return
	}

	result, err := s.dispatch(r.Context(), req.Method, req.Params)
	if err != nil {
		writeJSON(w, http.StatusOK, RPCResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, RPCResponse{Result: result})
}

// dispatch routes a method name to the appropriate rpc.API call.
// Only a representative slice is wired here; the full port is deferred.
func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "AppInfo":
		return s.api.AppInfo(ctx)

	case "ShellStatus":
		return s.api.ShellStatus(ctx)

	case "Sessions_List":
		return s.api.Sessions().List(ctx)

	case "Sessions_Get":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_Get: bad params: " + err.Error())
		}
		return s.api.Sessions().Get(ctx, p.ID)

	default:
		return nil, errors.New("method not found: " + method)
	}
}

// ─── /ws ──────────────────────────────────────────────────────────────────

// wsFrame is the shape of messages sent over the WebSocket.
type wsFrame struct {
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// wsRequest is the shape of messages received over the WebSocket.
type wsRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// handleWS handles a WebSocket connection for streaming subscriptions.
// Protocol:
//
//	client → {"method": "Sessions_Stream", "params": {}}
//	server → {"event": "sessions:snapshot", "data": [...]}  (initial)
//	server → {"event": "sessions:update", "data": {...}}    (on change)
//	server → {"event": "closed"}                            (on close)
func (s *Server) handleWS(ws *websocket.Conn) {
	defer ws.Close() //nolint:errcheck

	// Read first message to understand what stream the client wants.
	var req wsRequest
	if err := websocket.JSON.Receive(ws, &req); err != nil {
		_ = websocket.JSON.Send(ws, wsFrame{Error: "bad request: " + err.Error()})
		return
	}

	ctx, cancel := context.WithCancel(ws.Request().Context())
	defer cancel()

	switch req.Method {
	case "Sessions_Stream":
		s.streamSessions(ctx, ws)
	default:
		_ = websocket.JSON.Send(ws, wsFrame{Error: "unknown stream method: " + req.Method})
	}
}

// streamSessions sends an initial sessions snapshot then polls every 2 s
// until the connection closes.  Real-time push is deferred; this proves
// the WS pattern end-to-end with a simple poll-based approach.
func (s *Server) streamSessions(ctx context.Context, ws *websocket.Conn) {
	// writeFrame sends a frame and returns false when the connection is broken.
	writeFrame := func(event string, data any) bool {
		err := websocket.JSON.Send(ws, wsFrame{Event: event, Data: data})
		return err == nil
	}

	// Send an initial snapshot.
	sessions, err := s.api.Sessions().List(ctx)
	if err != nil {
		_ = websocket.JSON.Send(ws, wsFrame{Error: "sessions list: " + err.Error()})
		return
	}
	if !writeFrame("sessions:snapshot", sessions) {
		return
	}

	// Poll-based streaming: send updates every 2 s.  A deduplicated push-
	// based approach is deferred to a later WP once the broker is wired for
	// non-Wails emission.
	var mu sync.Mutex
	prevJSON, _ := json.Marshal(sessions)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Drain incoming messages in a separate goroutine so a client ping or
	// a disconnect signal reaches us promptly.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			var ignored json.RawMessage
			if err := websocket.JSON.Receive(ws, &ignored); err != nil {
				return // connection closed
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = websocket.JSON.Send(ws, wsFrame{Event: "closed"})
			return
		case <-readDone:
			return // client closed the connection
		case <-ticker.C:
			updated, err := s.api.Sessions().List(ctx)
			if err != nil {
				if !writeFrame("error", map[string]string{"message": err.Error()}) {
					return
				}
				continue
			}
			newJSON, _ := json.Marshal(updated)
			mu.Lock()
			changed := string(newJSON) != string(prevJSON)
			if changed {
				prevJSON = newJSON
			}
			mu.Unlock()
			if changed {
				if !writeFrame("sessions:update", updated) {
					return
				}
			}
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
