/**
 * Per-device preference for the Context health chip's expanded state
 * (knowledge-home-01DOGF0E WP06, spec FR-8). localStorage, not a backend
 * Setting: no settings schema, no migration. Versioned so a future shape
 * change can ignore old values instead of misreading them. A profile with
 * no stored value — every upgraded install — reads as collapsed.
 */
export const CONTEXT_HEALTH_EXPANDED_KEY = 'harness.knowledge.contextHealthExpanded.v1';

export function readContextHealthExpanded(): boolean {
  try {
    return localStorage.getItem(CONTEXT_HEALTH_EXPANDED_KEY) === '1';
  } catch {
    return false;
  }
}

export function writeContextHealthExpanded(v: boolean): void {
  try {
    localStorage.setItem(CONTEXT_HEALTH_EXPANDED_KEY, v ? '1' : '0');
  } catch {
    // Storage blocked — the choice just doesn't survive a reload.
  }
}
