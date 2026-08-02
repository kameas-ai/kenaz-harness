/**
 * servedTransport — HTTP/WebSocket transport for served mode.
 *
 * Used by createServedHarnessClient() when the frontend is served over
 * HTTP from core/serve (i.e. running inside the VM browser, not inside Wails).
 *
 * # Token resolution (priority order)
 *  1. Explicit token passed to ServedTransport constructor.
 *  2. <meta name="harness-token" content="..."> in the document head.
 *  3. window.__HARNESS_TOKEN__ global (for build-time injection).
 *  4. Empty string — auth disabled (local dev, HARNESS_SERVE_TOKEN unset).
 *
 * # RPC call
 *  POST /rpc  { method, params }
 *  Authorization: Bearer <token>
 *  → 200 { result } | { error }
 *
 * # WS streaming
 *  GET /ws
 *  Sec-WebSocket-Protocol: harness-auth.<token>  (browser-compatible auth)
 *  client → { method, params }
 *  server → { event, data } | { event: "closed" } | { error }
 *
 * # Connection state
 *  The transport emits connection state changes to a module-level store
 *  (setConnectionState) so the Shell can show a ConnectionLostBanner and
 *  attempt reconnect with exponential backoff (FR-003).
 */

import { setConnectionState } from './useConnectionState';

export interface ServedRPCRequest {
  method: string;
  params?: unknown;
}

export interface ServedRPCResponse<T = unknown> {
  result?: T;
  error?: string;
}

export type WSFrameHandler = (event: string, data: unknown) => void;
export type WSErrorHandler = (err: string) => void;
export type WSCloseHandler = () => void;

declare global {
  interface Window {
    __HARNESS_TOKEN__?: string;
  }
}

/**
 * Resolve the bearer token from the available injection points.
 */
function resolveToken(): string {
  if (typeof window === 'undefined') return '';

  // 1. <meta name="harness-token" content="...">
  const meta = document.querySelector<HTMLMetaElement>(
    'meta[name="harness-token"]',
  );
  if (meta?.content) return meta.content;

  // 2. window.__HARNESS_TOKEN__ (build-time injection)
  if (window.__HARNESS_TOKEN__) return window.__HARNESS_TOKEN__;

  return '';
}

// ── Reconnect poller ─────────────────────────────────────────────────────

/**
 * BACKOFF_STEPS_MS — exponential backoff delays for reconnect attempts.
 * The sequence is: 1s, 2s, 4s, 8s, 16s, 30s (capped).
 */
const BACKOFF_STEPS_MS = [1_000, 2_000, 4_000, 8_000, 16_000, 30_000];

let _reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let _reconnectAttempt = 0;
let _healthcheckURL = '';

/**
 * startReconnectPoller — called when the transport detects connection loss.
 * Polls /healthz with exponential backoff; transitions state back to 'ready'
 * on success. The poller is idempotent — calling it again while a timer is
 * running is a no-op.
 */
function startReconnectPoller(baseURL: string): void {
  if (_reconnectTimer !== null) return; // already running
  _healthcheckURL = `${baseURL}/healthz`;
  scheduleNextAttempt();
}

function scheduleNextAttempt(): void {
  const delay = BACKOFF_STEPS_MS[Math.min(_reconnectAttempt, BACKOFF_STEPS_MS.length - 1)];
  _reconnectTimer = setTimeout(() => {
    _reconnectTimer = null;
    void attemptReconnect();
  }, delay);
}

async function attemptReconnect(): Promise<void> {
  try {
    const res = await fetch(_healthcheckURL, { method: 'GET', cache: 'no-store' });
    if (res.ok) {
      // Server is back — transition to ready.
      _reconnectAttempt = 0;
      setConnectionState('ready');
      return;
    }
  } catch {
    // Network error: server still down.
  }
  _reconnectAttempt++;
  setConnectionState('lost');
  scheduleNextAttempt();
}

