/**
 * blockedRequestsClient — typed bridge to the BlockedRequests_* Wails
 * bindings.
 *
 * Mission: model-scheduled-jobs-01PMSJ01 (WP07, FR-004's second half —
 * owner decision 2: a denied permission request is surfaced next time
 * the user opens the app, with a Grant / Dismiss affordance and a
 * re-run path via the existing ScheduledChat_RunNow binding).
 *
 * All types mirror core/rpc/views/blockedrequests/api.go exactly;
 * consumers do NOT import from Go directly (DIRECTIVE_001). Follows the
 * same standalone-client pattern as scheduledChatClient.ts rather than
 * growing the monolithic harnessClient.ts.
 */

// ── wire types ────────────────────────────────────────────────────────────

export interface BlockedPermissionRequest {
  id: string;
  origin: 'scheduled_chat_run' | 'interactive';
  originId: string;
  sessionId: string;
  family: string; // "fs" — the only family with a Grant path today
  action: string; // e.g. "write_filesystem"
  resource: string;
  reason: string;
  status: 'pending' | 'granted' | 'dismissed';
  createdAt: string; // ISO 8601
  resolvedAt?: string; // ISO 8601
}

// ── client interface ──────────────────────────────────────────────────────

export interface BlockedRequestsClient {
  listPending(): Promise<BlockedPermissionRequest[]>;
  grant(id: string): Promise<void>;
  dismiss(id: string): Promise<void>;
}

// ── bridge helper ─────────────────────────────────────────────────────────

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function bridge(): any {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const b = (window as any)?.go?.rpc?.Bindings;
  if (!b) {
    throw new Error(
      'window.go.rpc.Bindings is not available. The harness frontend must run inside Wails.',
    );
  }
  return b;
}

// ── production client ─────────────────────────────────────────────────────

export function createBlockedRequestsClient(): BlockedRequestsClient {
  return {
    listPending: () => bridge().BlockedRequests_ListPending(),
    grant: (id) => bridge().BlockedRequests_Grant(id),
    dismiss: (id) => bridge().BlockedRequests_Dismiss(id),
  };
}

// ── test stub ─────────────────────────────────────────────────────────────

/**
 * createFakeBlockedRequestsClient — a stub for Vitest component tests
 * that cannot reach the live Wails bridge. seed overrides individual
 * methods; everything else returns safe empty/no-op responses.
 */
export function createFakeBlockedRequestsClient(
  seed: Partial<BlockedRequestsClient> = {},
): BlockedRequestsClient {
  return {
    listPending: seed.listPending ?? (() => Promise.resolve([])),
    grant: seed.grant ?? (() => Promise.resolve()),
    dismiss: seed.dismiss ?? (() => Promise.resolve()),
  };
}
