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

// TopicFleetSessionExpired is the Wails broker topic emitted when a token
// refresh fails (the user must re-authenticate). Declared here next to the
// payload type. The stream broker re-exports it via the event system.
const TopicFleetSessionExpired = "fleet:session:expired"

// SessionExpiredPayload is the JSON shape emitted on TopicFleetSessionExpired.
type SessionExpiredPayload struct {
	// Reason is a human-readable string describing why the session expired.
	Reason string `json:"reason"`
}

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

	// FR-006: proactively refresh when the token is known-expired (or within
	// the grace window) rather than only reacting to a server 401. This avoids
	// a failed first request for every poll cycle after token expiry.
	if !ts.ExpiresAt.IsZero() && time.Now().Add(tokenExpiryGrace).After(ts.ExpiresAt) {
		if ts.RefreshToken != "" {
			newTS, refreshErr := RefreshTokenSet(ctx, c.profile, ts.RefreshToken)
			if refreshErr != nil {
				return nil, c.refreshFailed("proactive refresh failed", refreshErr)
			}
			if saveErr := SaveTokens(newTS); saveErr != nil {
				return nil, saveErr
			}
			c.notifyAuthOK()
			ts = newTS
		} else {
			// Expired with no refresh token → session is dead.
			c.emitSessionExpired("access token expired and no refresh token available")
			return nil, ErrTokenExpired
		}
	}

	reqURL, urlErr := c.APIURL(ctx, path)
	if urlErr != nil {
		return nil, urlErr
	}

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
			if err != nil {
				cancel()
				lastErr = err
				continue // retry on transport error
			}

			if resp.StatusCode == http.StatusUnauthorized {
				// Drain and close body before refreshing.
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				cancel()
				lastResp = resp
				goto doRefresh
			}

			if resp.StatusCode >= 500 {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				cancel()
				lastResp = resp
				lastErr = fmt.Errorf("fleet: server error %d", resp.StatusCode)
				continue // backoff retry
			}

			// Success or a non-retryable error (4xx other than 401).
			//
			// The per-call context must outlive this function: Do returns
			// once the response HEADERS arrive, and the caller reads the body
			// afterwards. This used to call cancel() right after Do, so any
			// body still in flight was read under a cancelled context and
			// failed with "context canceled" — intermittently, depending on
			// whether the transport had already buffered it. That is the
			// fleet.unit.poll.pull_failed "context canceled" of dogfood
			// 2026-10-04 B3b (the poll succeeding on the next tick reset the
			// counter, which is why it read as noise). The context is now
			// released when the caller closes the body.
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			// The server accepted the token (anything but 401): the session
			// is alive, whatever an earlier refresh hiccup suggested.
			c.notifyAuthOK()
			return resp, nil
		}

		// All 3 attempts exhausted on 5xx.
		if lastErr != nil {
			return nil, lastErr
		}
		return lastResp, nil

	doRefresh:
		if refreshAttempts > 0 {
			// Already tried refresh once — session is dead.
			c.emitSessionExpired("re-authentication required after refresh failure")
			return nil, ErrTokenExpired
		}
		if _, ok := externalTokens(); ok {
			// Renewal is externally owned (host auth broker): there is no
			// refresh token on this side of the boundary. Re-read the source
			// once — the broker may have renewed since this request started —
			// and retry with the newer token; an unchanged token means the
			// server is rejecting a token the broker still considers live,
			// so the session is dead host-side.
			reloaded, reloadErr := LoadTokens()
			if reloadErr != nil || reloaded.AccessToken == ts.AccessToken {
				c.emitSessionExpired("brokered access token rejected by fleet")
				return nil, ErrTokenExpired
			}
			ts = reloaded
			continue
		}
		newTS, refreshErr := RefreshTokenSet(ctx, c.profile, ts.RefreshToken)
		if refreshErr != nil {
			return nil, c.refreshFailed("refresh token exchange failed", refreshErr)
		}
		if saveErr := SaveTokens(newTS); saveErr != nil {
			return nil, saveErr
		}
		c.notifyAuthOK()
		ts = newTS
		// Continue outer loop with new token.
	}

	return nil, errors.New("fleet: unexpected retry exhaustion")
}

// refreshFailed maps a RefreshTokenSet failure (fleet-session-truth-01DOGF0A
// review F2). Only a DEFINITE rejection — RefreshTokenSet wraps
// ErrTokenExpired around a non-200 from the token endpoint — means the
// session is dead and fires fleet:session:expired. A transport failure
// (laptop waking from sleep, VPN down, DNS) proves nothing about the
// session: it used to fire the same event, which the session snapshot then
// held as signed_out/session_expired while the tokens were still good.
// Transport failures now return a retryable ErrFleetUnreachable instead.
func (c *Client) refreshFailed(where string, err error) error {
	if errors.Is(err, ErrTokenExpired) {
		c.emitSessionExpired(where + ": " + err.Error())
		return ErrTokenExpired
	}
	return fmt.Errorf("%w (token refresh, %s): %v", ErrFleetUnreachable, where, err)
}

// SetAuthOKHook registers fn to run whenever fleet accepts this client's
// token (a non-401 response, or a refreshed token saved). The settings view
// uses it to clear a stale "session expired" (review F2 belt-and-braces).
// fn must be cheap; it runs on the request goroutine.
func (c *Client) SetAuthOKHook(fn func()) {
	if c == nil || c.isNop {
		return
	}
	c.authOK.Store(&fn)
}

func (c *Client) notifyAuthOK() {
	if c == nil {
		return
	}
	if p := c.authOK.Load(); p != nil && *p != nil {
		(*p)()
	}
}

// cancelOnClose releases a request's per-call context when the response
// body is closed (see the B3b note in do).
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// emitSessionExpired publishes a TopicFleetSessionExpired event to the broker,
// if one is wired. Safe to call with a nil broker.
func (c *Client) emitSessionExpired(reason string) {
	if c == nil || c.sessionBroker == nil {
		return
	}
	c.sessionBroker.Emit(TopicFleetSessionExpired, SessionExpiredPayload{Reason: reason})
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

// PatchJSON serialises v as JSON and PATCHes it. Caller closes the response body.
func (c *Client) PatchJSON(ctx context.Context, path string, v any) (*http.Response, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("fleet: marshal patch body: %w", err)
	}
	return c.do(ctx, http.MethodPatch, path, bytes.NewReader(data))
}

// Delete performs a DELETE.
func (c *Client) Delete(ctx context.Context, path string) (*http.Response, error) {
	return c.do(ctx, http.MethodDelete, path, nil)
}
