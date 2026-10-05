/**
 * Fleet-catalog browse rows of the Capabilities surface
 * (install-framework-01DOGF0B Phase 4 WP08 — the Marketplace fold-in).
 *
 * The retired MarketplaceView was the only browse of the fleet catalog
 * (Catalog_List). Skill and workflow catalog items already list through
 * their install providers (Capability_List), so this module covers what
 * only the Marketplace showed:
 *
 *   - bundle and agent_pack catalog items. Neither kind has an install
 *     provider yet (Phase 3 — WP06/WP07 — has NOT shipped; owner ruling
 *     2026-10-05 pulled Phase 4 forward without it). They keep EXACTLY the
 *     v0.87.0 WP02 honesty posture: listed, Install shown DISABLED with a
 *     visible reason naming the working alternative, never a badge. The
 *     backend refuses them too (core/fleet/catalog_install.go
 *     catalogInstallRefusal). Delete a kind from CATALOG_ONLY_KINDS when its
 *     provider lands — it then lists through Capability_List like skills.
 *   - installed/ residue: a payload an earlier release's badge-only install
 *     downloaded (workflow / agent_pack / bundle). Labelled "Downloaded —
 *     not active", never "Installed", with a "Remove download" action
 *     (Catalog_Uninstall). FR-7's finish-the-install offer is Phase 3 WP08.
 *   - the catalog listing facts the provider rows do not carry
 *     (visibility, published date) and Withdraw (Catalog_Unpublish).
 */
import type { CapabilityKind, CapabilitySource, CatalogItemView } from '@/lib/types';

export interface CatalogOnlyKind {
  kind: Extract<CapabilityKind, 'bundle' | 'agent_pack'>;
  /** Kind filter chip label. */
  label: string;
  /** Singular noun for row tags. */
  noun: string;
  /** Why Install is disabled, naming the working alternative (WP02 copy,
   *  verbatim from the retired MarketplaceView). */
  reason: string;
  /** The working alternative as a route, when it is one. */
  alternative?: { label: string; to: string };
}

export const CATALOG_ONLY_KINDS: readonly CatalogOnlyKind[] = [
  {
    kind: 'bundle',
    label: 'Bundles',
    noun: 'bundle',
    reason:
      "Installing bundles from the org catalog isn't supported yet — nothing on this device would load the download. Install a bundle from Settings › Integrations › Bundles instead.",
    alternative: { label: 'Open Bundles', to: '/bundles' },
  },
  {
    kind: 'agent_pack',
    label: 'Agent packs',
    noun: 'agent pack',
    reason:
      "Installing agent packs from the org catalog isn't supported yet — nothing on this device would load the download. Add agent profiles to the agents folder in your profile directory instead.",
  },
];

export function catalogOnlyKind(kind: string): CatalogOnlyKind | undefined {
  return CATALOG_ONLY_KINDS.find((k) => k.kind === kind);
}

/** Copy for a download an earlier release left under installed/. */
export function residueText(kind: string): string {
  if (kind === 'workflow') {
    return 'An earlier version downloaded this workflow, but nothing on this device uses that download. Install the workflow from its Workflows row in this list (it installs through the install framework), then remove this download.';
  }
  return 'An earlier version downloaded this item, but nothing on this device uses it. Remove the download to clean it up.';
}

/** Fleet visibility → the FR-4 source chip (mirrors capabilities.catalogSource in Go). */
export function catalogSource(visibility: string): CapabilitySource {
  return visibility === 'org_public' ? 'org_catalog' : 'team_catalog';
}

export const VISIBILITY_LABELS: Record<string, string> = {
  private: 'Private',
  team: 'Team',
  org_public: 'Org-public',
};

/** A fleet-catalog browse row. `residue` rows are installed/ downloads. */
export interface CatalogRowData {
  key: string;
  entry: CatalogItemView;
  residue: boolean;
}

function versionLess(a: string, b: string): boolean {
  const as = a.replace(/^v/, '').split('.');
  const bs = b.replace(/^v/, '').split('.');
  for (let i = 0; i < Math.max(as.length, bs.length); i++) {
    const x = as[i] ?? '';
    const y = bs[i] ?? '';
    if (x === y) continue;
    const xi = Number(x);
    const yi = Number(y);
    if (x !== '' && y !== '' && Number.isInteger(xi) && Number.isInteger(yi)) return xi < yi;
    return x < y;
  }
  return false;
}

/**
 * catalogRows — the browse rows the provider list does not already show:
 * the newest version of every bundle / agent_pack item (Install disabled),
 * plus one row per installed/ residue of any non-skill kind.
 */
export function catalogRows(entries: readonly CatalogItemView[]): CatalogRowData[] {
  const newest = new Map<string, CatalogItemView>();
  const out: CatalogRowData[] = [];
  for (const e of entries) {
    if (e.kind === 'skill') continue; // installed state is the slash registry's — provider row
    if (e.installed) {
      out.push({ key: `catalog-residue:${e.kind}:${e.id}@${e.version}`, entry: e, residue: true });
      continue;
    }
    if (!catalogOnlyKind(e.kind)) continue; // workflow: listed by its provider
    const cur = newest.get(e.id);
    if (!cur || versionLess(cur.version, e.version)) newest.set(e.id, e);
  }
  const residueIds = new Set(out.map((r) => `${r.entry.kind}:${r.entry.id}`));
  for (const e of newest.values()) {
    // A residue row for the same item already lists it; don't list it twice.
    if (residueIds.has(`${e.kind}:${e.id}`)) continue;
    out.push({ key: `catalog:${e.kind}:${e.id}`, entry: e, residue: false });
  }
  return out.sort((a, b) => a.entry.slug.localeCompare(b.entry.slug) || a.entry.version.localeCompare(b.entry.version));
}

/**
 * DOM id for the visible reason text (aria-describedby target). Built from
 * id + version, not slug: two rows can share a slug (another version, or a
 * different kind), and duplicate ids break the describedby link (WP02
 * review F2).
 */
export function reasonElId(entry: CatalogItemView): string {
  return `catalog-install-unsupported-${entry.id}-${entry.version}`.replace(/[^A-Za-z0-9_-]/g, '_');
}
