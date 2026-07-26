/**
 * useTheme — light / dark / system theme. Persisted via SettingsStore
 * (WP13). FR-006, NFR-002.
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
 * sets `data-theme` (tokens.css named blocks) + syncs the `.dark` class
 * for Tailwind, but never touches the persisted light/dark/system
 * setting. Standalone harness (no param) is byte-identical to before.
 * A manual in-harness theme change exits the override (applyClass clears
 * data-theme).
 */
export const HOST_THEMES = ['ink', 'linen', 'azure', 'paper'] as const;
const DARK_HOST_THEMES = new Set<string>(['ink', 'azure']);

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
  // Exiting any host named-theme override: an explicit light/dark/system
  // application always clears data-theme so the two axes never mix.
  root.removeAttribute('data-theme');
  const isDark =
    t === 'dark' ||
    (t === 'system' &&
      typeof window !== 'undefined' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches);
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
