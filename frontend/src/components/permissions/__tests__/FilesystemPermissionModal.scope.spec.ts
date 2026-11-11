/**
 * FilesystemPermissionModal — the "If allowed always, scope to" radio
 * reaches the backend (fleet e2e 2026-11-07, E2E-1).
 *
 * Before: the radio was rendered but never sent; every Allow-always was
 * an exact-path grant, so three files written into one new directory
 * produced three prompts and reading them back prompted again.
 *
 * Mutation: drop `:allow-always-scope="wireScope"` from the template →
 * the directory tests fail (bare 'allow_always' is sent).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FilesystemPermissionModal from '@/components/permissions/FilesystemPermissionModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';

function pendingFS(over: Partial<PermissionRequest> = {}): PermissionRequest {
  return {
    request_id: 'r-fs-1',
    session_id: 's-1',
    family: 'fs',
    resource_display: 'write /tmp/site/index.html',
    op: 'write',
    dangerous_tier: false,
    ...over,
  } as PermissionRequest;
}

async function mountWith(rows: PermissionRequest[]) {
  const client = createFakeHarnessClient();
  const list = vi.spyOn(client.permissions, 'listPending').mockResolvedValue(rows);
  const resolve = vi.spyOn(client.permissions, 'resolve').mockResolvedValue(undefined);
  mount(FilesystemPermissionModal, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  await flushPromises();
  await flushPromises();
  return { client, list, resolve };
}

function q<T extends Element>(id: string): T | null {
  return document.querySelector<T>(`[data-testid="${id}"]`);
}

async function clickAllowAlways() {
  q<HTMLButtonElement>('perm-modal-allow-always')!.click();
  await flushPromises();
  await flushPromises();
}

describe('FilesystemPermissionModal — grant scope', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('exact scope (the default for an existing directory) sends the bare decision', async () => {
    const { resolve } = await mountWith([pendingFS()]);
    expect(q<HTMLInputElement>('fs-scope-exact')!.checked).toBe(true);
    await clickAllowAlways();
    expect(resolve).toHaveBeenCalledWith('r-fs-1', 'allow_always');
  });

  it('a write that creates its directory defaults to directory scope, with reads', async () => {
    const { resolve } = await mountWith([
      pendingFS({ surface: { fs: { parent_missing: true } } }),
    ]);
    expect(q<HTMLInputElement>('fs-scope-directory')!.checked).toBe(true);
    expect(q<HTMLInputElement>('fs-scope-also-read')!.checked).toBe(true);
    await clickAllowAlways();
    expect(resolve).toHaveBeenCalledWith('r-fs-1', 'allow_always:directory_rw');
  });

  it('unticking "also allow reading" sends the write-only directory scope', async () => {
    const { resolve } = await mountWith([
      pendingFS({ surface: { fs: { parent_missing: true } } }),
    ]);
    const box = q<HTMLInputElement>('fs-scope-also-read')!;
    box.click();
    await flushPromises();
    await clickAllowAlways();
    expect(resolve).toHaveBeenCalledWith('r-fs-1', 'allow_always:directory');
  });

  it('a read prompt never offers "also allow reading" and sends plain directory scope', async () => {
    const { resolve } = await mountWith([
      pendingFS({ op: 'read', resource_display: 'read /tmp/site/a.js' }),
    ]);
    q<HTMLInputElement>('fs-scope-directory')!.click();
    await flushPromises();
    expect(q('fs-scope-also-read')).toBeNull();
    await clickAllowAlways();
    expect(resolve).toHaveBeenCalledWith('r-fs-1', 'allow_always:directory');
  });

  it('reconciles after an Allow-always so rows the grant released disappear', async () => {
    const second = pendingFS({ request_id: 'r-fs-2', resource_display: 'write /tmp/site/app.js' });
    const { list } = await mountWith([pendingFS(), second]);
    // Server-side the directory grant released r-fs-2 inside Resolve.
    list.mockResolvedValue([]);
    q<HTMLInputElement>('fs-scope-directory')!.click();
    await flushPromises();
    await clickAllowAlways();
    expect(q('base-permission-modal')).toBeNull();
  });

  it('renders the privileged banner for harness state', async () => {
    await mountWith([
      pendingFS({ surface: { fs: { privileged: "This is the harness's own state directory" } } }),
    ]);
    expect(q('fs-privileged-notice')!.textContent).toContain("harness's own state directory");
  });
});
