import { describe, it, expect } from 'vitest';
import { isPulledEntryPath, withoutPulledEntries } from '@/lib/pulledContext';
import { buildBatchEntries } from '@/views/contexts/folderBatch';
import type { ContextNode } from '@/lib/types';

const pulled = 'team/_fleet/6b2f6f0e-0000-4000-8000-0000000000e1';

function tree(): ContextNode {
  return {
    name: '',
    path: '',
    kind: 'folder',
    children: [
      {
        name: 'team',
        path: 'team',
        kind: 'folder',
        children: [
          { name: 'notes.md', path: 'team/notes.md', kind: 'file', size: 4 },
          { name: 'Deploy', path: pulled, kind: 'file' },
        ],
      },
      {
        name: 'org',
        path: 'org',
        kind: 'folder',
        children: [{ name: 'Rules', path: 'org/_fleet/abc', kind: 'file' }],
      },
      { name: 'empty', path: 'empty', kind: 'folder', children: [] },
    ],
  };
}

describe('pulledContext', () => {
  it('recognises pulled team/org entry paths only', () => {
    expect(isPulledEntryPath(pulled)).toBe(true);
    expect(isPulledEntryPath('org/_fleet/x')).toBe(true);
    expect(isPulledEntryPath('team/_fleet')).toBe(true);
    expect(isPulledEntryPath('team/notes.md')).toBe(false);
    expect(isPulledEntryPath('personal/_fleet/x')).toBe(false);
    expect(isPulledEntryPath(null)).toBe(false);
  });

  it('withoutPulledEntries drops pulled entries and the folders they emptied', () => {
    const out = withoutPulledEntries(tree());
    const paths = JSON.stringify(out);
    expect(paths).not.toContain('_fleet');
    expect(paths).toContain('team/notes.md');
    expect(out.children?.map((c) => c.path)).toEqual(['team', 'empty']);
  });

  it('a folder share or promote never includes a pulled entry', () => {
    const team = tree().children![0];
    for (const mode of ['share', 'promote'] as const) {
      const entries = buildBatchEntries(team, mode);
      const p = entries.find((e) => e.path === pulled)!;
      expect(p.ineligible).toMatch(/read-only/);
      expect(p.checked).toBe(false);
      expect(entries.find((e) => e.path === 'team/notes.md')!.checked).toBe(true);
    }
  });
});
