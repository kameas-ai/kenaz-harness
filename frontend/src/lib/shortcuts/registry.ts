/**
 * shortcuts/registry — single source of truth for every keyboard shortcut
 * in the harness (keyboard-shortcuts-settings-01KQ8TDR plan §2.1).
 *
 * Pure data + small helpers; no Vue imports. Components and composables
 * READ from this file; they never hard-code binding strings.
 *
 * Binding format: `Modifier+Modifier+Key` with modifiers in fixed order
 * Cmd|Ctrl|Alt|Shift. Registry stores canonical form using `Cmd`
 * (translated to `Ctrl` on Win/Linux at evaluation/display time).
 */

export type ShortcutCategory = 'Chat' | 'Navigation' | 'Editing' | 'Selection';
export type ShortcutScope = 'global' | 'chat' | 'modal' | 'sidebar';

export interface ShortcutDef {
  /** Stable functional identifier (e.g. 'chat.send'). */
  id: string;
  /** Short human-readable label. */
  label: string;
  /** Longer description shown in cheat-sheet and settings. */
  description: string;
  category: ShortcutCategory;
  scope: ShortcutScope;
  /** Canonical default binding using `Cmd` for the primary modifier. */
  defaultBinding: string;
}

/**
 * RESERVED_SYSTEM_BINDINGS — combos that the registry must never accept
 * as defaults or user overrides (C-002). Stored in canonical form.
 */
export const RESERVED_SYSTEM_BINDINGS = new Set<string>([
  'Cmd+Tab',
  'Cmd+Q',
  'Cmd+W',
  'Cmd+Space',
  'Cmd+H',
  'Alt+F4',
  'Ctrl+Alt+Delete',
]);

/**
 * SHORTCUTS — the v1 registry (17 entries, plan §2.8).
 * Order is display order inside each category group.
 */
export const SHORTCUTS: ReadonlyArray<ShortcutDef> = [
  // ── Chat ──────────────────────────────────────────────────────────────
  {
    id: 'chat.send',
    label: 'Send message',
    description: 'Submit the current composer text to the model.',
    category: 'Chat',
    scope: 'chat',
    defaultBinding: 'Cmd+Enter',
  },
  {
    id: 'chat.newline',
    label: 'Insert newline in composer',
    description: 'Add a line break without submitting.',
    category: 'Chat',
    scope: 'chat',
    defaultBinding: 'Shift+Enter',
  },
  {
    id: 'chat.regenerate',
    label: 'Regenerate response',
    description: 'Ask the model to re-answer the last user turn.',
    category: 'Chat',
    scope: 'chat',
    defaultBinding: 'Cmd+R',
  },
  {
    id: 'chat.copy-last',
    label: 'Copy last assistant message',
    description: 'Copy the most recent assistant response to the clipboard.',
    category: 'Chat',
    scope: 'chat',
    defaultBinding: 'Cmd+Shift+C',
  },

  // ── Navigation ────────────────────────────────────────────────────────
  {
    id: 'nav.new-session',
    label: 'New session',
    description: 'Open a fresh chat session.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+N',
  },
  {
    id: 'nav.focus-search',
    label: 'Focus cross-session search',
    description: 'Jump to the session-search input.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+F',
  },
  {
    id: 'nav.focus-composer',
    label: 'Focus composer',
    description: 'Move keyboard focus to the chat composer.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+L',
  },
  {
    id: 'nav.prev-session',
    label: 'Previous session',
    description: 'Navigate to the session above in the sidebar list.',
    category: 'Navigation',
    scope: 'sidebar',
    defaultBinding: 'Cmd+[',
  },
  {
    id: 'nav.next-session',
    label: 'Next session',
    description: 'Navigate to the session below in the sidebar list.',
    category: 'Navigation',
    scope: 'sidebar',
    defaultBinding: 'Cmd+]',
  },
  {
    id: 'nav.toggle-sidebar',
    label: 'Toggle sidebar',
    description: 'Show or hide the left-rail session list.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+\\',
  },
  {
    id: 'nav.toggle-full-history',
    label: 'Toggle compaction full-history view',
    description: 'Expand or collapse the compacted history view.',
    category: 'Navigation',
    scope: 'chat',
    defaultBinding: 'Cmd+Shift+H',
  },
  {
    id: 'nav.open-settings',
    label: 'Open settings',
    description: 'Navigate to the Settings panel.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+,',
  },
  {
    id: 'nav.open-tools',
    label: 'Open Tools view',
    description: 'Navigate to the Tools management panel.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+Shift+T',
  },
  {
    id: 'nav.command-palette',
    label: 'Open command palette',
    description: 'Open the quick-action command palette.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: 'Cmd+K',
  },
  {
    id: 'help.cheat-sheet',
    label: 'Show keyboard cheat sheet',
    description: 'Display an overlay of all registered shortcuts.',
    category: 'Navigation',
    scope: 'global',
    defaultBinding: '?',
  },

  // ── Editing ───────────────────────────────────────────────────────────
  {
    id: 'editing.dismiss-modal',
    label: 'Dismiss modal',
    description: 'Close the currently open modal or overlay.',
    category: 'Editing',
    scope: 'modal',
    defaultBinding: 'Escape',
  },

  // ── Selection ─────────────────────────────────────────────────────────
  {
    id: 'selection.select-all-msg',
    label: 'Select all in current message',
    description: 'Select all text in the focused message.',
    category: 'Selection',
    scope: 'chat',
    defaultBinding: 'Cmd+A',
  },
];

/** Index by id for O(1) lookup. */
const BY_ID = new Map<string, ShortcutDef>(SHORTCUTS.map((s) => [s.id, s]));

/** Look up a shortcut definition by its stable id. */
export function getShortcut(id: string): ShortcutDef | undefined {
  return BY_ID.get(id);
}

/**
 * resolveBinding — return the effective binding for `id`.
 *
 * If `overrides` contains a non-empty entry for `id`, that wins.
 * Otherwise falls back to the registry default. Returns `undefined`
 * only when `id` is unknown.
 */
export function resolveBinding(
  id: string,
  overrides: Record<string, string>,
): string | undefined {
  const def = BY_ID.get(id);
  if (!def) return undefined;
  const override = overrides[id];
  if (override && override.trim() !== '') return override;
  return def.defaultBinding;
}

/**
 * groupByCategory — returns the shortcuts array partitioned into an
 * ordered map of category → entries. Order follows the SHORTCUTS array.
 */
export function groupByCategory(
  shortcuts: ReadonlyArray<ShortcutDef>,
): Map<ShortcutCategory, ShortcutDef[]> {
  const out = new Map<ShortcutCategory, ShortcutDef[]>();
  for (const s of shortcuts) {
    const bucket = out.get(s.category) ?? [];
    bucket.push(s);
    out.set(s.category, bucket);
  }
  return out;
}
