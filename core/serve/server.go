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
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
	permissionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/permissions"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
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
//
// script-src carries a CSP hash-source (not 'unsafe-inline') that allowlists
// exactly one inline script: the "read ?theme= before first paint" snippet
// in served.html (P0 theme fix). If that script's text ever changes, this
// hash AND THEME_SCRIPT_HASH in frontend/vite.config.ts must be
// recomputed together — see the comment there.
const servedCSP = "default-src 'none'; connect-src 'self'; " +
	"script-src 'self' 'sha256-S9VfhoaWcxszZps4jluBpniHVTyGsrOIZlLNj5x7ekE='; " +
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
	queueCap    int                  // per-WS-client frame queue depth; 0 → defaultStreamQueueCap

	// baseCtx is the server's lifetime context, captured in Serve. It is
	// the context handed to RPCs that spawn work OUTLIVING the HTTP
	// request that started them — see backgroundCtx.
	baseCtx context.Context
}

// backgroundCtx returns a context scoped to the SERVER's lifetime rather
// than to one HTTP request.
//
// This is not a nicety. LLM_StartStream returns a subscription id
// immediately and leaves the chat runner streaming in a goroutine; the
// tokens arrive later over the WebSocket. Handing it r.Context() means
// the turn is cancelled the instant the POST response is written — the
// runner dies with "context canceled" on its first history read and the
// browser sits watching a stream that will never produce a token.
//
// The desktop transport has always had this right by accident: its
// bindings pass the long-lived Wails app context. This is the served
// equivalent, and it still cancels on shutdown so in-flight turns are
// torn down when the harness stops.
func (s *Server) backgroundCtx() context.Context {
	if s.baseCtx != nil {
		return s.baseCtx
	}
	return context.Background()
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

// WithStreamQueueCap overrides the per-WebSocket-client frame queue depth
// (default [defaultStreamQueueCap]).
//
// The queue is what stands between a browser that stalls and the event bus
// that must not stall with it; see the backpressure policy in wsstream.go.
// Lower it to make the truncation path reachable deterministically in
// tests, or to cap per-client memory in a very constrained workbench.
// Values <= 0 keep the default.
func WithStreamQueueCap(n int) ServerOption {
	return func(srv *Server) { srv.queueCap = n }
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
	// Capture the server-lifetime context for RPCs whose work outlives the
	// request that started them (see backgroundCtx).
	s.baseCtx = ctx

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.log.Info("harness.serve.listening",
		"addr", s.srv.Addr,
		"auth", s.token != "",
	)

	// Shut down when the context is cancelled. Recover panics (FR-003).
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logging.L().Error("serve.server.shutdown_goroutine.panic",
					"panic", fmt.Sprintf("%v", r),
					"stack", string(stack),
				)
			}
		}()
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
//
// The wired surface is deliberately a subset of the desktop binding
// surface, chosen so that a browser inside a Kenaz workbench can do the
// thing the harness exists to do — hold a conversation — end to end:
// browse and create sessions, read and append messages, start and stop a
// streaming turn, and answer the permission prompts a turn raises.
//
// Everything outside that set still returns the explicit "not ported"
// error below rather than fake data. When adding to this switch, the bar
// is: a method belongs here only when the whole user-visible flow it
// participates in can actually complete inside a VM. Half a flow is worse
// than an honest refusal, because the user only finds out at the point
// where it breaks.
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

	// ── chat: session lifecycle ──────────────────────────────────────
	//
	// These mirror the desktop Sessions_* bindings one-for-one. Without
	// them a served harness could list conversations it could never start
	// or continue — the default app in every Kenaz workbench was unable to
	// hold a conversation at all.

	case "Sessions_Create":
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_Create: bad params: " + err.Error())
		}
		return s.api.Sessions().Create(ctx, p.Name)

	case "Sessions_ListMessages":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_ListMessages: bad params: " + err.Error())
		}
		return s.api.Sessions().ListMessages(ctx, p.ID)

	case "Sessions_ListMessagesActive":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_ListMessagesActive: bad params: " + err.Error())
		}
		return s.api.Sessions().ListMessagesActive(ctx, p.ID)

	case "Sessions_ListMessagesAll":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_ListMessagesAll: bad params: " + err.Error())
		}
		return s.api.Sessions().ListMessagesAll(ctx, p.ID)

	case "Sessions_AppendMessage":
		var p struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_AppendMessage: bad params: " + err.Error())
		}
		return s.api.Sessions().AppendMessage(ctx, p.ID, p.Role, p.Content)

	// Sessions_SendMessageWithBlocks goes through API.SendMessageBlocks so
	// the fleet emergency-lockdown gate is the SAME code the desktop
	// binding runs — a served client must not be able to dispatch a turn
	// the desktop app would refuse. See core/rpc/chat_gates.go.
	case "Sessions_SendMessageWithBlocks":
		var p struct {
			ID     string                      `json:"id"`
			Blocks []sessionsview.ContentBlock `json:"contentBlocks"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_SendMessageWithBlocks: bad params: " + err.Error())
		}
		return rpc.SendMessageBlocks(ctx, s.api, p.ID, p.Blocks)

	case "Sessions_SaveDraft":
		var p struct {
			ID    string `json:"id"`
			Draft string `json:"draft"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_SaveDraft: bad params: " + err.Error())
		}
		return nil, s.api.Sessions().SaveDraft(ctx, p.ID, p.Draft)

	case "Sessions_LoadDraft":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_LoadDraft: bad params: " + err.Error())
		}
		return s.api.Sessions().LoadDraft(ctx, p.ID)

	case "Sessions_GetUsage":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_GetUsage: bad params: " + err.Error())
		}
		return s.api.Sessions().GetUsage(ctx, p.ID)

	case "Sessions_Rename":
		var p struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_Rename: bad params: " + err.Error())
		}
		return nil, s.api.Sessions().Rename(ctx, p.ID, p.Name)

	case "Sessions_Delete":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_Delete: bad params: " + err.Error())
		}
		return nil, s.api.Sessions().Delete(ctx, p.ID)

	// SetSystemPrompt + MoveToProject + Projects_List exist here because the
	// new-session dialog needs all three: it optionally files the session
	// under a project and seeds it with a system prompt. Porting Create
	// alone would leave that dialog throwing halfway through.
	case "Sessions_SetSystemPrompt":
		var p struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Kind    string `json:"kind"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_SetSystemPrompt: bad params: " + err.Error())
		}
		return nil, s.api.Sessions().SetSystemPrompt(ctx, p.ID, p.Content, p.Kind)

	case "Sessions_MoveToProject":
		var p struct {
			ID        string `json:"id"`
			ProjectID string `json:"projectId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Sessions_MoveToProject: bad params: " + err.Error())
		}
		return nil, s.api.Sessions().MoveToProject(ctx, p.ID, p.ProjectID)

	case "Projects_List":
		return s.api.Projects().List(ctx)

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

	// LLM_ListProviders is READ-ONLY provider state. It exists so a served
	// harness inside a Kenaz workbench can tell its user the truth about
	// provider configuration instead of the old dead end ("Providers is not
	// available in served mode … run the harness as a desktop app" — advice
	// nobody inside a VM can act on).
	//
	// The only providers a served harness has are the ones the host control
	// plane delivered (Source "host", seeded from the EnvGrant environment
	// via rpc.WithHostProviders). The corresponding WRITE methods
	// (LLM_AddProvider / Update / Remove) are deliberately NOT ported: host
	// providers are immutable from inside the VM, so a write surface here
	// would be a lie with a spinner on it.
	//
	// Privacy: Provider carries only an INDIRECT credential reference — the
	// credential env var NAME (e.g. "ANTHROPIC_API_KEY"), never its bytes.
	case "LLM_ListProviders":
		return s.api.LLMConnector().ListProviders(ctx)

	// ── chat: the turn itself ────────────────────────────────────────
	//
	// Both go through API.StartLLMStream / API.StopLLMStream — the same
	// entry points the desktop Wails bindings call — so the lockdown gate,
	// the provider resolution and the error taxonomy cannot drift between
	// the two transports. The resulting llm:stream-chunk /
	// llm:stream-closed events reach this browser over the Sessions_Stream
	// WebSocket (see wsstream.go).

	case "LLM_StartStream":
		var p struct {
			ProfileID     string `json:"profileId"`
			SessionID     string `json:"sessionId"`
			ModelOverride string `json:"modelOverride"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("LLM_StartStream: bad params: " + err.Error())
		}
		// Server-lifetime context, NOT the request context: the turn keeps
		// streaming long after this POST has been answered.
		return rpc.StartLLMStream(s.backgroundCtx(), s.api, p.ProfileID, p.SessionID, p.ModelOverride)

	case "LLM_StopStream":
		var p struct {
			SubID string `json:"subId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("LLM_StopStream: bad params: " + err.Error())
		}
		return nil, rpc.StopLLMStream(ctx, s.api, p.SubID)

	// ── interactive gates ────────────────────────────────────────────
	//
	// The permission modal is not a nicety for an agentic harness: the
	// first tool call a turn makes raises a `<family>:permission-pending`
	// event and then BLOCKS on the user's answer. Forwarding the event
	// without porting Resolve would put a dialog on screen whose buttons
	// do nothing, so the pair ships together.

	case "Permissions_Resolve":
		var p struct {
			RequestID string `json:"requestId"`
			Decision  string `json:"decision"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, errors.New("Permissions_Resolve: bad params: " + err.Error())
		}
		return nil, s.api.Permissions().Resolve(ctx, p.RequestID, permissionsview.Decision(p.Decision))

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

// ─── helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
