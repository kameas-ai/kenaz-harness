/**
 * shortcuts/platform — cross-platform keyboard utilities
 * (keyboard-shortcuts-settings-01KQ8TDR plan §2.5).
 *
 * Pure TS; no Vue imports. Memoised platform detection + canonical
 * binding string helpers + a runtime event matcher.
 *
 * Canonical form: always uses `Cmd` for the primary modifier.
 * At display time and at event-match time, `Cmd` translates to:
 *   - macOS  → metaKey
 *   - Win/Linux → ctrlKey
 *
 * Override knob for tests / dev: set
 *   localStorage.kenaz_shortcut_platform = 'mac' | 'win' | 'linux'
 */

export type Platform = 'mac' | 'win' | 'linux';

let _cachedPlatform: Platform | null = null;

/** detectPlatform — memoised. Uses localStorage override when present. */
export function detectPlatform(): Platform {
  if (_cachedPlatform !== null) return _cachedPlatform;

  if (typeof localStorage !== 'undefined') {
    const override = localStorage.getItem('kenaz_shortcut_platform');
    if (override === 'mac' || override === 'win' || override === 'linux') {
      _cachedPlatform = override;
      return _cachedPlatform;
    }
  }

  if (typeof navigator !== 'undefined') {
    const p = navigator.platform ?? '';
    if (p.startsWith('Mac') || p.startsWith('iPhone') || p.startsWith('iPad')) {
      _cachedPlatform = 'mac';
    } else if (p.startsWith('Win')) {
      _cachedPlatform = 'win';
    } else {
      _cachedPlatform = 'linux';
    }
  } else {
    _cachedPlatform = 'linux';
  }
  return _cachedPlatform;
}

/** Reset memoised platform (useful in tests). */
export function _resetPlatformCache(): void {
  _cachedPlatform = null;
}

/**
 * MODIFIER_ORDER — the canonical ordering used when building binding strings.
 * A parsed key is always serialised in this order so `Cmd+Shift+K` ===
 * `Cmd+Shift+K` regardless of which order modifiers were pressed.
 */
const MODIFIER_ORDER = ['Cmd', 'Ctrl', 'Alt', 'Shift', 'Meta'] as const;

export interface ParsedBinding {
  modifiers: Set<string>;
  key: string;
}

/**
 * parseBinding — split a canonical binding string (e.g. `Cmd+Shift+K`) into
 * its component modifiers and key. Returns `null` if the string is empty.
 */
export function parseBinding(binding: string): ParsedBinding | null {
  if (!binding || binding.trim() === '') return null;
  const parts = binding.split('+');
  if (parts.length === 0) return null;
  const key = parts[parts.length - 1];
  const modifiers = new Set(parts.slice(0, -1));
  return { modifiers, key };
}

/**
 * toCanonical — normalise a keydown event into a canonical binding string.
 *
 * Uses `Cmd` for the platform's primary modifier (metaKey on Mac,
 * ctrlKey on Win/Linux) so the same binding string works cross-platform.
 *
 * Returns `null` for pure-modifier presses (no non-modifier key).
 */
export function toCanonical(e: KeyboardEvent): string | null {
  const modifierKeys = new Set(['Meta', 'Control', 'Alt', 'Shift']);
  if (modifierKeys.has(e.key)) return null; // pure modifier press

  const platform = detectPlatform();
  const parts: string[] = [];

  const primaryModifier = platform === 'mac' ? e.metaKey : e.ctrlKey;
  const ctrlAsSecondary = platform === 'mac' ? e.ctrlKey : false;

  if (primaryModifier) parts.push('Cmd');
  if (ctrlAsSecondary) parts.push('Ctrl');
  if (e.altKey) parts.push('Alt');
  if (e.shiftKey) parts.push('Shift');

  // Normalise key to a consistent display form.
  parts.push(normaliseKey(e.key));

  return parts.join('+');
}

/** normaliseKey — maps KeyboardEvent.key to the canonical display form. */
function normaliseKey(key: string): string {
  const map: Record<string, string> = {
    Enter: 'Enter',
    Escape: 'Escape',
    ArrowUp: 'Up',
    ArrowDown: 'Down',
    ArrowLeft: 'Left',
    ArrowRight: 'Right',
    Backspace: 'Backspace',
    Delete: 'Delete',
    Tab: 'Tab',
    ' ': 'Space',
  };
  return map[key] ?? (key.length === 1 ? key.toUpperCase() : key);
}

/**
 * matchesEvent — returns true if the canonical `binding` string matches
 * the given KeyboardEvent. Handles cross-platform `Cmd` translation.
 */
export function matchesEvent(binding: string, e: KeyboardEvent): boolean {
  const parsed = parseBinding(binding);
  if (!parsed) return false;

  const platform = detectPlatform();

  // Check modifiers.
  const wantsCmd = parsed.modifiers.has('Cmd');
  const wantsCtrl = parsed.modifiers.has('Ctrl');
  const wantsAlt = parsed.modifiers.has('Alt');
  const wantsShift = parsed.modifiers.has('Shift');

  // On Mac, `Cmd` means metaKey. On Win/Linux, `Cmd` means ctrlKey.
  const cmdActive = platform === 'mac' ? e.metaKey : e.ctrlKey;
  const ctrlActive = platform === 'mac' ? e.ctrlKey : false;

  if (wantsCmd !== cmdActive) return false;
  if (wantsCtrl !== ctrlActive) return false;
  if (wantsAlt !== e.altKey) return false;
  if (wantsShift !== e.shiftKey) return false;

  const eventKey = normaliseKey(e.key);
  if (parsed.key !== eventKey) return false;

  return true;
}

/**
 * formatBinding — render a canonical binding string using platform-
 * appropriate symbols.
 *
 * Mac:     ⌘⌥⇧⌃ + key
 * Win/Linux: Ctrl+Alt+Shift + key
 */
export function formatBinding(binding: string): string {
  const parsed = parseBinding(binding);
  if (!parsed) return binding;

  const platform = detectPlatform();
  const parts: string[] = [];

  // Modifiers in order.
  for (const mod of MODIFIER_ORDER) {
    if (!parsed.modifiers.has(mod)) continue;
    if (platform === 'mac') {
      parts.push(MAC_SYMBOLS[mod] ?? mod);
    } else {
      parts.push(WIN_SYMBOLS[mod] ?? mod);
    }
  }

  // Key display.
  const key = parsed.key;
  if (platform === 'mac') {
    parts.push(MAC_KEY_SYMBOLS[key] ?? key);
  } else {
    parts.push(WIN_KEY_SYMBOLS[key] ?? key);
  }

  return platform === 'mac' ? parts.join('') : parts.join('+');
}

const MAC_SYMBOLS: Record<string, string> = {
  Cmd: '⌘',
  Ctrl: '⌃',
  Alt: '⌥',
  Shift: '⇧',
  Meta: '⌘',
};

const WIN_SYMBOLS: Record<string, string> = {
  Cmd: 'Ctrl',
  Ctrl: 'Ctrl',
  Alt: 'Alt',
  Shift: 'Shift',
  Meta: 'Win',
};

const MAC_KEY_SYMBOLS: Record<string, string> = {
  Enter: '⏎',
  Escape: '⎋',
  Backspace: '⌫',
  Delete: '⌦',
  Tab: '⇥',
  Up: '↑',
  Down: '↓',
  Left: '←',
  Right: '→',
  Space: '␣',
};

const WIN_KEY_SYMBOLS: Record<string, string> = {};
