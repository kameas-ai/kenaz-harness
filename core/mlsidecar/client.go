package mlsidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultBaseURL is the sidecar's loopback-only listen address (design
// §3.1: "kameas-ml sidecar ... FastAPI :7774"). Tests always point a
// Client at an httptest.Server URL instead — nothing in this package's
// test suite dials the real port.
const DefaultBaseURL = "http://127.0.0.1:7774"

// Client speaks the wire shapes design §3.3 specifies. It has no
// knowledge of whether the far end is the real kenaz-ml sidecar or the
// test stub (sidecar_stub_test.go) — that is the entire point of
// building WP12 "against a stub HTTP server ... regardless" (tasks.md).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a Client. A nil http.Client falls back to a
// short-timeout default — advice calls are budgeted in the hundreds of
// milliseconds (core/advice's 800ms hard timeout), and lifecycle calls
// (health, lease) should fail fast rather than hang the caller.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{BaseURL: baseURL, HTTP: httpClient}
}

// ErrKindNotServed is the typed refusal the engine returns from
// /v1/recommend/{kind} for a kind with no graduated model (design
// Amendment A3.2: "REFUSES a kind with no graduated model, typed 'kind
// not served'; falling back is the CLIENT's job"). A *StatusError whose
// Code is "kind_not_served" satisfies errors.Is(err, ErrKindNotServed).
var ErrKindNotServed = errors.New("mlsidecar: kind not served")

// KindNotServedCode is the wire error code carrying ErrKindNotServed.
const KindNotServedCode = "kind_not_served"

// ErrUnusableResponse marks a call where SOMETHING answered on the port
// but not with a usable payload: a non-2xx /health, or a body that does
// not decode into the wire type. It is deliberately distinct from a
// transport failure (connection refused / timeout): the port is occupied,
// so the Manager must never treat it as "nothing is running" and spawn a
// second engine onto a taken port (the failure mode a /health shape
// drift produced before the 2026-09-30 interop review).
var ErrUnusableResponse = errors.New("mlsidecar: sidecar answered with an unusable response")

// StatusError is a non-2xx sidecar response. Code is the engine's typed
// error code when the body was `{"error": "<code>"}`, else "".
type StatusError struct {
	Method string
	Path   string
	Status int
	Code   string
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("mlsidecar: %s %s: status %d (%s)", e.Method, e.Path, e.Status, e.Code)
	}
	return fmt.Sprintf("mlsidecar: %s %s: status %d", e.Method, e.Path, e.Status)
}

// Is makes errors.Is(err, ErrKindNotServed) work on a typed refusal.
func (e *StatusError) Is(target error) bool {
	return target == ErrKindNotServed && e.Code == KindNotServedCode
}

func newStatusError(method, path string, resp *http.Response) *StatusError {
	se := &StatusError{Method: method, Path: path, Status: resp.StatusCode}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return se
	}
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil {
		se.Code = env.Error
	}
	return se
}

func (c *Client) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return 0, fmt.Errorf("mlsidecar: build request %s: %w", path, err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("mlsidecar: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%w: GET %s: status %d", ErrUnusableResponse, path, resp.StatusCode)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("mlsidecar: read %s body: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%w: decode %s body: %v", ErrUnusableResponse, path, err)
	}
	return resp.StatusCode, nil
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any, headers map[string]string) (int, error) {
	var buf bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&buf).Encode(in); err != nil {
			return 0, fmt.Errorf("mlsidecar: encode %s body: %w", path, err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		return 0, fmt.Errorf("mlsidecar: build request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("mlsidecar: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, newStatusError(http.MethodPost, path, resp)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("mlsidecar: read %s body: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%w: decode %s body: %v", ErrUnusableResponse, path, err)
	}
	return resp.StatusCode, nil
}

// Health calls GET /health — the ONLY endpoint that carries the engine's
// identity (product, sidecar_version, exe_path, engine_sha256,
// lifecycle_protocol). Adoption (F2) and the skew-window check must read
// it from here; /status carries none of it (see types.go).
func (c *Client) Health(ctx context.Context) (HealthPayload, error) {
	var out HealthPayload
	if _, err := c.get(ctx, "/health", &out); err != nil {
		return HealthPayload{}, err
	}
	return out, nil
}

// Contracts calls GET /v1/contracts (design §3.3's per-kind ordered
// feature contract publication).
func (c *Client) Contracts(ctx context.Context) (ContractsPayload, error) {
	var out ContractsPayload
	if _, err := c.get(ctx, "/v1/contracts", &out); err != nil {
		return ContractsPayload{}, err
	}
	return out, nil
}

// Lease calls POST /v1/clients/lease. StatusCode is returned alongside
// the error so callers can distinguish "sidecar too old to know about
// leases" (404, design §3.7 R4's legacy-engine detection: "a harness
// that finds a legacy engine (no /v1/clients/lease → 404 ...)") from
// every other failure.
func (c *Client) Lease(ctx context.Context, req LeaseWireRequest) (LeaseWireResponse, int, error) {
	var out LeaseWireResponse
	status, err := c.postJSON(ctx, "/v1/clients/lease", req, &out, nil)
	return out, status, err
}

// Shutdown calls POST /v1/admin/shutdown with the local token as a
// Bearer credential (design §3.5: "graceful stop (POST
// /v1/admin/shutdown, authorized by the local token file in lease/)").
func (c *Client) Shutdown(ctx context.Context, token string) error {
	_, err := c.postJSON(ctx, "/v1/admin/shutdown", nil, nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	return err
}

// PushLabels calls POST /v1/labels/{kind} — the WP14 label ingest lane
// (design §5.2 + Amendment A3.3). Loopback only; see LabelPusher, the
// sole production caller.
func (c *Client) PushLabels(ctx context.Context, kind string, req LabelPushRequest) (LabelPushResponse, error) {
	var out LabelPushResponse
	if _, err := c.postJSON(ctx, "/v1/labels/"+kind, req, &out, nil); err != nil {
		return LabelPushResponse{}, err
	}
	return out, nil
}

// Recommend calls POST /v1/recommend/{kind}. Production caller: the
// AdviceEngine adapter (adviceengine.go) behind advice.SidecarAdvisor
// (WP15). An engine refusal of an unserved kind surfaces as a
// *StatusError satisfying errors.Is(err, ErrKindNotServed).
func (c *Client) Recommend(ctx context.Context, kind string, req RecommendRequest) (RecommendResponse, error) {
	var out RecommendResponse
	if _, err := c.postJSON(ctx, "/v1/recommend/"+kind, req, &out, nil); err != nil {
		return RecommendResponse{}, err
	}
	return out, nil
}

// SystemOne calls POST /v1/systemone — raw laya pass-through (design
// §3.2 owner ruling; not called by anything in WP12, present so the
// stub's coverage of the design's wire shapes is complete).
func (c *Client) SystemOne(ctx context.Context, req SystemOneRequest) (SystemOneResponse, error) {
	var out SystemOneResponse
	if _, err := c.postJSON(ctx, "/v1/systemone", req, &out, nil); err != nil {
		return SystemOneResponse{}, err
	}
	return out, nil
}
