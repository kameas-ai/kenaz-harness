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
	"encoding/base64"
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
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
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

// cspPlaceholder is the sentinel that the Vite `inject-csp` plugin normally
// substitutes at build time (see frontend/vite.config.ts). The server
// replaces it defensively at serve time so a bundle that shipped the raw
// placeholder (e.g. built without the plugin) can never serve a literal
// "__CSP_PLACEHOLDER__" as its Content-Security-Policy.
const cspPlaceholder = "__CSP_PLACEHOLDER__"

// servedCSP is the Content-Security-Policy for served mode. It must be kept
// in sync with SERVED_CSP in frontend/vite.config.ts. Unlike the desktop
// production CSP (connect-src 'none'), served mode allows same-origin
// connect-src so the browser can reach /rpc and /ws on the harness server.
const servedCSP = "default-src 'none'; connect-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; base-uri 'none'; form-action 'none'; " +
	"frame-ancestors 'none'; object-src 'none'"

// Server is the harness HTTP/WS server for served mode.  Construct with New
// and call Serve to block until the context is cancelled.
type Server struct {
	api         *rpc.API
	bus         *rpc.EventBus // in-process event bus for real-time WS push
	token       string
	staticFS    fs.FS // embedded dist-served bundle; nil → 404 for static paths
	log         *slog.Logger
	srv         *http.Server
	authSession *authbroker.Session  // nil when serve mode is not wired with auth (tests / anonymous)
	elicit      elicitview.ElicitAPI // nil → falls back to api.Elicit(); injected for a stable pending surface
}

// ServerOption is a functional option for [New].
type ServerOption func(*Server)

// WithAuthSession wires an [authbroker.Session] into the server.  When set,
// the Auth_State RPC method returns the current auth state.  When nil (default)
// Auth_State returns "anonymous".
func WithAuthSession(s *authbroker.Session) ServerOption {
	return func(srv *Server) { srv.authSession = s }
}

