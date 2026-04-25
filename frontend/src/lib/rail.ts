/**
 * rail — primary-surface registry consumed by LeftRail. Surfaces lower
 * in the list register through this module to avoid hard-coding the
 * order in LeftRail.vue. (LeftRail currently inlines defaults; this
 * registry is the v1.x extension point.)
 */

export interface RailEntryDef {
  id: string;
  label: string;
  to: string;
  /** Lower numbers render closer to the top. */
  order: number;
}

const registry = new Map<string, RailEntryDef>();

export function registerRailEntry(e: RailEntryDef): () => void {
  registry.set(e.id, e);
  return () => registry.delete(e.id);
}

export function listRailEntries(): RailEntryDef[] {
  return Array.from(registry.values()).sort((a, b) => a.order - b.order);
}
