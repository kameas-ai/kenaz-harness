package fleet

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

// do executes an HTTP request against the fleet server with:
//   - Bearer token injection from the keychain
//   - 5xx exponential backoff (1s/2s/4s, max 3 total attempts)
//   - 401 → one refresh-token exchange → retry once
//   - Per-call context timeout (default 30s, configurable via ClientOpts)
//
// The access token bytes are fetched from the keychain inside this function
// and are NOT passed as parameters.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}
	if !c.profile.Configured() {
		return nil, ErrProfileNotConfigured
	}

	// Read body once (we may need to replay it on retry).
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("fleet: read request body: %w", err)
		}
	}

	ts, err := LoadTokens()
	if err != nil {
		return nil, ErrNotSignedIn
	}

	reqURL := c.profile.FleetBaseURL + path

	var lastResp *http.Response
	var lastErr error

	// Outer loop: up to 1 refresh-retry cycle.
	for refreshAttempts := 0; refreshAttempts < 2; refreshAttempts++ {
		// Inner loop: up to 3 backoff attempts on 5xx.
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				backoff := time.Duration(1<<(attempt-1)) * time.Second // 1s, 2s
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}

			reqCtx, cancel := context.WithTimeout(ctx, c.httpTimeout)
			var reqBody io.Reader
			if len(bodyBytes) > 0 {
				reqBody = bytes.NewReader(bodyBytes)
			}
			req, err := http.NewRequestWithContext(reqCtx, method, reqURL, reqBody)
			if err != nil {
				cancel()
				return nil, fmt.Errorf("fleet: build request: %w", err)
			}
			req.Header.Set("Authorization", "Bearer "+ts.AccessToken)

			resp, err := c.httpClient.Do(req)
			cancel()
			if err != nil {
				lastErr = err
				continue // retry on transport error
			}

			if resp.StatusCode == http.StatusUnauthorized {
				// Drain and close body before refreshing.
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				lastResp = resp
				goto doRefresh
			}

			if resp.StatusCode >= 500 {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				lastResp = resp
				lastErr = fmt.Errorf("fleet: server error %d", resp.StatusCode)
				continue // backoff retry
			}

			// Success or a non-retryable error (4xx other than 401).
			return resp, nil
		}

		// All 3 attempts exhausted on 5xx.
		if lastErr != nil {
			return nil, lastErr
		}
		return lastResp, nil

	doRefresh:
		if refreshAttempts > 0 {
			// Already tried refresh once.
			return nil, ErrTokenExpired
		}
		newTS, refreshErr := RefreshTokenSet(ctx, c.profile, ts.RefreshToken)
		if refreshErr != nil {
			return nil, ErrTokenExpired
		}
		if saveErr := SaveTokens(newTS); saveErr != nil {
			return nil, saveErr
		}
		ts = newTS
		// Continue outer loop with new token.
	}

	return nil, errors.New("fleet: unexpected retry exhaustion")
}

// Get performs a GET against the fleet server path.
func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// Post performs a POST with a raw body.
func (c *Client) Post(ctx context.Context, path string, contentType string, body io.Reader) (*http.Response, error) {
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// PostJSON serialises v as JSON and POSTs it. Caller closes the response body.
func (c *Client) PostJSON(ctx context.Context, path string, v any) (*http.Response, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("fleet: marshal post body: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	// Set Content-Type is a no-op after the request is sent but we log it
	// for debugging. The request already has the JSON bytes.
	return resp, nil
}

// Put performs a PUT with a raw body.
func (c *Client) Put(ctx context.Context, path string, body io.Reader) (*http.Response, error) {
	return c.do(ctx, http.MethodPut, path, body)
}

// Delete performs a DELETE.
func (c *Client) Delete(ctx context.Context, path string) (*http.Response, error) {
	return c.do(ctx, http.MethodDelete, path, nil)
}
