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
// GET  / (and all other paths) — serves the embedded dist-served bundle.
// GET / always injects the bearer token as a <meta name="harness-token"> tag
// in place of the <!--HARNESS_TOKEN_META--> placeholder in served.html.
//
// # Auth
//
// All endpoints except /healthz and static assets require:
//
//	Authorization: Bearer <token>
//
// where <token> matches HARNESS_SERVE_TOKEN.  Comparison is constant-time.
// A missing or wrong token yields 401.  When HARNESS_SERVE_TOKEN is empty the
// server still starts but every request is allowed through — local-dev only.
//
// # Embedding the bundle
//
// The caller (main.go) is responsible for embedding frontend/dist-served via
// //go:embed and passing the resulting fs.FS to New.  This keeps core/serve
// free of a direct dependency on the build artifact layout.
// When staticFS is nil the static-file handler returns 404 (test / minimal
// deployments).
package serve

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

const (
	// DefaultListenAddr is the default bind address for serve mode.
	DefaultListenAddr = "0.0.0.0:7880"

	// EnvListenAddr is the env var that overrides the default listen address.
	EnvListenAddr = "HARNESS_SERVE_LISTEN"

	// EnvToken is the env var holding the bearer token (constant-time compared).
	EnvToken = "HARNESS_SERVE_TOKEN"
)

// tokenPlaceholder is the HTML comment that served.html uses as a slot for
// the injected bearer-token meta tag.
const tokenPlaceholder = "<!--HARNESS_TOKEN_META-->"

// Server is the harness HTTP/WS server for served mode.  Construct with New
// and call Serve to block until the context is cancelled.
type Server struct {
	api         *rpc.API
	bus         *rpc.EventBus // in-process event bus for real-time WS push
	token       string
	staticFS    fs.FS              // embedded dist-served bundle; nil → 404 for static paths
	log         *slog.Logger
	srv         *http.Server
	authSession *authbroker.Session // nil when serve mode is not wired with auth (tests / anonymous)
}

// ServerOption is a functional option for [New].
type ServerOption func(*Server)

// WithAuthSession wires an [authbroker.Session] into the server.  When set,
// the Auth_State RPC method returns the current auth state.  When nil (default)
// Auth_State returns "anonymous".
func WithAuthSession(s *authbroker.Session) ServerOption {
	return func(srv *Server) { srv.authSession = s }
}

// New constructs a Server backed by the given *rpc.API.
//
// staticFS is the embedded frontend/dist-served bundle (pass the fs.FS
// sub-rooted at the dist-served directory).  When nil, GET / returns 404.
//
// If token is empty, auth is disabled (local dev).
//
// opts are optional [ServerOption] values, e.g. [WithAuthSession].
func New(api *rpc.API, addr, token string, staticFS fs.FS, log *slog.Logger, opts ...ServerOption) *Server {
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
		api:      api,
		bus:      api.EventBus(),
		token:    token,
		staticFS: staticFS,
		log:      log,
	}
	for _, o := range opts {
		o(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/ws", s.authMiddleware(websocket.Handler(s.handleWS)))
	mux.Handle("/rpc", s.authMiddleware(http.HandlerFunc(s.handleRPC)))
	// All other paths are served from the embedded static bundle.
	// Static assets are public (no auth) because the HTML page itself carries
	// the token via the injected meta tag — the JS reads it on boot.
	mux.HandleFunc("/", s.handleStatic)

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

// ─── static file serving ──────────────────────────────────────────────────

// handleStatic serves the embedded dist-served bundle.  GET / and GET
// /index.html both serve served.html with the token placeholder replaced.
// Asset requests (e.g. /assets/foo.js) are served verbatim.
//
// If staticFS is nil a 404 is returned immediately.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.staticFS == nil {
		http.Error(w, "static bundle not available", http.StatusNotFound)
		return
	}

	// Normalise the path: strip leading slash and collapse dots.
	p := path.Clean(r.URL.Path)
	p = strings.TrimPrefix(p, "/")

	// Index: serve served.html with token injection regardless of the exact
	// path requested (SPA hash-router handles client-side routing).
	isIndex := p == "" || p == "." || p == "index.html" || !strings.Contains(p, ".")
	if isIndex {
		s.serveIndex(w, r)
		return
	}

	f, err := s.staticFS.Open(p)
	if err != nil {
		// Fall back to index for unknown paths so SPA deep-links work.
		s.serveIndex(w, r)
		return
	}
	defer f.Close() //nolint:errcheck

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		s.serveIndex(w, r)
		return
	}

	// Serve the file with a detected content type.
	ext := path.Ext(p)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// serveIndex reads served.html from the embedded FS, injects the token meta
