package fleet

// unsupported_endpoint.go — "this fleet server does not have that route".
//
// Verified against kenaz-fleet main (2026-10-05): fleet registers NO
// /api/v1/context/append, /api/v1/context/replay, /api/v1/handoff/* or
// /api/v1/audit/append route. They answer with the Go mux's plain-text
// "404 page not found" — not a JSON {code,message} envelope — and no
// provisioning, sign-in or retry changes that. The fleet owner's client
// guidance: treat a plain 404 on these as UNSUPPORTED, stop retrying, keep
// the events local.
//
// So a plain 404 latches the feature unsupported until the next fleet
// session reset (sign-in / sign-out — ResetUnsupportedEndpoints), logs once at INFO naming the endpoint,
// and every later call short-circuits with ErrEndpointUnsupported before any
// HTTP. A 404 that DOES carry a JSON envelope is a real application answer
// (e.g. a missing remote resource) and is left to the caller's existing
// handling, as are 5xx / timeouts.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ErrEndpointUnsupported is the sentinel every UnsupportedEndpointError
// matches via errors.Is. It is not a failure of the user's data or session:
// the connected fleet server simply does not implement the feature.
var ErrEndpointUnsupported = errors.New("fleet: endpoint not supported by this fleet server")

// The feature keys an unsupported latch is recorded under. One feature can
// span several routes (the event stream is append + replay; handoff is
// send + inbox + {id}) — a plain 404 on any of them latches the feature.
const (
	FeatureContextEventStream = "context_event_stream"
	FeatureTeamHandoff        = "team_handoff"
	FeatureAuditAppend        = "audit_append"
)

// UnsupportedEndpointError names the feature and the route that answered a
// plain 404. errors.Is(err, ErrEndpointUnsupported) is true.
type UnsupportedEndpointError struct {
	Feature  string
	Endpoint string
}

func (e *UnsupportedEndpointError) Error() string {
	return "fleet: " + e.Feature + " not supported by this fleet server (" + e.Endpoint + " → 404)"
}

// Is makes errors.Is(err, ErrEndpointUnsupported) match.
func (e *UnsupportedEndpointError) Is(target error) bool { return target == ErrEndpointUnsupported }

// endpointSupport is the per-Client unsupported-feature latch. Zero value is
// ready to use.
type endpointSupport struct {
	mu sync.Mutex
	// unsupported maps feature → the endpoint that first answered a plain 404.
	unsupported map[string]string
}

// check returns an *UnsupportedEndpointError when feature is latched.
func (s *endpointSupport) check(feature string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ep, ok := s.unsupported[feature]; ok {
		return &UnsupportedEndpointError{Feature: feature, Endpoint: ep}
	}
	return nil
}

// mark latches feature unsupported (logging once, at INFO) and returns the
// typed error.
func (s *endpointSupport) mark(feature, endpoint string) error {
	s.mu.Lock()
	if s.unsupported == nil {
		s.unsupported = map[string]string{}
	}
	_, already := s.unsupported[feature]
	if !already {
		s.unsupported[feature] = endpoint
	}
	s.mu.Unlock()
	if !already {
		logging.L().Info("fleet.endpoint.unsupported",
			"feature", feature,
			"endpoint", endpoint,
			"action", "stop_retrying_keep_local",
		)
	}
	return &UnsupportedEndpointError{Feature: feature, Endpoint: endpoint}
}

// reset clears every latched feature.
func (s *endpointSupport) reset() {
	s.mu.Lock()
	had := len(s.unsupported) > 0
	s.unsupported = nil
	s.mu.Unlock()
	if had {
		logging.L().Info("fleet.endpoint.unsupported_reset")
	}
}

// ResetUnsupportedEndpoints clears the unsupported-feature latch so the next
// call to each feature reaches the server again. Wired to the settings view's
// fleet session reset (sign-in / sign-out), so a fixed host or a fleet that
// has since shipped the endpoint recovers without an app restart.
//
// Deliberately NOT tied to SetAuthOKHook: that hook fires on every non-401
// response — including the plain 404 that sets the latch — so clearing there
// would un-latch immediately and turn the latch back into per-call retries.
func (c *Client) ResetUnsupportedEndpoints() {
	if c == nil {
		return
	}
	c.support.reset()
}

// endpointUnsupported returns the latched error for feature, or nil. Nil-safe.
func (c *Client) endpointUnsupported(feature string) error {
	if c == nil {
		return nil
	}
	return c.support.check(feature)
}

// markEndpointUnsupported latches feature on c. Nil-safe (returns the error
// without latching).
func (c *Client) markEndpointUnsupported(feature, endpoint string) error {
	if c == nil {
		return &UnsupportedEndpointError{Feature: feature, Endpoint: endpoint}
	}
	return c.support.mark(feature, endpoint)
}

// isPlainNotFound reports whether a response is the Go mux's route-level
// 404: status 404 AND either an empty body, or a text/plain body that is not
// a JSON {code} envelope (http.NotFound answers "404 page not found\n" with
// Content-Type text/plain). Anything else — a JSON envelope (an application
// answer), an HTML/XML error page from a load balancer, CDN or SPA fallback
// (a routing problem in front of fleet, not proof fleet lacks the route) —
// returns false and keeps the caller's ordinary error handling.
func isPlainNotFound(status int, contentType string, body []byte) bool {
	if status != http.StatusNotFound {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return true
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if !strings.HasPrefix(ct, "text/plain") {
		return false
	}
	if trimmed[0] == '<' {
		return false // markup mislabelled as text/plain
	}
	if trimmed[0] == '{' {
		var env struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(trimmed, &env) == nil && strings.TrimSpace(env.Code) != "" {
			return false
		}
	}
	return true
}

// isProxyNotFoundPage reports a 404 that came back as an HTML/XML page — a
// load balancer / CDN / SPA fallback in front of fleet. It is NOT latched
// unsupported; callers treat it as transient (the host may be fixed).
func isProxyNotFoundPage(status int, contentType string, body []byte) bool {
	if status != http.StatusNotFound {
		return false
	}
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "html") || strings.Contains(ct, "xml") {
		return true
	}
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("<"))
}

// endpointPath strips a query string so logs name the route, not the
// stream/session id in the query.
func endpointPath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}
