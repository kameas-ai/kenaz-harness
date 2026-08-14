/**
 * workflowsClient — minimal direct-bridge accessor for the v0.3.0-beta
 * workflows surface (mission workflows-01KQ8TDG).
 *
 * NOT folded into the main HarnessClient yet because the full editor /
 * inputs / history / typed-error envelope arrives in WP07–WP10. Until
 * then the WorkflowsView talks to the bridge directly through this
 * tiny shim — `wails generate module` will collapse this into the
 * generated client when the rest of the surface lands.
 *
 * The shape mirrors `core/rpc/views/workflows.{Summary,Workflow,RunResult}`
 * exactly; see that file for the source of truth.
 */

export interface WorkflowsSummary {
  id: string;
  name: string;
  description?: string;
  version: number;
  stepCount: number;
  source: string;
}

export interface WorkflowsInput {
  name: string;
  kind: string;
  required?: boolean;
  default?: string;
  options?: string[];
}

/**
 * One step on the wire. Mirrors `workflowsview.Step` — a SUBSET of the
 * Go `workflows.Step`, which matters because the structured save path
 * reconstructs a workflow from exactly these fields.
 *
 * `inputsFrom` is the DAG: the names of the steps this one depends on.
 * It is what the shared canvas draws as edges
 * (visual-graph-authoring-01PMUX01 WP06).
 */
export interface WorkflowsStep {
  name: string;
  kind: string;
  /** Names of the steps that must run before this one. */
  inputsFrom?: string[];
  userPrompt?: string;
  cmd?: string;
  args?: string[];
  /** http_request */
  method?: string;
  /** http_request / web_fetch / web_scrape */
  url?: string;
  /** web_scrape: "" | css | llm */
  mode?: string;
}

export interface WorkflowsWorkflow {
  id: string;
  name: string;
  description?: string;
  version: number;
  inputs?: WorkflowsInput[];
  steps: WorkflowsStep[];
}

export interface WorkflowsStepRun {
  name: string;
  kind: string;
  status: string;
  output?: string;
  error?: string;
}

export interface WorkflowsRunResult {
  runId: string;
  workflowId: string;
  status: string;
  steps: WorkflowsStepRun[];
  error?: string;
}

/**
 * WorkflowsSaveInput — exactly one of `yaml` or `workflow` must be set.
 * `yaml` routes through ImportYAML on the backend (fresh id); `workflow`
 * is a structured update keyed by the supplied id.
 */
export interface WorkflowsSaveInput {
  yaml?: string;
  workflow?: WorkflowsWorkflow;
}

export interface WorkflowsSaveOutput {
  id: string;
  name: string;
  version: number;
  hash: string;
  yaml: string;
  createdAt: string;
  updatedAt: string;
}

// --- WP03 Catalog types ---

/** CatalogEntry is one card in the catalog grid. */
export interface WorkflowsCatalogEntry {
  id: string;
  name: string;
  description?: string;
  source: string;
  version: string;
  icon?: string;
  requiresCedarGrants?: string[];
  requiresCredentials?: string[];
  estimatedCostUSD: number;
  installStatus: string; // "not_installed" | "installed" | "installed_outdated"
}

/** CatalogPreview is returned by catalog.get() — full YAML + entry metadata. */
export interface WorkflowsCatalogPreview {
  entry: WorkflowsCatalogEntry;
  yamlSource: string;
}

/** CatalogInstallResult is returned by catalog.install(). */
export interface WorkflowsCatalogInstallResult {
  workflowId: string;
  scheduled: boolean;
  missingCredentials?: string[];
}

/** WorkflowsCatalogClient groups the WP03 catalog methods. */
export interface WorkflowsCatalogClient {
  list(): Promise<WorkflowsCatalogEntry[]>;
  get(id: string): Promise<WorkflowsCatalogPreview>;
  install(id: string): Promise<WorkflowsCatalogInstallResult>;
}

