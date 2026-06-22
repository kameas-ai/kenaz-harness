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
 *  Authorization: Bearer <token>  (via `?token=` query param, WS doesn't carry headers)
 *  client → { method, params }
 *  server → { event, data } | { event: "closed" } | { error }
 */

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
   */
  async call<T = unknown>(method: string, params?: unknown): Promise<T> {
    const body: ServedRPCRequest = { method, params: params ?? null };
    const res = await fetch(`${this.baseURL}/rpc`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...this.authHeaders(),
      },
      body: JSON.stringify(body),
    });

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
      throw new Error(`servedTransport: ${method}: ${json.error}`);
    }
    return json.result as T;
  }

  /**
   * openStream — open a WebSocket to /ws and return a cancellable handle.
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
    const ws = new WebSocket(wsURL);
    let closed = false;

    ws.addEventListener('open', () => {
      // Send the stream-subscribe request once connected.
      ws.send(
        JSON.stringify({ method: opts.method, params: opts.params ?? null }),
      );
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
      if (!closed) opts.onError?.('websocket error');
    });

    ws.addEventListener('close', () => {
      if (!closed) opts.onClose?.();
      closed = true;
    });

    return () => {
      closed = true;
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
        ws.close();
      }
    };
  }

  /**
   * buildWSURL — convert the base HTTP URL to a ws:// / wss:// URL.
   */
  private buildWSURL(): string {
    if (this.baseURL === '') {
      // Same origin: use window.location
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const path = '/ws';
      const tokenParam = this.token
        ? `?token=${encodeURIComponent(this.token)}`
        : '';
      return `${proto}//${window.location.host}${path}${tokenParam}`;
    }
    const url = new URL(this.baseURL);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.pathname = '/ws';
    if (this.token) url.searchParams.set('token', this.token);
    return url.toString();
  }
}
