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

export interface WorkflowsStep {
  name: string;
  kind: string;
  userPrompt?: string;
  cmd?: string;
  args?: string[];
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
  Workflows_Catalog_List: () => Promise<WorkflowsCatalogEntry[]>;
  Workflows_Catalog_Get: (id: string) => Promise<WorkflowsCatalogPreview>;
  Workflows_Catalog_Install: (id: string) => Promise<WorkflowsCatalogInstallResult>;
}

declare global {
  interface Window {
    go?: { rpc?: { Bindings?: BridgeShape } };
  }
}

function bridge(): BridgeShape {
  const b =
    typeof window !== 'undefined' && window.go?.rpc?.Bindings
      ? (window.go.rpc.Bindings as BridgeShape)
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
      list: () => bridge().Workflows_Catalog_List(),
      get: (id) => bridge().Workflows_Catalog_Get(id),
      install: (id) => bridge().Workflows_Catalog_Install(id),
    },
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
  };
}