// --- WP02 schedule types (workflows-agentic-01KW2D3X) ---

export interface WorkflowsScheduleSetInput {
  workflowId: string;
  cron: string;
  timezone?: string;
}

export interface WorkflowsScheduleEntry {
  workflowId: string;
  cron: string;
  timezone?: string;
  enabled: boolean;
}

export interface WorkflowsRunSummary {
  runId: string;
  workflowId: string;
  status: string; // completed | failed | running
  startedAt: string; // ISO
  endedAt?: string; // ISO
  error?: string;
  scheduled: boolean;
}

export interface WorkflowsClient {
  list(): Promise<WorkflowsSummary[]>;
  get(id: string): Promise<WorkflowsWorkflow>;
  run(id: string, inputs: Record<string, string>): Promise<WorkflowsRunResult>;
  /** WP07: persist a user workflow (yaml import or structured update). */
  save(input: WorkflowsSaveInput): Promise<WorkflowsSaveOutput>;
  /** WP07: delete a stored workflow by id. */
  remove(id: string): Promise<void>;
  /** WP03: catalog sub-client. */
  catalog: WorkflowsCatalogClient;

  // ── Scheduler methods (workflows-agentic-01KW2D3X WP02) ─────────────
  scheduleSet(input: WorkflowsScheduleSetInput): Promise<void>;
  scheduleClear(workflowId: string): Promise<void>;
  scheduleList(): Promise<WorkflowsScheduleEntry[]>;
  runNow(workflowId: string): Promise<WorkflowsRunSummary>;

  // ── Scheduled-inbox methods (workflow-extensions-01KW2D3Y WP01) ─────
  scheduleRunHistory(workflowId: string, limit: number): Promise<WorkflowsRunSummary[]>;
  /** Returns ISO timestamp string or empty string if no schedule. */
  scheduleNextFire(workflowId: string): Promise<string>;
  cancelRun(runId: string): Promise<void>;
}

interface BridgeShape {
  Workflows_List: () => Promise<WorkflowsSummary[]>;
  Workflows_Get: (id: string) => Promise<WorkflowsWorkflow>;
  Workflows_Run: (
    id: string,
    inputs: Record<string, string>,
  ) => Promise<WorkflowsRunResult>;
  Workflows_Save: (input: WorkflowsSaveInput) => Promise<WorkflowsSaveOutput>;
  Workflows_Delete: (id: string) => Promise<void>;
  Workflows_CatalogList: () => Promise<WorkflowsCatalogEntry[]>;
  Workflows_CatalogGet: (id: string) => Promise<WorkflowsCatalogPreview>;
  Workflows_CatalogInstall: (id: string) => Promise<WorkflowsCatalogInstallResult>;
  // Scheduler (workflows-agentic-01KW2D3X WP02)
  Workflows_ScheduleSet: (input: WorkflowsScheduleSetInput) => Promise<void>;
  Workflows_ScheduleClear: (workflowId: string) => Promise<void>;
  Workflows_ScheduleList: () => Promise<WorkflowsScheduleEntry[]>;
  Workflows_RunNow: (workflowId: string) => Promise<WorkflowsRunSummary>;
  // Scheduled-inbox (workflow-extensions-01KW2D3Y WP01)
  Workflows_ScheduleRunHistory: (workflowId: string, limit: number) => Promise<WorkflowsRunSummary[]>;
  Workflows_ScheduleNextFire: (workflowId: string) => Promise<string>;
  Workflows_CancelRun: (runId: string) => Promise<void>;
}

function bridge(): BridgeShape {
  const b =
    typeof window !== 'undefined' && (window as unknown as { go?: { rpc?: { Bindings?: unknown } } }).go?.rpc?.Bindings
      ? ((window as unknown as { go: { rpc: { Bindings: BridgeShape } } }).go.rpc.Bindings)
      : undefined;
  if (!b) {
    throw new Error(
      'window.go.rpc.Bindings is not available. The harness frontend must run inside Wails.',
    );
  }
  return b;
}

