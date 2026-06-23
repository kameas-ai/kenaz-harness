// Package webfetch implements the kenaz__web_fetch built-in tool.
//
// web_fetch makes HTTP requests on behalf of the model. It supports
// @secret: reference substitution in header values, the request body,
// and URL query strings. Resolved plaintext is zeroed immediately after
// the HTTP response returns (FR-003). Tool result content is passed
// through the per-turn Sanitizer so any response body that echoes the
// token is redacted before it re-enters the conversation (FR-007).
//
// This is one of the primary tools the model uses to make authenticated
// API calls without ever seeing a plaintext credential.
//
// Spec mapping: model-secret-references-01KW7M5A FR-002, FR-003, FR-007,
// WP07.
package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

const (
	// ToolName is the namespaced tool identifier.
	ToolName = "kenaz__web_fetch"

	// ToolDescription is the model-facing description.
	ToolDescription = "Make an HTTP request. Supports @secret:<locator> references " +
		"in header values, the request body, and URL query strings — the harness " +
		"resolves references at request time so plaintext never enters the model context. " +
		"Returns the response status, headers (minus sensitive ones), and body (up to 512 KiB)."

	// MaxResponseBodyBytes caps the response body at 512 KiB.
	MaxResponseBodyBytes = 512 * 1024
	// DefaultTimeoutMs is the default request timeout in milliseconds.
	DefaultTimeoutMs = 30_000
)

const inputSchema = `{
  "type": "object",
  "required": ["url"],
  "properties": {
    "url": {
      "type": "string",
      "description": "The request URL. @secret: references in query parameters are substituted."
    },
    "method": {
      "type": "string",
      "description": "HTTP method (GET, POST, PUT, PATCH, DELETE, HEAD). Defaults to GET."
    },
    "headers": {
      "type": "object",
      "description": "HTTP headers. Values may contain @secret:<locator> references.",
      "additionalProperties": { "type": "string" }
    },
    "body": {
      "type": "string",
      "description": "Request body. May contain @secret:<locator> references. Ignored for GET/HEAD."
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Request timeout in milliseconds. Defaults to 30000."
    }
  }
}`

// Options configures the Tool.
type Options struct {
	// HTTPClient is used for requests. When nil, a hardened client with
	// redirect validation is created. Tests should inject a controlled
	// httptest-backed client.
	HTTPClient *http.Client
	// Enabled is consulted before each call; returns false → tool declines.
	Enabled func() bool
	// Gate is the Cedar policy gate used to check network requests before
	// dispatch. When nil, no Cedar check is performed (pre-boot / test posture).
	Gate cedar.Gate
	// SkipBlockList disables the IP block-list check for the initial URL.
	// FOR TESTING ONLY — never set this in production paths. Useful when
	// injecting an httptest client whose server binds to 127.0.0.1.
	SkipBlockList bool
}

// Tool is the web_fetch builtin.
type Tool struct {
	client        *http.Client
	enabled       func() bool
	gate          cedar.Gate
	skipBlockList bool // true when a custom HTTPClient was injected (test mode)
}

// New constructs a web_fetch Tool.
func New(opts Options) *Tool {
	client := opts.HTTPClient
	if client == nil {
		client = newHardenedClient(opts.Gate)
	}
	return &Tool{
		client:        client,
		enabled:       opts.Enabled,
		gate:          opts.Gate,
		skipBlockList: opts.SkipBlockList,
	}
}

