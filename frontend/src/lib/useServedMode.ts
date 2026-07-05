/**
 * useServedMode — composable that detects whether the frontend is running
 * in served mode (HTTP/WS transport, not inside the Wails desktop runtime).
 *
 * Detection: served mode sets a <meta name="harness-token"> tag in the
 * document head AND does NOT expose window.go.rpc.Bindings.  We use the
 * absence of the Wails binding surface as the primary signal (works in both
 * production and test environments).
 *
 * (served-mode-honesty-WZQR1ZJE WP02 / FR-002)
 */

import { ref, readonly, type Ref } from 'vue';

/** Cached result — computed once at module init time (no reactivity needed). */
const _isServedMode = ref<boolean>(detectServedMode());

function detectServedMode(): boolean {
  if (typeof window === 'undefined') return false;
  // Wails injects window.go.rpc.Bindings at startup; its absence means we're
  // running in a plain browser (i.e. served mode).
  const w = window as unknown as { go?: { rpc?: { Bindings?: unknown } } };
  return !w.go?.rpc?.Bindings;
}

/**
 * useServedMode returns a readonly boolean ref that is true when the frontend
 * is running in served mode (no Wails bridge available).
 */
export function useServedMode(): Readonly<Ref<boolean>> {
  return readonly(_isServedMode);
}

/**
 * isServedMode — non-reactive helper for use outside of Vue setup contexts
 * (e.g. in client factories).
 */
export function isServedMode(): boolean {
  return _isServedMode.value;
}