export function createWorkflowsClient(): WorkflowsClient {
  return {
    list: () => bridge().Workflows_List(),
    get: (id) => bridge().Workflows_Get(id),
    run: (id, inputs) => bridge().Workflows_Run(id, inputs),
    save: (input) => bridge().Workflows_Save(input),
    remove: (id) => bridge().Workflows_Delete(id),
    catalog: {
      list: () => bridge().Workflows_CatalogList(),
      get: (id) => bridge().Workflows_CatalogGet(id),
      install: (id) => bridge().Workflows_CatalogInstall(id),
    },
    scheduleSet: (input) => bridge().Workflows_ScheduleSet(input),
    scheduleClear: (workflowId) => bridge().Workflows_ScheduleClear(workflowId),
    scheduleList: () => bridge().Workflows_ScheduleList(),
    runNow: (workflowId) => bridge().Workflows_RunNow(workflowId),
    scheduleRunHistory: (workflowId, limit) =>
      bridge().Workflows_ScheduleRunHistory(workflowId, limit),
    scheduleNextFire: (workflowId) => bridge().Workflows_ScheduleNextFire(workflowId),
    cancelRun: (runId) => bridge().Workflows_CancelRun(runId),
  };
}

/**
 * createFakeWorkflowsClient — a stub used by WorkflowsView tests that
 * cannot reach the live Wails bridge. seed lets a test override
 * individual methods; everything else returns an empty / no-op.
 */
export function createFakeWorkflowsClient(
  seed: Partial<WorkflowsClient> = {},
  catalogSeed: Partial<WorkflowsCatalogClient> = {},
): WorkflowsClient {
  return {
    list: seed.list ?? (() => Promise.resolve([])),
    get:
      seed.get ??
      ((id) =>
        Promise.resolve({
          id,
          name: '',
          version: 0,
          steps: [],
        })),
    run:
      seed.run ??
      ((id) =>
        Promise.resolve({
          runId: 'run-stub',
          workflowId: id,
          status: 'completed',
          steps: [],
        })),
    save:
      seed.save ??
      ((input) =>
        Promise.resolve({
          id: input.workflow?.id ?? 'wf-stub',
          name: input.workflow?.name ?? '',
          version: 1,
          hash: 'stub-hash',
          yaml: input.yaml ?? '',
          createdAt: '1970-01-01T00:00:00.000Z',
          updatedAt: '1970-01-01T00:00:00.000Z',
        })),
    remove: seed.remove ?? (() => Promise.resolve()),
    catalog: {
      list:
        catalogSeed.list ??
        (() => Promise.resolve([])),
      get:
        catalogSeed.get ??
        ((id) =>
          Promise.resolve({
            entry: {
              id,
              name: '',
              source: 'builtin',
              version: 'v1',
              estimatedCostUSD: 0,
              installStatus: 'not_installed',
            },
            yamlSource: '',
          })),
      install:
        catalogSeed.install ??
        ((id) =>
          Promise.resolve({
            workflowId: id,
            scheduled: false,
          })),
    },
    // Scheduler stubs
    scheduleSet: seed.scheduleSet ?? (() => Promise.resolve()),
    scheduleClear: seed.scheduleClear ?? (() => Promise.resolve()),
    scheduleList: seed.scheduleList ?? (() => Promise.resolve([])),
    runNow:
      seed.runNow ??
      ((workflowId) =>
        Promise.resolve({
          runId: 'run-now-stub',
          workflowId,
          status: 'completed',
          startedAt: new Date().toISOString(),
          scheduled: false,
        })),
    // Scheduled-inbox stubs
    scheduleRunHistory: seed.scheduleRunHistory ?? (() => Promise.resolve([])),
    scheduleNextFire: seed.scheduleNextFire ?? (() => Promise.resolve('')),
    cancelRun: seed.cancelRun ?? (() => Promise.resolve()),
  };
}
