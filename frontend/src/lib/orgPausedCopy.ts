/**
 * orgPausedCopy.ts — the one copy table for a paused org (kenaz-fleet PR 206:
 * a Kameas-staff "pause paid features" hold — billing/abuse control,
 * reversible, nothing deleted).
 *
 * Every fleet-health surface (Settings › Account, the Sync / Memory /
 * Compliance panels, the LegendBar fleet chip) renders this state instead of
 * its tier-gated copy. A pause is NOT a plan problem: no "upgrade" / "your
 * plan" / "Pro+" wording may appear on this path (pinned by
 * orgPausedCopy.spec.ts). Data-rights actions (forget / export / erase) stay
 * enabled while paused.
 */

/** The five categories fleet sends (exact strings, confirmed for PR 206). */
export type OrgPausedCategory = 'billing_review' | 'security' | 'abuse' | 'legal' | 'other';

export const ORG_PAUSED_CATEGORIES: readonly OrgPausedCategory[] = [
  'billing_review',
  'security',
  'abuse',
  'legal',
  'other',
] as const;

/** The banner's single headline — the same for every category. */
export const ORG_PAUSED_TITLE =
  "Paused by your organization's account status — contact your admin";

/** Per-category one-liner shown under the headline. */
export const ORG_PAUSED_CATEGORY_COPY: Record<OrgPausedCategory, string> = {
  billing_review: "Your organization's billing is under review.",
  security: 'Your organization is under a security review.',
  abuse: "Your organization's usage is under review.",
  legal: 'A legal matter is under review for your organization.',
  other: "Your organization's paid features are temporarily on hold.",
};

/** Reassurance line: what still works while paused. */
export const ORG_PAUSED_DATA_RIGHTS_NOTE =
  'Nothing has been deleted. You can still export or delete your data.';

/** Normalise an absent or unknown category to "other". */
export function normalizeOrgPausedCategory(c: string | null | undefined): OrgPausedCategory {
  return (ORG_PAUSED_CATEGORIES as readonly string[]).includes(c ?? '')
    ? (c as OrgPausedCategory)
    : 'other';
}

/** The per-category one-liner for a (possibly unknown) category. */
export function orgPausedCategoryLine(c: string | null | undefined): string {
  return ORG_PAUSED_CATEGORY_COPY[normalizeOrgPausedCategory(c)];
}

/** Lane / status reason code every fleet consumer reports while paused. */
export const ORG_PAUSED_REASON = 'org_paused';
