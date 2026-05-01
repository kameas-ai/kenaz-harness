import { describe, it, expect } from 'vitest';
import {
  SHORTCUTS,
  RESERVED_SYSTEM_BINDINGS,
  resolveBinding,
  groupByCategory,
  getShortcut,
} from '../registry';

describe('shortcuts/registry', () => {
  it('has 17 v1 shortcuts', () => {
    expect(SHORTCUTS).toHaveLength(17);
  });

  it('every shortcut has a non-empty id, label, defaultBinding', () => {
    for (const s of SHORTCUTS) {
      expect(s.id).toBeTruthy();
      expect(s.label).toBeTruthy();
      expect(s.defaultBinding).toBeTruthy();
    }
  });

  it('no two shortcuts share the same id', () => {
    const ids = SHORTCUTS.map((s) => s.id);
    const unique = new Set(ids);
    expect(unique.size).toBe(ids.length);
  });

  it('no default binding collides within the same scope', () => {
    const seen = new Map<string, string>(); // scope+binding -> id
    for (const s of SHORTCUTS) {
      const key = `${s.scope}:${s.defaultBinding}`;
      if (seen.has(key)) {
        throw new Error(
          `Binding collision: ${s.id} and ${seen.get(key)} both use ${s.defaultBinding} in scope ${s.scope}`,
        );
      }
      seen.set(key, s.id);
    }
  });

  it('no default binding is a reserved system binding', () => {
    for (const s of SHORTCUTS) {
      expect(
        RESERVED_SYSTEM_BINDINGS.has(s.defaultBinding),
        `${s.id} uses reserved binding ${s.defaultBinding}`,
      ).toBe(false);
    }
  });

  it('resolveBinding returns default when no override', () => {
    expect(resolveBinding('chat.send', {})).toBe('Cmd+Enter');
  });

  it('resolveBinding returns override when set', () => {
    expect(resolveBinding('chat.send', { 'chat.send': 'Cmd+Shift+Enter' })).toBe(
      'Cmd+Shift+Enter',
    );
  });

  it('resolveBinding returns default when override is empty string', () => {
    expect(resolveBinding('chat.send', { 'chat.send': '' })).toBe('Cmd+Enter');
  });

  it('resolveBinding returns undefined for unknown id', () => {
    expect(resolveBinding('nonexistent.id', {})).toBeUndefined();
  });

  it('getShortcut looks up by id', () => {
    const s = getShortcut('chat.send');
    expect(s?.label).toBe('Send message');
  });

  it('groupByCategory groups all shortcuts', () => {
    const groups = groupByCategory(SHORTCUTS);
    let total = 0;
    for (const entries of groups.values()) {
      total += entries.length;
    }
    expect(total).toBe(SHORTCUTS.length);
  });

  it('chat.send id is present', () => {
    const s = SHORTCUTS.find((x) => x.id === 'chat.send');
    expect(s).toBeDefined();
    expect(s?.defaultBinding).toBe('Cmd+Enter');
  });

  it('nav.focus-search uses Cmd+F', () => {
    const s = SHORTCUTS.find((x) => x.id === 'nav.focus-search');
    expect(s?.defaultBinding).toBe('Cmd+F');
  });

  it('editing.dismiss-modal uses Escape', () => {
    const s = SHORTCUTS.find((x) => x.id === 'editing.dismiss-modal');
    expect(s?.defaultBinding).toBe('Escape');
  });

  it('help.cheat-sheet uses ?', () => {
    const s = SHORTCUTS.find((x) => x.id === 'help.cheat-sheet');
    expect(s?.defaultBinding).toBe('?');
  });
});
