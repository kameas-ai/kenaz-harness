import { describe, it, expect } from 'vitest';
import { CATEGORIES, CATEGORY_REGISTRY, categoryColor } from '@/lib/categories';

describe('Category registry (FR-001d, plan §5.3)', () => {
  it('preserves the 5 Kenaz categories + 10 harness extensions = 15 entries', () => {
    expect(CATEGORIES.length).toBe(15);
    expect(CATEGORIES).toContain('FILESYSTEM');
    expect(CATEGORIES).toContain('PROCESS');
    expect(CATEGORIES).toContain('CLIPBOARD');
    expect(CATEGORIES).toContain('NETWORK');
    expect(CATEGORIES).toContain('KEYSTROKE');
    expect(CATEGORIES).toContain('LLM');
    expect(CATEGORIES).toContain('MCP');
    expect(CATEGORIES).toContain('A2A');
    expect(CATEGORIES).toContain('POLICY');
    expect(CATEGORIES).toContain('TRUST');
    expect(CATEGORIES).toContain('BUNDLE');
    expect(CATEGORIES).toContain('CONTEXT');
    expect(CATEGORIES).toContain('SECRETS');
    expect(CATEGORIES).toContain('STORAGE');
    expect(CATEGORIES).toContain('SCHEDULER');
  });

  it('every category resolves to a token CSS variable', () => {
    for (const cat of CATEGORIES) {
      const def = CATEGORY_REGISTRY[cat];
      expect(def.token.startsWith('--')).toBe(true);
      expect(categoryColor(cat)).toBe(`var(${def.token})`);
    }
  });
});