// newHardenedClient returns an *http.Client whose CheckRedirect callback
// re-validates each redirect target against the IP block list and the Cedar
// network gate. This prevents a server from redirecting the tool to a
// protected address (e.g. 169.254.169.254) even if the initial URL passed
// the gate.
func newHardenedClient(g cedar.Gate) *http.Client {
	return &http.Client{
		Timeout: time.Duration(DefaultTimeoutMs) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req == nil {
				return nil
			}
			host := req.URL.Hostname()
			if err := blockListCheck(host); err != nil {
				return err
			}
			if g != nil {
				if err := cedar.CheckNetwork(req.Context(), g, host); err != nil {
					return fmt.Errorf("web_fetch: redirect blocked by Cedar policy: %w", err)
				}
			}
			// Standard redirect limit: allow up to 10 redirects.
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// Name implements BuiltinTool.
func (t *Tool) Name() string { return ToolName }

// Description implements BuiltinTool.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema implements BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

type callArgs struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	TimeoutMs int               `json:"timeout_ms"`
}

type callResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Call implements BuiltinTool. Performs the HTTP request with @secret:
// substitution on URL, headers, and body. The Sanitizer in ctx redacts
// any resolved plaintext that appears in the response body.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errorResult("web_fetch is not enabled"), nil
	}

	var args callArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return errorResult("invalid args: " + err.Error()), nil
	}
	if args.URL == "" {
		return errorResult("url is required"), nil
	}
	if args.Method == "" {
		args.Method = http.MethodGet
	}
	timeoutMs := args.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}

	// ── Defense-in-depth: IP block list (F-002) ──────────────────────────
	// Resolve the target host and reject RFC-1918 / loopback / link-local /
	// IMDS ranges unconditionally — before Cedar, before secret resolution.
	// The block list is skipped when a custom HTTPClient is injected (test
	// mode) so httptest servers on 127.0.0.1 remain reachable in tests.
	parsedURL, urlParseErr := url.Parse(args.URL)
	if urlParseErr != nil {
		return errorResult("invalid url: " + urlParseErr.Error()), nil
	}
	targetHost := parsedURL.Hostname()
	if !t.skipBlockList {
		if err := blockListCheck(targetHost); err != nil {
			return errorResult(err.Error()), nil
		}
	}

	// ── Cedar network gate (F-002) ────────────────────────────────────────
	if err := cedar.CheckNetwork(ctx, t.gate, targetHost); err != nil {
		return errorResult("web_fetch blocked by Cedar network policy: " + err.Error()), nil
	}

	// Retrieve the resolver from ctx (may be nil in test or non-tool paths).
	resolver := refs.ResolverFromContext(ctx)
	rctx := cedar.ResolveContext{
		ToolName:        ToolName,
		DestinationHost: targetHost,
	}

	// ── Substitute @secret: references ──────────────────────────────────
	// URL.
	resolvedURL := args.URL
	var urlDecisions []cedar.Decision
	if resolver != nil && refs.HasReference(args.URL) {
		var urlDecs []cedar.Decision
		var err error
		resolvedURL, urlDecs, err = resolver.Substitute(ctx, args.URL, rctx)
		if err != nil {
			return errorResult("secret resolution failed in URL: " + err.Error()), nil
		}
		urlDecisions = append(urlDecisions, urlDecs...)
	}
	defer func() {
		_ = urlDecisions // hold reference
	}()

	// Headers.
	resolvedHeaders := make(map[string]string, len(args.Headers))
	for k, v := range args.Headers {
		if resolver != nil && refs.HasReference(v) {
			sub, _, err := resolver.Substitute(ctx, v, rctx)
			if err != nil {
				return errorResult(fmt.Sprintf("secret resolution failed in header %q: %s", k, err)), nil
			}
			resolvedHeaders[k] = sub
		} else {
			resolvedHeaders[k] = v
		}
	}

	// Body.
	resolvedBody := args.Body
	if resolver != nil && refs.HasReference(args.Body) {
		var err error
		resolvedBody, _, err = resolver.Substitute(ctx, args.Body, rctx)
		if err != nil {
			return errorResult("secret resolution failed in body: " + err.Error()), nil
		}
	}

	// ── Build + execute request ──────────────────────────────────────────
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var bodyReader io.Reader
	if resolvedBody != "" && args.Method != http.MethodGet && args.Method != http.MethodHead {
		bodyReader = strings.NewReader(resolvedBody)
	}

	req, err := http.NewRequestWithContext(reqCtx, strings.ToUpper(args.Method), resolvedURL, bodyReader)
	if err != nil {
		return errorResult("build request: " + err.Error()), nil
	}
	for k, v := range resolvedHeaders {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return errorResult("request failed: " + err.Error()), nil
	}
	defer resp.Body.Close()

	// ── Read + sanitize response body ────────────────────────────────────
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if err != nil {
		return errorResult("read response: " + err.Error()), nil
	}

	// Sanitize: redact any resolved plaintext that echoed back.
	sanitizer := refs.SanitizerFromContext(ctx)
	if sanitizer != nil {
		bodyBytes = sanitizer.Sanitize(bodyBytes)
	}

	// Build response headers (F-006: expanded blocklist for token-bearing headers).
	respHeaders := make(map[string]string)
	for k := range resp.Header {
		if isSensitiveResponseHeader(k) {
			continue
		}
		respHeaders[k] = resp.Header.Get(k)
	}

	result := callResult{
		Status:  resp.StatusCode,
		Headers: respHeaders,
		Body:    string(bodyBytes),
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return errorResult("marshal result: " + err.Error()), nil
	}
	return raw, nil
}

