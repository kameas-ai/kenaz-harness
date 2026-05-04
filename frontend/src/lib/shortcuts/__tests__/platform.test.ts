import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
  detectPlatform,
  toCanonical,
  matchesEvent,
  formatBinding,
  _resetPlatformCache,
} from '../platform';

// Helper to build a minimal KeyboardEvent-like object.
function fakeEvent(
  key: string,
  opts: {
    metaKey?: boolean;
    ctrlKey?: boolean;
    altKey?: boolean;
    shiftKey?: boolean;
  } = {},
): KeyboardEvent {
  return {
    key,
    metaKey: opts.metaKey ?? false,
    ctrlKey: opts.ctrlKey ?? false,
    altKey: opts.altKey ?? false,
    shiftKey: opts.shiftKey ?? false,
  } as KeyboardEvent;
}

describe('shortcuts/platform', () => {
  beforeEach(() => {
    _resetPlatformCache();
  });

  afterEach(() => {
    // Clean up localStorage override.
    if (typeof localStorage !== 'undefined') {
      localStorage.removeItem('kaneaz_shortcut_platform');
    }
    _resetPlatformCache();
  });

  describe('detectPlatform', () => {
    it('returns mac/win/linux via localStorage override', () => {
      localStorage.setItem('kaneaz_shortcut_platform', 'mac');
      expect(detectPlatform()).toBe('mac');

      _resetPlatformCache();
      localStorage.setItem('kaneaz_shortcut_platform', 'win');
      expect(detectPlatform()).toBe('win');

      _resetPlatformCache();
      localStorage.setItem('kaneaz_shortcut_platform', 'linux');
      expect(detectPlatform()).toBe('linux');
    });
  });

  describe('toCanonical — mac platform', () => {
    beforeEach(() => {
      localStorage.setItem('kaneaz_shortcut_platform', 'mac');
    });

    it('Cmd+K from metaKey + k', () => {
      const result = toCanonical(fakeEvent('k', { metaKey: true }));
      expect(result).toBe('Cmd+K');
    });

    it('Cmd+Shift+K from metaKey + shiftKey + k', () => {
      const result = toCanonical(fakeEvent('k', { metaKey: true, shiftKey: true }));
      expect(result).toBe('Cmd+Shift+K');
    });

    it('Escape with no modifiers', () => {
      const result = toCanonical(fakeEvent('Escape'));
      expect(result).toBe('Escape');
    });

    it('pure modifier press returns null', () => {
      const result = toCanonical(fakeEvent('Meta', { metaKey: true }));
      expect(result).toBeNull();
    });

    it('? with no modifiers', () => {
      const result = toCanonical(fakeEvent('?'));
      expect(result).toBe('?');
    });
  });

  describe('toCanonical — win platform', () => {
    beforeEach(() => {
      localStorage.setItem('kaneaz_shortcut_platform', 'win');
    });

    it('Cmd+K from ctrlKey + k on Win', () => {
      const result = toCanonical(fakeEvent('k', { ctrlKey: true }));
      expect(result).toBe('Cmd+K');
    });

    it('Cmd+Shift+Enter from ctrlKey + shiftKey + Enter on Win', () => {
      const result = toCanonical(
        fakeEvent('Enter', { ctrlKey: true, shiftKey: true }),
      );
      expect(result).toBe('Cmd+Shift+Enter');
    });
  });

  describe('matchesEvent — mac platform', () => {
    beforeEach(() => {
      localStorage.setItem('kaneaz_shortcut_platform', 'mac');
    });

    it('Cmd+K matches metaKey+k on Mac', () => {
      expect(matchesEvent('Cmd+K', fakeEvent('k', { metaKey: true }))).toBe(true);
    });

    it('Cmd+K does NOT match ctrlKey+k on Mac', () => {
      expect(matchesEvent('Cmd+K', fakeEvent('k', { ctrlKey: true }))).toBe(false);
    });

    it('Escape matches bare Escape', () => {
      expect(matchesEvent('Escape', fakeEvent('Escape'))).toBe(true);
    });

    it('? matches bare ? key', () => {
      expect(matchesEvent('?', fakeEvent('?'))).toBe(true);
    });
  });

  describe('matchesEvent — win platform', () => {
    beforeEach(() => {
      localStorage.setItem('kaneaz_shortcut_platform', 'win');
    });

    it('Cmd+K matches ctrlKey+k on Win', () => {
      expect(matchesEvent('Cmd+K', fakeEvent('k', { ctrlKey: true }))).toBe(true);
    });

    it('Cmd+K does NOT match metaKey+k on Win', () => {
      expect(matchesEvent('Cmd+K', fakeEvent('k', { metaKey: true }))).toBe(false);
    });
  });

  describe('formatBinding — mac platform', () => {
    beforeEach(() => {
      localStorage.setItem('kaneaz_shortcut_platform', 'mac');
    });

    it('Cmd+Enter → ⌘⏎', () => {
      expect(formatBinding('Cmd+Enter')).toBe('⌘⏎');
    });

    it('Cmd+Shift+K → ⌘⇧K', () => {
      expect(formatBinding('Cmd+Shift+K')).toBe('⌘⇧K');
    });

    it('Escape → ⎋', () => {
      expect(formatBinding('Escape')).toBe('⎋');
    });

    it('? → ?', () => {
      expect(formatBinding('?')).toBe('?');
    });
  });

  describe('formatBinding — win platform', () => {
    beforeEach(() => {
      localStorage.setItem('kaneaz_shortcut_platform', 'win');
    });

    it('Cmd+Enter → Ctrl+Enter', () => {
      expect(formatBinding('Cmd+Enter')).toBe('Ctrl+Enter');
    });

    it('Cmd+Shift+K → Ctrl+Shift+K', () => {
      expect(formatBinding('Cmd+Shift+K')).toBe('Ctrl+Shift+K');
    });
  });
});
