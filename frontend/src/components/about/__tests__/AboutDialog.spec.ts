/**
 * AboutDialog.spec.ts — tests for the About Kenaz Harness dialog.
 *
 * Verifies:
 *   1. Visible when open=true; hidden when open=false
 *   2. Displays version, truncated commit, and buildTime from appInfo()
 *   3. Falls back to "dev" version when appInfo() rejects
 *   4. Emits update:open=false when closed
 *   5. Docs link points to docs.kameas.ai
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AboutDialog from '../AboutDialog.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function mountAboutDialog(
  client: ReturnType<typeof createFakeHarnessClient>,
  props: { open: boolean },
) {
  return mount(AboutDialog, {
    props,
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        // Stub BaseDialog to a minimal wrapper that honours :open and emits close.
        BaseDialog: {
          template: `
            <div v-if="open" data-testid="base-dialog">
              <div data-testid="dialog-title">{{ title }}</div>
              <slot />
              <button data-testid="dialog-close-btn" @click="$emit('close')" />
            </div>
          `,
          props: ['open', 'title', 'panelClass'],
          emits: ['close'],
        },
      },
    },
  });
}

describe('AboutDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders when open=true', async () => {
    const client = createFakeHarnessClient();
    const wrapper = mountAboutDialog(client, { open: true });
    await flushPromises();
    expect(wrapper.find('[data-testid="base-dialog"]').exists()).toBe(true);
  });

  it('does not render when open=false', async () => {
    const client = createFakeHarnessClient();
    const wrapper = mountAboutDialog(client, { open: false });
    await flushPromises();
    expect(wrapper.find('[data-testid="base-dialog"]').exists()).toBe(false);
  });

  it('displays version, truncated commit (8 chars), and buildTime from appInfo()', async () => {
    const client = createFakeHarnessClient({
      appInfo: vi.fn(async () => ({
        build: '1.2.3',
        commit: 'abcdef1234567890',
        buildTime: '2026-07-04T00:00:00Z',
        goVersion: '',
        platform: 'darwin',
        windowSize: { width: 1280, height: 800 },
      })),
    });
    const wrapper = mountAboutDialog(client, { open: true });
    await flushPromises();

    // Version
    const versionEl = wrapper.find('[data-testid="about-version"]');
    expect(versionEl.exists()).toBe(true);
    expect(versionEl.text()).toContain('1.2.3');

    // Commit truncated to 8 chars
    const commitEl = wrapper.find('[data-testid="about-commit"]');
    expect(commitEl.exists()).toBe(true);
    expect(commitEl.text()).toBe('abcdef12');

    // Build time
    const buildTimeEl = wrapper.find('[data-testid="about-buildtime"]');
    expect(buildTimeEl.exists()).toBe(true);
    expect(buildTimeEl.text()).toContain('2026-07-04');
  });

  it('shows "dev" version fallback when appInfo() rejects', async () => {
    const client = createFakeHarnessClient({
      appInfo: vi.fn(async () => { throw new Error('unavailable'); }),
    });
    const wrapper = mountAboutDialog(client, { open: true });
    await flushPromises();

    const versionEl = wrapper.find('[data-testid="about-version"]');
    expect(versionEl.text()).toContain('dev');
  });

  it('emits update:open=false when the dialog is closed', async () => {
    const client = createFakeHarnessClient();
    const wrapper = mountAboutDialog(client, { open: true });
    await flushPromises();

    await wrapper.find('[data-testid="dialog-close-btn"]').trigger('click');
    await flushPromises();

    const emitted = wrapper.emitted('update:open');
    expect(emitted).toBeTruthy();
    expect(emitted![emitted!.length - 1]).toEqual([false]);
  });

  it('docs link points to docs.kameas.ai', async () => {
    const client = createFakeHarnessClient();
    const wrapper = mountAboutDialog(client, { open: true });
    await flushPromises();

    const link = wrapper.find('[data-testid="about-docs-link"]');
    expect(link.exists()).toBe(true);
    expect(link.attributes('href')).toBe('https://docs.kameas.ai');
  });

  it('shows "About Kenaz Harness" as dialog title', async () => {
    const client = createFakeHarnessClient();
    const wrapper = mountAboutDialog(client, { open: true });
    await flushPromises();

    const titleEl = wrapper.find('[data-testid="dialog-title"]');
    expect(titleEl.text()).toBe('About Kenaz Harness');
  });
});
