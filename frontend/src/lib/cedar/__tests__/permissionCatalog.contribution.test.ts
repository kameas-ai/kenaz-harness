import { describe, it, expect } from 'vitest';
import {
  PERMISSION_CATALOG,
  type PermissionCatalogEntry,
} from '../permissionCatalog.contribution';

describe('PERMISSION_CATALOG', () => {
  it('contains exactly 4 resource family entries', () => {
    expect(PERMISSION_CATALOG).toHaveLength(4);
  });

  const EXPECTED_FAMILIES = ['Credential', 'BashCommand', 'FilesystemOp', 'Tool'] as const;

  it('contains all 4 required resource families', () => {
    const families = PERMISSION_CATALOG.map((e) => e.family);
    for (const expected of EXPECTED_FAMILIES) {
      expect(families).toContain(expected);
    }
  });

  it('each entry has a non-empty entityType', () => {
    for (const entry of PERMISSION_CATALOG) {
      expect(entry.entityType.length).toBeGreaterThan(0);
    }
  });

  it('each entry has at least one action', () => {
    for (const entry of PERMISSION_CATALOG) {
      expect(entry.actions.length).toBeGreaterThan(0);
      for (const action of entry.actions) {
        expect(action.length).toBeGreaterThan(0);
      }
    }
  });

  it('each entry has a non-empty examplePermit body', () => {
    for (const entry of PERMISSION_CATALOG) {
      expect(entry.examplePermit.trim().length).toBeGreaterThan(0);
    }
  });

  it('each entry has a non-empty exampleForbid body', () => {
    for (const entry of PERMISSION_CATALOG) {
      expect(entry.exampleForbid.trim().length).toBeGreaterThan(0);
    }
  });

  it('Credential entry has use_credential action', () => {
    const entry = PERMISSION_CATALOG.find((e) => e.family === 'Credential');
    expect(entry).toBeDefined();
    expect(entry!.actions).toContain('use_credential');
  });

  it('BashCommand entry has run_bash_command action', () => {
    const entry = PERMISSION_CATALOG.find((e) => e.family === 'BashCommand');
    expect(entry).toBeDefined();
    expect(entry!.actions).toContain('run_bash_command');
  });

  it('FilesystemOp entry has read_filesystem and write_filesystem actions', () => {
    const entry = PERMISSION_CATALOG.find((e) => e.family === 'FilesystemOp');
    expect(entry).toBeDefined();
    expect(entry!.actions).toContain('read_filesystem');
    expect(entry!.actions).toContain('write_filesystem');
  });

  it('Tool entry has use_tool action', () => {
    const entry = PERMISSION_CATALOG.find((e) => e.family === 'Tool');
    expect(entry).toBeDefined();
    expect(entry!.actions).toContain('use_tool');
  });

  it('examplePermit snippets contain permit keyword', () => {
    for (const entry of PERMISSION_CATALOG) {
      expect(entry.examplePermit).toMatch(/\bpermit\b/);
    }
  });

  it('exampleForbid snippets contain forbid keyword', () => {
    for (const entry of PERMISSION_CATALOG) {
      expect(entry.exampleForbid).toMatch(/\bforbid\b/);
    }
  });

  it('all entries satisfy the PermissionCatalogEntry shape', () => {
    for (const entry of PERMISSION_CATALOG) {
      // TypeScript already enforces the shape at compile time; this
      // runtime check guards against future accidental undefined fields.
      const e = entry as PermissionCatalogEntry;
      expect(typeof e.family).toBe('string');
      expect(typeof e.entityType).toBe('string');
      expect(Array.isArray(e.actions)).toBe(true);
      expect(typeof e.examplePermit).toBe('string');
      expect(typeof e.exampleForbid).toBe('string');
    }
  });
});
