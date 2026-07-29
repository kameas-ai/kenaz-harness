/**
 * useTheme — light / dark / system theme. Persisted via SettingsStore
 * (WP13). FR-006, NFR-002.
 *
 * Design-system-adoption note: the standalone light/dark/system toggle
 * used to drive a bespoke, harness-local light palette (tokens.css
 * `:root:not(.dark)`) that diverged from the DS's four named themes — a
 * genuine "5th theme" with its own `--ink-subtle` value. That palette has
 * been dropped; the harness now offers exactly the DS's theme set.
 * `dark` maps to the DS `ink` theme, `light` maps to the DS `linen`
 * theme (see REFRESH.md "Theme model" for the decision record — this is
 * flagged in the adoption PR as operator-vetoable).
 */

import { ref, watch, onMounted, type Ref } from 'vue';
import { useHarnessClient } from './harnessClientContext';
import type { Theme } from './types';

interface UseThemeResult {
  theme: Ref<Theme>;
  set(t: Theme): Promise<void>;
  cycle(): void;
}

const order: Theme[] = ['system', 'dark', 'light'];

/**
 * Host named-theme override — spec 073 (workspace) FR-3/FR-4.
 *
 * When the harness is embedded in a kenaz workbench, the chrome appends
 * `?theme=<ink|linen|azure|paper>` to the iframe src (CONTRACTS.md §
 * kenaz.workbench.surface-theme-param v1). The override is EPHEMERAL: it
 * sets `data-theme` (DS tokens.css named blocks) + syncs the `.dark`
 * class for Tailwind, but never touches the persisted light/dark/system
 * setting. Standalone harness (no param) uses the same `data-theme`
 * mechanism now (see `applyClass` below) — the override and the
 * standalone toggle are the same axis, not two parallel ones.
 */
export const HOST_THEMES = ['ink', 'linen', 'azure', 'paper'] as const;
const DARK_HOST_THEMES = new Set<string>(['ink', 'azure']);

/** Standalone light/dark/system toggle → DS named theme. See module doc. */
const STANDALONE_DARK_THEME = 'ink';
const STANDALONE_LIGHT_THEME = 'linen';

export function hostThemeParam(search?: string): string | null {
  const q = search ?? (typeof window !== 'undefined' ? window.location.search : '');
  const t = new URLSearchParams(q).get('theme');
  return t && (HOST_THEMES as readonly string[]).includes(t) ? t : null;
}

export function applyHostTheme(name: string): void {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  root.setAttribute('data-theme', name);
  const isDark = DARK_HOST_THEMES.has(name);
  root.classList.toggle('dark', isDark);
  root.style.colorScheme = isDark ? 'dark' : 'light';
}

function applyClass(t: Theme) {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  const isDark =
    t === 'dark' ||
    (t === 'system' &&
      typeof window !== 'undefined' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches);
  // Standalone toggle now resolves to a DS named theme directly instead of
  // a bespoke light palette — see module doc. `.dark` is still synced
  // alongside `data-theme` so any not-yet-migrated `dark:` Tailwind
  // variant keeps working.
  root.setAttribute('data-theme', isDark ? STANDALONE_DARK_THEME : STANDALONE_LIGHT_THEME);
  root.classList.toggle('dark', isDark);
  root.style.colorScheme = isDark ? 'dark' : 'light';
}

export function useTheme(): UseThemeResult {
  const client = useHarnessClient();
  const theme = ref<Theme>('system');

  async function set(t: Theme) {
    theme.value = t;
    applyClass(t);
    try {
      await client.settings.saveTheme(t);
    } catch {
      // best-effort; theme still applies in-memory
    }
  }

  function cycle() {
    const i = order.indexOf(theme.value);
    const next = order[(i + 1) % order.length];
    void set(next);
  }

  onMounted(async () => {
    // Spec 073 FR-4: the host param wins for this session and skips the
    // stored setting entirely — nothing is read or written, so the
    // harness settings store is untouched by an override session.
    const host = hostThemeParam();
    if (host) {
      applyHostTheme(host);
      return;
    }
    try {
      const t = await client.settings.loadTheme();
      theme.value = t;
      applyClass(t);
    } catch {
      applyClass('system');
    }
  });

  watch(theme, applyClass);

  return { theme, set, cycle };
}