// tag in place of <!--HARNESS_TOKEN_META-->, and writes it to w.
func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	raw, err := fs.ReadFile(s.staticFS, "served.html")
	if err != nil {
		http.Error(w, "served.html not found in bundle", http.StatusInternalServerError)
		return
	}

	// Build the meta tag.  When no token is configured, inject an empty tag so
	// the JS still finds the element and treats auth as disabled.
	metaTag := fmt.Sprintf(`<meta name="harness-token" content=%q />`,
		s.token)
	injected := bytes.ReplaceAll(raw, []byte(tokenPlaceholder), []byte(metaTag))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Prevent caching of the index so browsers always get a fresh token.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(injected)
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

// AuthStateResult is the response shape for the Auth_State RPC method.
// It exposes the current in-VM auth state to the served frontend so the UI
// can show signed-in vs anonymous vs signed-out without token bytes.
//
// Privacy: no token bytes, claims, email, or PII are included here.
type AuthStateResult struct {
	// State is one of "anonymous", "signed_in", or "signed_out".
	State string `json:"state"`
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

	// Auth_State returns the current in-VM auth state for the served frontend.
	// Privacy: no token bytes are included in the response.
	case "Auth_State":
		state := authbroker.StateAnonymous
		if s.authSession != nil {
			state = s.authSession.State()
		}
		return AuthStateResult{State: state.String()}, nil

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

// streamSessions sends an initial sessions snapshot then pushes events in
// real time via the EventBus.  The bus receives every event the StreamBroker
// publishes (both desktop Wails and served-mode paths share the same broker
// with a MultiEmitter fan-out).
//
// The caller is the Sessions_Stream WS method handler and drains incoming
// frames concurrently so a client ping or disconnect is detected promptly.
func (s *Server) streamSessions(ctx context.Context, ws *websocket.Conn) {
	// writeFrame sends a frame and returns false when the connection is broken.
	writeFrame := func(event string, data any) bool {
		err := websocket.JSON.Send(ws, wsFrame{Event: event, Data: data})
		return err == nil
	}

	// Send an initial snapshot before subscribing to the bus so the client
	// always sees the current state even when no events arrive immediately.
	sessions, err := s.api.Sessions().List(ctx)
	if err != nil {
		_ = websocket.JSON.Send(ws, wsFrame{Error: "sessions list: " + err.Error()})
		return
	}
	if !writeFrame("sessions:snapshot", sessions) {
		return
	}

	// Subscribe to session-list change events on the in-process bus.
	// TopicSessionListChanged is the broker topic emitted by every backend
	// write that mutates the sessions list.
	busCh, busCancel := s.bus.Subscribe(64, rpc.TopicSessionListChanged)
	defer busCancel()

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
		case ev, ok := <-busCh:
			if !ok {
				// bus channel was closed (server shutting down)
				_ = websocket.JSON.Send(ws, wsFrame{Event: "closed"})
				return
			}
			// Re-fetch the full list so the client gets a consistent snapshot
			// after each change rather than a bare delta.  The payload
			// (SessionListChangedPayload) is forwarded as event metadata.
			updated, err := s.api.Sessions().List(ctx)
			if err != nil {
				if !writeFrame("error", map[string]string{"message": err.Error()}) {
					return
				}
				continue
			}
			if !writeFrame("sessions:update", map[string]any{
				"sessions": updated,
				"change":   ev.Payload,
			}) {
				return
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
