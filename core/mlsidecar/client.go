package mlsidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultBaseURL is the sidecar's loopback-only listen address (design
// §3.1: "kameas-ml sidecar ... FastAPI :7774"). Tests always point a
// Client at an httptest.Server URL instead — nothing in this package's
// test suite dials the real port.
//
// This is the PROD engine's address; design Amendment A5(1) maps the port
// per env (prod 7774, dev 7775, test 7776). Production wiring dials
// DefaultEngineBaseURL(), which resolves this process's env.
const DefaultBaseURL = "http://127.0.0.1:7774"

// DefaultEngineBaseURL is this process's env's loopback engine URL
// (BaseURLForEnv(EngineEnv())) — what production wiring dials, so a dev
// build never talks to (or port-conflicts with) the prod engine.
func DefaultEngineBaseURL() string { return BaseURLForEnv(EngineEnv()) }

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
		return resp.StatusCode, fmt.Errorf("mlsidecar: GET %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("mlsidecar: read %s body: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("mlsidecar: decode %s body: %w", path, err)
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
		return resp.StatusCode, fmt.Errorf("mlsidecar: POST %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("mlsidecar: read %s body: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("mlsidecar: decode %s body: %w", path, err)
	}
	return resp.StatusCode, nil
}

// Health calls GET /health.
func (c *Client) Health(ctx context.Context) (HealthPayload, error) {
	var out HealthPayload
	if _, err := c.get(ctx, "/health", &out); err != nil {
		return HealthPayload{}, err
	}
	return out, nil
}

// Status calls GET /status.
func (c *Client) Status(ctx context.Context) (StatusPayload, error) {
	var out StatusPayload
	if _, err := c.get(ctx, "/status", &out); err != nil {
		return StatusPayload{}, err
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

// Recommend calls POST /v1/recommend/{kind}. Not used by any production
// call site in WP12 (design §9 Phase 0's gating note: no kind is
// sidecar-preferred yet) — included so the stub's shape is provable
// end-to-end and so WP04-06's later ladder work has a client ready.
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
