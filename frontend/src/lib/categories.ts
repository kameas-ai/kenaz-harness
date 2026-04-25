/**
 * Category color registry (FR-001d, plan §5.3). Five Kenaz categories
 * preserved verbatim + ten harness extensions. `token` references a
 * CSS variable defined in tokens.css; entries marked TBD are pending
 * Kenaz design review (open question §9.3 of spec).
 */

export const CATEGORIES = [
  // Kenaz's existing five — sourced from Kenaz palette.
  'FILESYSTEM',
  'PROCESS',
  'CLIPBOARD',
  'NETWORK',
  'KEYSTROKE',
  // Harness extensions — proposed-from-Kenaz-palette assignments per §5.3.
  'LLM',
  'MCP',
  'A2A',
  'POLICY',
  'TRUST',
  'BUNDLE',
  'CONTEXT',
  'SECRETS',
  'STORAGE',
  'SCHEDULER',
] as const;

export type Category = (typeof CATEGORIES)[number];

interface CategoryDef {
  /** CSS variable name (no `var(...)` wrapper). */
  token: string;
  label: string;
  /** Pending Kenaz design sign-off per FR-001d open question §9.3. */
  tbd?: boolean;
}

export const CATEGORY_REGISTRY: Readonly<Record<Category, CategoryDef>> = {
  // Kenaz five.
  FILESYSTEM: { token: '--info', label: 'FILESYSTEM' },
  PROCESS: { token: '--ok', label: 'PROCESS' },
  CLIPBOARD: { token: '--warn', label: 'CLIPBOARD' },
  NETWORK: { token: '--danger', label: 'NETWORK' },
  // Yellow placeholder — Kenaz publishes the canonical KEYSTROKE token.
  KEYSTROKE: { token: '--warn', label: 'KEYSTROKE', tbd: true },

  // Harness extensions.
  LLM: { token: '--accent', label: 'LLM' },
  MCP: { token: '--violet', label: 'MCP' },
  A2A: { token: '--signal-git', label: 'A2A' },
  POLICY: { token: '--danger', label: 'POLICY' },
  TRUST: { token: '--ok', label: 'TRUST' },
  BUNDLE: { token: '--warn', label: 'BUNDLE', tbd: true },
  CONTEXT: { token: '--info', label: 'CONTEXT', tbd: true },
  SECRETS: { token: '--signal-neutral', label: 'SECRETS' },
  STORAGE: { token: '--ok', label: 'STORAGE', tbd: true },
  SCHEDULER: { token: '--violet', label: 'SCHEDULER', tbd: true },
};

export function categoryColor(category: Category): string {
  return `var(${CATEGORY_REGISTRY[category].token})`;
}
