/**
 * toolExposure — layer edits and copy for the tool-exposure controls
 * (tool-context-budget-01TCBUD01 WP06): Capabilities' tier rows, the
 * composer Tools menu and the schedule form.
 *
 * A layer (user, project or session) is a sparse map: a server or tool
 * with no entry has no opinion and inherits the next layer down. Every
 * edit returns a new layer and drops entries that end up empty, so
 * clearing the last setting writes an empty layer (which clears it on
 * the Go side) rather than `{servers: {x: {}}}`.
 */
import type {
  ServerSchemaCost,
  ToolExposure,
  ToolExposureLevel,
  ToolExposureTier,
  ToolServerExposure,
} from './types';

/** The built-in server; its hot set stays full, so it is never unloaded. */
export const BUILTIN_SERVER = 'kenaz';

export const TIER_LABELS: Record<ToolExposureTier | 'mixed', string> = {
  full: 'Full',
  summary: 'Summary',
  off: 'Off',
  mixed: 'Mixed',
};

/** Why a tier is what it is, in the voice of the row it sits on. */
export function sourceLabel(source: ToolExposureLevel): string {
  switch (source) {
    case 'org_pin':
      return 'set by your organisation';
    case 'session':
      return 'set for this session';
    case 'project':
      return 'set for this project';
    case 'user':
      return 'your default';
    case 'default':
      return 'harness default';
    case 'invariant':
      return 'required while tools are in summary';
    default:
      return 'set per tool';
  }
}

/** "~1.2k", "~14k", "~640" — a token estimate, never exact. */
export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (n < 1_000) return `~${Math.round(n)}`;
  if (n < 10_000) return `~${(n / 1_000).toFixed(1).replace(/\.0$/, '')}k`;
  return `~${Math.round(n / 1_000)}k`;
}

function cloneServers(e: ToolExposure | null | undefined): Record<string, ToolServerExposure> {
  const out: Record<string, ToolServerExposure> = {};
  for (const [name, se] of Object.entries(e?.servers ?? {})) {
    out[name] = { ...se, tools: se.tools ? { ...se.tools } : undefined };
  }
  return out;
}

function prune(servers: Record<string, ToolServerExposure>): ToolExposure {
  const out: Record<string, ToolServerExposure> = {};
  for (const [name, se] of Object.entries(servers)) {
    const tools = se.tools && Object.keys(se.tools).length > 0 ? se.tools : undefined;
    if (!se.tier && !tools) continue;
    out[name] = { ...(se.tier ? { tier: se.tier } : {}), ...(tools ? { tools } : {}) };
  }
  return Object.keys(out).length > 0 ? { servers: out } : {};
}

/** The layer's own server tier ('' = no opinion). */
export function layerServerTier(e: ToolExposure | null | undefined, server: string): ToolExposureTier | '' {
  return e?.servers?.[server]?.tier ?? '';
}

/** The layer's own per-tool tier ('' = no opinion). */
export function layerToolTier(
  e: ToolExposure | null | undefined,
  server: string,
  tool: string,
): ToolExposureTier | '' {
  return e?.servers?.[server]?.tools?.[tool] ?? '';
}

/** Set (or, with '' / null, clear) a server's tier in a layer. */
export function withServerTier(
  e: ToolExposure | null | undefined,
  server: string,
  tier: ToolExposureTier | '' | null,
): ToolExposure {
  const servers = cloneServers(e);
  const se = servers[server] ?? {};
  servers[server] = { ...se, tier: tier || undefined };
  return prune(servers);
}

/** Set (or clear) one tool's tier in a layer. */
export function withToolTier(
  e: ToolExposure | null | undefined,
  server: string,
  tool: string,
  tier: ToolExposureTier | '' | null,
): ToolExposure {
  const servers = cloneServers(e);
  const se = servers[server] ?? {};
  const tools = { ...(se.tools ?? {}) };
  if (tier) tools[tool] = tier;
  else delete tools[tool];
  servers[server] = { ...se, tools };
  return prune(servers);
}

/**
 * Tokens of every tool the next request in this scope sends: full tools
 * of running servers plus, in a session, activated ones.
 */
export function sendableTokens(costs: readonly ServerSchemaCost[]): number {
  return costs.reduce((sum, c) => sum + (c.sendableTokenEst || 0), 0);
}