/**
 * stopReconnectPoller — cancels any pending reconnect attempt.
 * Called when the transport is deliberately closed.
 */
export function stopReconnectPoller(): void {
  if (_reconnectTimer !== null) {
    clearTimeout(_reconnectTimer);
    _reconnectTimer = null;
  }
  _reconnectAttempt = 0;
}

/**
 * retryReconnectNow — fire an immediate reconnect probe, bypassing the backoff
 * timer. Wired to the ConnectionLostBanner "Retry" button so a user-initiated
 * retry doesn't have to wait for the next scheduled backoff step. Cancels any
 * pending scheduled attempt, resets the backoff, and probes /healthz right
 * away; on success attemptReconnect() flips the state back to 'ready', and on
 * failure it reschedules the poller as usual.
 *
 * No-op when no reconnect target has been recorded yet (i.e. the transport
 * never observed a connection loss). Safe to call in native mode — nothing
 * ever populates `_healthcheckURL` there, so it stays a no-op.
 */
export function retryReconnectNow(): void {
  if (_reconnectTimer !== null) {
    clearTimeout(_reconnectTimer);
    _reconnectTimer = null;
  }
  _reconnectAttempt = 0;
  if (_healthcheckURL === '') return; // no target recorded — nothing to probe
  void attemptReconnect();
}

/**
 * looksLikeRPCErrorEnvelope reports whether a server error string is a
 * JSON-encoded core/rpc.RPCError. Mirrors the shape check in
 * errors.ts parseRPCError() — kept local so servedTransport stays free of
 * a dependency on the error-parsing module.
 */
function looksLikeRPCErrorEnvelope(raw: string): boolean {
  if (!raw.startsWith('{')) return false;
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    return (
      typeof parsed['code'] === 'string' &&
      typeof parsed['message'] === 'string' &&
      typeof parsed['retryable'] === 'boolean'
    );
  } catch {
    return false;
  }
}

export class ServedTransport {
  private readonly baseURL: string;
  private readonly token: string;

  constructor(opts?: { baseURL?: string; token?: string }) {
    // Default to same origin; callers can override for cross-origin setups.
    this.baseURL = opts?.baseURL ?? '';
    this.token = opts?.token ?? resolveToken();
  }

  private authHeaders(): Record<string, string> {
    if (!this.token) return {};
    return { Authorization: `Bearer ${this.token}` };
  }

