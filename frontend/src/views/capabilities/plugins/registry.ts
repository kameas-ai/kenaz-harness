/**
 * Per-kind plugins of the "Add capability" surface
 * (install-framework-01DOGF0B, FR-4: "per-kind flows are preserved as
 * detail-pane plugins"). The surface itself is kind-agnostic: it lists
 * Capability_List rows, installs zero-input items through
 * Capability_Install, and hands everything kind-specific — key prompts,
 * OAuth, directory picks, previews, removal confirmations — to the plugin
 * registered here for the row's kind.
 */
import type { Component } from 'vue';
import type { CapabilityItem, CapabilityKind, CapabilitySource } from '@/lib/types';
import McpRecipeDetail from './McpRecipeDetail.vue';

export interface KindPlugin {
  kind: CapabilityKind;
  /** Kind filter chip label. */
  label: string;
  /** Singular noun for row tags. */
  noun: string;
  /** Detail-pane component; receives `item` and emits `changed`. May
   *  expose `beginInstall()` — the surface calls it when a row's Install
   *  needs the per-kind flow (unsatisfied requirements). */
  detail: Component;
  /** True when the detail pane owns removal (its own confirmation and
   *  per-kind binding). Otherwise the surface offers a generic Remove
   *  through Capability_Uninstall. */
  ownsRemove: boolean;
}

export const KIND_PLUGINS: readonly KindPlugin[] = [
  {
    kind: 'mcp_recipe',
    label: 'MCP servers',
    noun: 'MCP server',
    detail: McpRecipeDetail,
    ownsRemove: true,
  },
];

export function pluginFor(kind: CapabilityKind): KindPlugin | undefined {
  return KIND_PLUGINS.find((p) => p.kind === kind);
}

/** "Add your own" entry points (FR-4: paste-config and custom-recipe
 *  authoring stay as entry points in the same surface). */
export interface EntryPoint {
  id: 'mcp-paste' | 'mcp-custom';
  kind: CapabilityKind;
  label: string;
}

export const ENTRY_POINTS: readonly EntryPoint[] = [
  { id: 'mcp-paste', kind: 'mcp_recipe', label: 'Paste MCP config' },
  { id: 'mcp-custom', kind: 'mcp_recipe', label: 'Custom MCP server' },
];

export const SOURCE_LABELS: Record<CapabilitySource, string> = {
  builtin: 'Built-in',
  registry: 'Registry',
  org_catalog: 'Org catalog',
  team_catalog: 'Team catalog',
  local: 'Local',
};

/**
 * needsFlow — a row whose install needs the per-kind flow: any requirement
 * the device does not already hold (a key, a directory, a warning to
 * acknowledge, a sign-in). Zero-requirement rows install in one click.
 */
export function needsFlow(item: CapabilityItem): boolean {
  return (item.requirements ?? []).some((r) => !r.satisfied);
}

/** Human text for an Unavailable reason code (P-5). */
export function unavailableText(reason: string, message?: string): string {
  switch (reason) {
    case 'signed_out':
      return 'Sign in to your organization to browse this catalog.';
    case 'fleet_disabled':
      return 'Fleet is not configured on this device.';
    default:
      return message || 'This source could not be listed.';
  }
}
