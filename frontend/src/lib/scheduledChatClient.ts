/**
 * scheduledChatClient — typed bridge to the ScheduledChat_* Wails bindings.
 *
 * Mission: scheduled-chat-runs-01KX5R8B (WP05).
 *
 * All types mirror core/rpc/views/scheduledchat/api.go exactly;
 * consumers do NOT import from Go directly (DIRECTIVE_001).
 */

// ── wire types ────────────────────────────────────────────────────────────

export interface ScheduledChatEntry {
  id: string;
  name: string;
  promptTemplate: string;
  cron: string;
  timezone?: string;
  model?: string;
  outputSink: string;
  enabled: boolean;
  createdAt: string; // ISO 8601
  updatedAt: string; // ISO 8601
}

export interface ScheduledChatRunSummary {
  id: string;
  chatRunId: string;
  sessionId?: string;
  status: 'completed' | 'failed' | 'running';
  startedAt: string; // ISO 8601
  endedAt?: string;  // ISO 8601
  outputSnippet?: string;
  error?: string;
}

export interface ScheduledChatCreateInput {
  name: string;
  promptTemplate: string;
  cron: string;
  timezone?: string;
  model?: string;
  outputSink?: string;
  enabled: boolean;
}

export interface ScheduledChatUpdateInput {
  id: string;
  name: string;
  promptTemplate: string;
  cron: string;
  timezone?: string;
  model?: string;
  outputSink?: string;
  enabled: boolean;
}

// ── client interface ──────────────────────────────────────────────────────

export interface ScheduledChatClient {
  create(input: ScheduledChatCreateInput): Promise<ScheduledChatEntry>;
  update(input: ScheduledChatUpdateInput): Promise<ScheduledChatEntry>;
  remove(id: string): Promise<void>;
  list(): Promise<ScheduledChatEntry[]>;
  get(id: string): Promise<ScheduledChatEntry>;
  runNow(id: string): Promise<ScheduledChatRunSummary>;
  history(id: string, limit: number): Promise<ScheduledChatRunSummary[]>;
  setEnabled(id: string, enabled: boolean): Promise<void>;
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

export function createScheduledChatClient(): ScheduledChatClient {
  return {
    create: (input) => bridge().ScheduledChat_Create(input),
    update: (input) => bridge().ScheduledChat_Update(input),
    remove: (id) => bridge().ScheduledChat_Delete(id),
    list: () => bridge().ScheduledChat_List(),
    get: (id) => bridge().ScheduledChat_Get(id),
    runNow: (id) => bridge().ScheduledChat_RunNow(id),
    history: (id, limit) => bridge().ScheduledChat_History(id, limit),
    setEnabled: (id, enabled) => bridge().ScheduledChat_SetEnabled(id, enabled),
  };
}

// ── test stub ─────────────────────────────────────────────────────────────

/**
 * createFakeScheduledChatClient — a stub for Vitest components tests that
 * cannot reach the live Wails bridge. seed overrides individual methods;
 * everything else returns safe empty/no-op responses.
 */
export function createFakeScheduledChatClient(
  seed: Partial<ScheduledChatClient> = {},
): ScheduledChatClient {
  const stub: ScheduledChatEntry = {
    id: 'stub-id',
    name: '',
    promptTemplate: '',
    cron: '0 9 * * *',
    outputSink: 'banner',
    enabled: true,
    createdAt: '1970-01-01T00:00:00Z',
    updatedAt: '1970-01-01T00:00:00Z',
  };
  return {
    create: seed.create ?? ((input) => Promise.resolve({ ...stub, ...input, id: 'new-id' })),
    update: seed.update ?? ((input) => Promise.resolve({ ...stub, ...input })),
    remove: seed.remove ?? (() => Promise.resolve()),
    list: seed.list ?? (() => Promise.resolve([])),
    get: seed.get ?? ((id) => Promise.resolve({ ...stub, id })),
    runNow:
      seed.runNow ??
      ((id) =>
        Promise.resolve({
          id: 'hist-stub',
          chatRunId: id,
          status: 'completed',
          startedAt: new Date().toISOString(),
        })),
    history: seed.history ?? (() => Promise.resolve([])),
    setEnabled: seed.setEnabled ?? (() => Promise.resolve()),
  };
}