// WithElicitAPI wires a stable [elicitview.ElicitAPI] into the server so the
// Elicit_ListPending RPC and the elicit:pending:snapshot WS frame observe a
// single, long-lived pending-ask registry.
//
// This matters because [rpc.API.Elicit] returns a fresh zero-config stub on
// every call when the API was constructed with a nil core (the test chassis
// path), which would make any registered pending ask invisible to a later
// snapshot read. Production wiring (rpc.New(core)) already returns a stable
// surface, so this option is primarily used by tests and by callers that want
// to guarantee the pending registry the served frontend sees is the same one
// the tool layer writes to.
func WithElicitAPI(e elicitview.ElicitAPI) ServerOption {
	return func(srv *Server) { srv.elicit = e }
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

	// wsServer wraps handleWS with a custom handshake that:
	//  1. Selects exactly one sub-protocol from the client's offered list
	//     (required by the WS spec and by golang.org/x/net/websocket when
	//     the client offers multiple protocols).
	//  2. Preserves the auth-agnostic origin check from the default handler.
	//  When the client sends "harness-v1, harness-auth.<token>", the server
	//  selects "harness-v1" as the agreed protocol and the auth token is
	//  extracted from the offered list in checkAuth() (before upgrade).
	wsServer := websocket.Server{
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			// Check origin (mirrors the default Handler behaviour).
			var err error
			cfg.Origin, err = websocket.Origin(cfg, req)
			if err != nil {
				return err
			}
			// Select exactly one protocol: prefer "harness-v1" if offered.
			// This resolves the AcceptHandshake "multiple protocols" error.
			for _, p := range cfg.Protocol {
				if p == "harness-v1" {
					cfg.Protocol = []string{"harness-v1"}
					return nil
				}
			}
			// No recognized control protocol — select nothing (pre-v2 compat).
			cfg.Protocol = nil
			return nil
		},
		Handler: websocket.Handler(s.handleWS),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/ws", s.authMiddleware(&wsServer))
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
//
// Two auth paths are accepted (FR-004):
//  1. HTTP Authorization header: "Bearer <token>" — used by non-browser
//     clients (curl, the Go test client, etc.).
//  2. WebSocket sub-protocol: "harness-auth.<base64url-token>" — used by
//     browsers, which cannot set the Authorization header during a WS
//     handshake.  The client sends Sec-WebSocket-Protocol: harness-v1,
//     harness-auth.<base64(token)> and the server extracts the token from
//     the matching sub-protocol label.
func (s *Server) checkAuth(r *http.Request) bool {
	if s.token == "" {
		return true // auth disabled — local dev
	}
	// Path 1: Authorization header.
	hdr := r.Header.Get("Authorization")
	const bearerPrefix = "Bearer "
	if strings.HasPrefix(hdr, bearerPrefix) {
		provided := hdr[len(bearerPrefix):]
		return subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
	}
	// Path 2: Sec-WebSocket-Protocol sub-protocol auth.
	// The browser sends: Sec-WebSocket-Protocol: harness-v1, harness-auth.<base64token>
	// We scan the comma-separated list for a label starting with "harness-auth.".
	const wsAuthPrefix = "harness-auth."
	for _, proto := range strings.Split(r.Header.Get("Sec-Websocket-Protocol"), ",") {
		proto = strings.TrimSpace(proto)
		if strings.HasPrefix(proto, wsAuthPrefix) {
			encoded := proto[len(wsAuthPrefix):]
			// Re-pad the base64 string (the client strips trailing = characters).
			switch len(encoded) % 4 {
			case 2:
				encoded += "=="
			case 3:
				encoded += "="
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return false
			}
			return subtle.ConstantTimeCompare(decoded, []byte(s.token)) == 1
		}
	}
	return false
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

	// Defensively substitute the CSP placeholder. The Vite `inject-csp` plugin
	// normally replaces it at build time; doing it here too means a bundle
	// built without that plugin can never serve a literal "__CSP_PLACEHOLDER__"
	// as its Content-Security-Policy (which would break the page). No-op when
	// the placeholder is already substituted.
	injected = bytes.ReplaceAll(injected, []byte(cspPlaceholder), []byte(servedCSP))

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

// elicitAPI returns the elicitation surface the served frontend should read.
// It prefers an explicitly-injected surface (WithElicitAPI) and otherwise
// falls back to api.Elicit(). The fallback is safe for production where
// rpc.New(core) returns a stable surface; injection is needed for the test
// chassis where rpc.New(nil).Elicit() hands back a fresh stub each call.
func (s *Server) elicitAPI() elicitview.ElicitAPI {
	if s.elicit != nil {
		return s.elicit
	}
	return s.api.Elicit()
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

	// Elicit_ListPending returns in-flight blocking elicitations (FR-007).
	// The served frontend calls this on WS reconnect to re-render any
	// dialog that was open before the connection was lost.
	case "Elicit_ListPending":
		return s.elicitAPI().ListPending(ctx)

	default:
		// Explicit "not ported" error so the served frontend can distinguish
		// "this method exists on desktop but is not yet wired in serve mode"
		// from a genuine typo.  FR-005: no fake success — the caller gets a
		// clear error, never silent fake data.
		return nil, fmt.Errorf("serve: %q is not ported to served mode; use the desktop app or file a ticket", method)
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
// In addition to session-list changes, this handler also bridges
// elicit:pending events (FR-007) so the served frontend receives new
// elicitation dialogs over the same WS connection without a separate stream.
// The frontend should call Elicit_ListPending via POST /rpc on reconnect to
// recover any asks that were in-flight before the connection was lost.
//
// The caller is the Sessions_Stream WS method handler and drains incoming
// frames concurrently so a client ping or disconnect is detected promptly.
func (s *Server) streamSessions(ctx context.Context, ws *websocket.Conn) {
	// writeFrame sends a frame and returns false when the connection is broken.
	writeFrame := func(event string, data any) bool {
		err := websocket.JSON.Send(ws, wsFrame{Event: event, Data: data})
		return err == nil
	}

	// Send an initial sessions snapshot before subscribing to the bus so the
	// client always sees the current state even when no events arrive immediately.
	sessions, err := s.api.Sessions().List(ctx)
	if err != nil {
		_ = websocket.JSON.Send(ws, wsFrame{Error: "sessions list: " + err.Error()})
		return
	}
	if !writeFrame("sessions:snapshot", sessions) {
		return
	}

	// Send any currently-pending elicitation asks as an initial snapshot so
	// the frontend can reconstruct dialog state on reconnect (FR-007).
	pending, err := s.elicitAPI().ListPending(ctx)
	if err == nil && len(pending) > 0 {
		if !writeFrame("elicit:pending:snapshot", pending) {
			return
		}
	}

	// Subscribe to session-list change events and elicit:pending events on
	// the in-process bus.
	busCh, busCancel := s.bus.Subscribe(64, rpc.TopicSessionListChanged, rpc.TopicElicitPending)
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
			switch ev.Topic {
			case rpc.TopicElicitPending:
				// Forward elicitation ask as-is so the served frontend can
				// open the ask dialog (FR-007).
				if !writeFrame("elicit:pending", ev.Payload) {
					return
				}
			default:
				// Session-list change: re-fetch the full list so the client
				// gets a consistent snapshot after each change rather than a
				// bare delta.  The payload (SessionListChangedPayload) is
				// forwarded as event metadata.
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
}

// ─── helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