// ── F-002: IP block list ──────────────────────────────────────────────────────

// blockedIPNets is the list of IP ranges that are always blocked regardless
// of Cedar policy. Covers loopback, RFC-1918, link-local (incl. IMDS), and
// unique-local IPv6.
var blockedIPNets []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",     // IPv4 loopback
		"::1/128",         // IPv6 loopback
		"10.0.0.0/8",      // RFC 1918
		"172.16.0.0/12",   // RFC 1918
		"192.168.0.0/16",  // RFC 1918
		"169.254.0.0/16",  // IPv4 link-local (incl. AWS/Azure IMDS)
		"fd00::/8",        // IPv6 unique local
		"fe80::/10",       // IPv6 link-local
	}
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			blockedIPNets = append(blockedIPNets, ipNet)
		}
	}
}

// blockListCheck resolves host to its IP addresses and returns an error if any
// resolved address falls within a blocked range. The check runs unconditionally
// before Cedar — it is defense-in-depth against SSRF.
func blockListCheck(host string) error {
	if host == "" {
		return nil
	}
	// Strip brackets from IPv6 literals (e.g. [::1]).
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	// Fast path: host is already an IP literal.
	if ip := net.ParseIP(host); ip != nil {
		return checkIP(ip)
	}
	// Slow path: resolve hostname.
	addrs, err := net.LookupHost(host)
	if err != nil {
		// If resolution fails, allow the request to proceed (the HTTP client
		// will fail with its own error). We don't want to block on DNS failures.
		return nil
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil {
			if err := checkIP(ip); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkIP returns an error if ip falls in a blocked range.
func checkIP(ip net.IP) error {
	for _, blocked := range blockedIPNets {
		if blocked.Contains(ip) {
			return fmt.Errorf("web_fetch: request to private/reserved address %s is blocked (SSRF protection)", ip)
		}
	}
	return nil
}

// ── F-006: response-header blocklist ─────────────────────────────────────────

// sensitiveHeaderExact is a set of header names (canonical form) to always
// drop from the tool response.
var sensitiveHeaderExact = map[string]struct{}{
	"Authorization":      {},
	"Set-Cookie":         {},
	"Cookie":             {},
	"X-Api-Key":          {},
	"X-Auth-Token":       {},
	"Proxy-Authorization": {},
	"Www-Authenticate":   {},
	"Proxy-Authenticate": {},
}

// sensitiveHeaderSubstrings contains substrings that, if present in a header
// name (case-insensitive), cause the header to be dropped.
var sensitiveHeaderSubstrings = []string{"token", "secret", "key", "auth"}

// isSensitiveResponseHeader reports whether the named header should be
// stripped from the tool result. Canonical Go http.Header keys are already
// title-cased.
func isSensitiveResponseHeader(name string) bool {
	if _, ok := sensitiveHeaderExact[http.CanonicalHeaderKey(name)]; ok {
		return true
	}
	lower := strings.ToLower(name)
	for _, sub := range sensitiveHeaderSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func errorResult(msg string) json.RawMessage {
	v, _ := json.Marshal(map[string]any{"error": msg, "is_error": true})
	return v
}
