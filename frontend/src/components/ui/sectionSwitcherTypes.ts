/** One tab in a SectionSwitcher (knowledge-home-01DOGF0E WP02). */
export interface SectionSwitcherItem {
  /** Path segment under basePath, also the test-id suffix. */
  id: string;
  label: string;
  /** Tooltip — one line saying what the section is. */
  hint?: string;
}
