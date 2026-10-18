/**
 * Display labels shared by the "Add capability" surface and its plugins
 * (install-framework-01DOGF0B). Kept apart from registry.ts so plugins can
 * import them without an import cycle through the plugin table.
 */
import type { CapabilitySource } from '@/lib/types';

export const SOURCE_LABELS: Record<CapabilitySource, string> = {
  builtin: 'Built-in',
  registry: 'Registry',
  org_catalog: 'Org catalog',
  team_catalog: 'Team catalog',
  local: 'Local',
};
