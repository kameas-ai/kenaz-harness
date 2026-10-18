/**
 * Catalog lifecycle display (skill-library-01SKLIB01 WP02).
 *
 * A fleet catalog version is active, deprecated or revoked (fleet migrations
 * 0114-0116). The state rides the UNSIGNED catalog list/fetch JSON only —
 * deprecation never changes the signed org bundle, so it changes pixels
 * only: a deprecated item stays installable, and a deprecated org-required
 * copy stays installed. A revoked version cannot be newly installed (fleet's
 * fetch answers 410 item_revoked, which the backend names install.ErrRevoked).
 * Unknown future states are shown verbatim, never hidden.
 */

/** The FR-1 copy for a revoked version (matches fleet.ErrCatalogItemRevoked). */
export const REVOKED_INSTALL_COPY = 'This version was revoked by your org and can no longer be installed.';

export interface LifecycleBearing {
  lifecycle?: string;
  lifecycle_reason?: string;
  superseded_by?: string;
}

export function isRevoked(it: LifecycleBearing | null | undefined): boolean {
  return it?.lifecycle === 'revoked';
}

export interface LifecycleChip {
  label: string;
  /** Tone class for the chip text. */
  tone: string;
  /** Tooltip / accessible description: the org's reason, when sent. */
  title: string;
}

/** The chip to show for an item, or null when it is active / not a catalog item. */
export function lifecycleChip(it: LifecycleBearing | null | undefined): LifecycleChip | null {
  const lc = (it?.lifecycle ?? '').trim();
  if (lc === '' || lc === 'active') return null;
  const reason = it?.lifecycle_reason?.trim() ?? '';
  if (lc === 'deprecated') {
    const parts = ['Deprecated by your org'];
    if (reason) parts.push(reason);
    if (it?.superseded_by) parts.push('a newer version is available');
    return { label: 'Deprecated', tone: 'text-signal-warn', title: parts.join(' — ') };
  }
  if (lc === 'revoked') {
    return { label: 'Revoked', tone: 'text-signal-danger', title: reason ? `Revoked by your org — ${reason}` : 'Revoked by your org' };
  }
  // Forward compatibility: a state this build does not know, shown as sent.
  return { label: lc, tone: 'text-ink-muted', title: reason || `Catalog state: ${lc}` };
}
