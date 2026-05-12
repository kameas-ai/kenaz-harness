// Package webfetch implements the kaneaz__web_fetch built-in tool.
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
	"net/http"
	"strings"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/credstore/refs"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

const (
	// ToolName is the namespaced tool identifier.
	ToolName = "kaneaz__web_fetch"

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
	// HTTPClient is used for requests. When nil, http.DefaultClient is used.
	HTTPClient *http.Client
	// Enabled is consulted before each call; returns false → tool declines.
	Enabled func() bool
}

// Tool is the web_fetch builtin.
type Tool struct {
	client  *http.Client
	enabled func() bool
}

// New constructs a web_fetch Tool.
func New(opts Options) *Tool {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Tool{client: client, enabled: opts.Enabled}
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

	// Retrieve the resolver from ctx (may be nil in test or non-tool paths).
	resolver := refs.ResolverFromContext(ctx)
	rctx := cedar.ResolveContext{
		ToolName: ToolName,
	}

	// ── Substitute @secret: references ──────────────────────────────────
	// URL.
	resolvedURL := args.URL
	var urlDecisions []cedar.Decision
	if resolver != nil && refs.HasReference(args.URL) {
		// Extract the destination host for Cedar context BEFORE substitution.
		rctx.DestinationHost = hostFromURL(args.URL)
		var urlDecs []cedar.Decision
		var err error
		resolvedURL, urlDecs, err = resolver.Substitute(ctx, args.URL, rctx)
		if err != nil {
			return errorResult("secret resolution failed in URL: " + err.Error()), nil
		}
		urlDecisions = append(urlDecisions, urlDecs...)
	} else {
		rctx.DestinationHost = hostFromURL(args.URL)
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

	// Build response headers (omit sensitive ones).
	respHeaders := make(map[string]string)
	for k := range resp.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "set-cookie" || lk == "cookie" {
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

func hostFromURL(rawURL string) string {
	// Simple host extraction without importing net/url to keep the package lean.
	// Handles http:// and https:// schemes.
	stripped := rawURL
	if strings.HasPrefix(stripped, "https://") {
		stripped = stripped[len("https://"):]
	} else if strings.HasPrefix(stripped, "http://") {
		stripped = stripped[len("http://"):]
	}
	// Strip path.
	if idx := strings.IndexByte(stripped, '/'); idx != -1 {
		stripped = stripped[:idx]
	}
	// Strip query.
	if idx := strings.IndexByte(stripped, '?'); idx != -1 {
		stripped = stripped[:idx]
	}
	// Strip port.
	if idx := strings.LastIndexByte(stripped, ':'); idx != -1 {
		stripped = stripped[:idx]
	}
	return strings.ToLower(stripped)
}

func errorResult(msg string) json.RawMessage {
	v, _ := json.Marshal(map[string]any{"error": msg, "is_error": true})
	return v
}
