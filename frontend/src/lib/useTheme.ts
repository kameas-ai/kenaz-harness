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

function applyClass(t: Theme) {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
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
