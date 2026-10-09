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
  /**
   * One-shot schedules (model-scheduled-jobs-01PMSJ01 WP08, FR-006).
   * "cron" (the default, backward-compatible with every row created
   * before this field existed) or "once". Always populated on the real
   * wire response; optional here (rather than required) so existing
   * fixtures/tests written before this field existed do not all need
   * updating — treat a missing value as "cron".
   */
  triggerKind?: 'cron' | 'once';
  /** Fire time for a triggerKind "once" row (ISO 8601, UTC). Absent for "cron". */
  runAt?: string;
  /**
   * The newest persisted run outcome; absent when the schedule has never
   * run (dogfood 2026-10-08 round 2 — the row used to show no trace of a
   * run that only surfaced as a 10-second toast).
   */
  lastRun?: ScheduledChatRunSummary;
}

/** What "active default" resolves to for a schedule with no model override. */
export interface ScheduledChatDefaultModel {
  profileId: string;
  model: string;
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
  /** Model the run was dispatched with; absent when unknown. */
  model?: string;
  /** Run cost in USD; absent/0 when unknown or free. */
  costUsd?: number;
}

export interface ScheduledChatCreateInput {
  name: string;
  promptTemplate: string;
  /** Required when triggerKind is "cron" (the default); ignored for "once". */
  cron: string;
  timezone?: string;
  model?: string;
  outputSink?: string;
  enabled: boolean;
  /** "cron" (default) or "once" (model-scheduled-jobs-01PMSJ01 WP08). */
  triggerKind?: 'cron' | 'once';
  /** Required (ISO 8601) when triggerKind is "once"; ignored for "cron". */
  runAt?: string;
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
  triggerKind?: 'cron' | 'once';
  runAt?: string;
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
  defaultModel(): Promise<ScheduledChatDefaultModel>;
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
    defaultModel: () => bridge().ScheduledChat_DefaultModel(),
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
    triggerKind: 'cron',
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
    defaultModel:
      seed.defaultModel ?? (() => Promise.resolve({ profileId: '', model: '' })),
  };
}