  /**
   * call — POST /rpc and return the typed result.
   * Throws on network error or when the server returns { error }.
   * On network error, transitions connection state to 'lost' and starts
   * the reconnect poller (FR-003).
   */
  async call<T = unknown>(method: string, params?: unknown): Promise<T> {
    const body: ServedRPCRequest = { method, params: params ?? null };
    let res: Response;
    try {
      res = await fetch(`${this.baseURL}/rpc`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...this.authHeaders(),
        },
        body: JSON.stringify(body),
      });
    } catch {
      // Network-level failure (server down / offline).
      setConnectionState('lost');
      startReconnectPoller(this.baseURL);
      throw new Error(`servedTransport: ${method}: network error`);
    }

    // Successful HTTP response — connection is up.
    if (_reconnectTimer !== null) {
      stopReconnectPoller();
      setConnectionState('ready');
    }

    if (!res.ok) {
      let msg = `HTTP ${res.status}`;
      try {
        const j = (await res.json()) as { error?: string };
        if (j.error) msg = j.error;
      } catch {
        // ignore parse failure
      }
      throw new Error(`servedTransport: ${method}: ${msg}`);
    }

    const json = (await res.json()) as ServedRPCResponse<T>;
    if (json.error) {
      // Preserve a structured backend error envelope VERBATIM.
      //
      // The Go side encodes typed failures (auth / transient /
      // context_overflow / …) as a JSON rpc.RPCError string, and the
      // frontend's parseRPCError() recovers the hint and retryability from
      // it. On the desktop transport Wails hands that string over
      // untouched. Prefixing it here — as every other served error is
      // prefixed — would make it unparseable, so served mode would show a
      // raw Go string where the desktop shows "Your API key was rejected;
      // rotate it in Kenaz". Same backend, same taxonomy, both transports.
      throw new Error(looksLikeRPCErrorEnvelope(json.error)
        ? json.error
        : `servedTransport: ${method}: ${json.error}`);
    }
    return json.result as T;
  }

  /**
   * openStream — open a WebSocket to /ws and return a cancellable handle.
   *
   * Auth: when a token is set, it is sent as a Sec-WebSocket-Protocol header
   * value using the "harness-auth.<token>" sub-protocol (browser-compatible;
   * see FR-004).  The server must accept this sub-protocol and extract the
   * token from it.  The token is NOT placed in the URL query string.
   *
   * The caller provides:
   *   - method  — e.g. "Sessions_Stream"
   *   - params  — optional params forwarded to the server
   *   - onFrame — called for each { event, data } frame from the server
   *   - onError — called when the server sends { error }
   *   - onClose — called when the WS closes (clean or error)
   *
   * Returns a close() function.  The caller MUST call it when done.
   */
  openStream(opts: {
    method: string;
    params?: unknown;
    onFrame: WSFrameHandler;
    onError?: WSErrorHandler;
    onClose?: WSCloseHandler;
  }): () => void {
    const wsURL = this.buildWSURL();
    const wsOpts: string[] = ['harness-v1'];
    if (this.token) {
      // Encode the token as a sub-protocol so browsers can send auth
      // without a custom header (which the WS API doesn't support).
      // The server must accept "harness-auth.<token>" and validate it.
      // Base64url-encode the token to ensure it's a valid protocol label
      // (no spaces, commas, or other WS-protocol-disallowed chars).
      wsOpts.push(`harness-auth.${btoa(this.token).replace(/=/g, '')}`);
    }
    const ws = new WebSocket(wsURL, wsOpts);
    let closed = false;

    ws.addEventListener('open', () => {
      // Send the stream-subscribe request once connected.
      ws.send(
        JSON.stringify({ method: opts.method, params: opts.params ?? null }),
      );
      // Clear any pending reconnect state — WS came up.
      if (_reconnectTimer !== null) {
        stopReconnectPoller();
        setConnectionState('ready');
      }
    });

    ws.addEventListener('message', (ev: MessageEvent<string>) => {
      let frame: { event?: string; data?: unknown; error?: string };
      try {
        frame = JSON.parse(ev.data) as typeof frame;
      } catch {
        opts.onError?.('bad frame: ' + ev.data);
        return;
      }
      if (frame.error) {
        opts.onError?.(frame.error);
        return;
      }
      if (frame.event === 'closed') {
        opts.onClose?.();
        return;
      }
      if (frame.event) {
        opts.onFrame(frame.event, frame.data ?? null);
      }
    });

    ws.addEventListener('error', () => {
      if (!closed) {
        opts.onError?.('websocket error');
        setConnectionState('lost');
        startReconnectPoller(this.baseURL);
      }
    });

    ws.addEventListener('close', () => {
      if (!closed) {
        opts.onClose?.();
        // Unexpected close: start the reconnect poller.
        setConnectionState('lost');
        startReconnectPoller(this.baseURL);
      }
      closed = true;
    });

    return () => {
      closed = true;
      stopReconnectPoller();
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
        ws.close();
      }
    };
  }

  /**
   * buildWSURL — convert the base HTTP URL to a ws:// / wss:// URL.
   * The token is NOT included in the URL (FR-004 privacy constraint).
   */
  private buildWSURL(): string {
    if (this.baseURL === '') {
      // Same origin: use window.location
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const path = '/ws';
      return `${proto}//${window.location.host}${path}`;
    }
    const url = new URL(this.baseURL);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.pathname = '/ws';
    // Do NOT add token as query param — use sub-protocol instead.
    return url.toString();
  }
}
