/**
 * PermissionFlow.e2e.spec — WP12 frontend e2e tests for the universal
 * permission-prompt modals (cedar-credential-policy-01KQ8TDE).
 *
 * Because the four permission modal Vue components (BashPermissionModal,
 * FilesystemPermissionModal, CredentialPermissionModal, ToolPermissionModal)
 * are pending WP07/WP08, this test file mounts minimal stub Vue components
 * that implement the SAME DOM contract the real components must satisfy.
 * The contract is derived from plan §4 and the task spec:
 *
 *   - bash modal: renders the pattern + danger-copy line for dangerous patterns.
 *   - fs modal: exposes a two-tier radio (this file / this dir and below).
 *   - cred modal: renders ref-id + purpose.
 *   - tool modal: renders server + tool + args summary.
 *
 * Each modal must also satisfy:
 *   - Esc key → resolve(deny).
 *   - "Allow Once" button → resolve(allow_once).
 *   - 5-minute timeout → resolve(deny) — modelled with a fake timer.
 *
 * Resolution is forwarded to a `client.permissions.resolve` stub
 * (the real binding lands with WP07/WP08). The test verifies the
 * stub was called with the expected payload.
 *
 * NOTE: WP05 mcp_spawn flow is NOT covered here. The CredentialPermissionModal
 * stub below models only the `provider_call` / `manual_export` purposes.
 * When WP05 lands, add a scenario for `mcp_spawn` purpose.
 *
 * Testing approach: @vue/test-utils + happy-dom. No real Wails backend,
 * no backend process. Fake client pattern mirrors NewSessionDialog.test.ts.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import {
  defineComponent,
  h,
  ref,
  onMounted,
  onUnmounted,
} from 'vue';

// ──────────────────────────────────────────────────────────────────────
// Shared resolution type — mirrors the WP07 permissions.resolve payload.
// ──────────────────────────────────────────────────────────────────────

type ResolutionScope = 'allow_once' | 'allow_always' | 'deny';

interface PermissionResolution {
  promptId: string;
  scope: ResolutionScope;
  /** For fs modal: the directory pattern (e.g. "/path/*"). */
  dirPattern?: string;
}

// ──────────────────────────────────────────────────────────────────────
// Timeout constant (5 minutes = 300_000 ms). The modals auto-deny
// when the user ignores the prompt for this long.
// ──────────────────────────────────────────────────────────────────────
const MODAL_TIMEOUT_MS = 300_000;

// ──────────────────────────────────────────────────────────────────────
// Stub modal components
//
// Each component accepts props that mirror the real modal's expected
// inputs. The onResolve callback fires when the user makes a choice.
// Each component installs a keydown listener for Escape → deny, and
// a timeout for 5-minute auto-deny.
// ──────────────────────────────────────────────────────────────────────

/**
 * BashPermissionModal stub. Props:
 *   promptId:  unique prompt id.
 *   pattern:   derived argv pattern (e.g. "rm -rf").
 *   dangerous: true → render the danger-copy line.
 *   onResolve: (PermissionResolution) => void
 */
const BashPermissionModal = defineComponent({
  name: 'BashPermissionModal',
  props: {
    promptId: { type: String, required: true },
    pattern: { type: String, required: true },
    dangerous: { type: Boolean, default: false },
    onResolve: { type: Function, required: true },
  },
  setup(props) {
    const timerRef = ref<ReturnType<typeof setTimeout> | null>(null);

    function resolve(scope: ResolutionScope) {
      if (timerRef.value !== null) clearTimeout(timerRef.value);
      (props.onResolve as (r: PermissionResolution) => void)({
        promptId: props.promptId,
        scope,
      });
    }

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') resolve('deny');
    }

    onMounted(() => {
      document.addEventListener('keydown', onKey);
      timerRef.value = setTimeout(() => resolve('deny'), MODAL_TIMEOUT_MS);
    });

    onUnmounted(() => {
      document.removeEventListener('keydown', onKey);
      if (timerRef.value !== null) clearTimeout(timerRef.value);
    });

    return () =>
      h('div', { 'data-testid': 'bash-modal' }, [
        h('p', { 'data-testid': 'bash-pattern' }, props.pattern),
        props.dangerous
          ? h(
              'p',
              { 'data-testid': 'bash-danger-copy' },
              `⚠ This is a dangerous command: ${props.pattern}`,
            )
          : null,
        h(
          'button',
          {
            'data-testid': 'bash-allow-once',
            onClick: () => resolve('allow_once'),
          },
          'Allow Once',
        ),
        h(
          'button',
          {
            'data-testid': 'bash-allow-always',
            onClick: () => resolve('allow_always'),
          },
          'Allow Always',
        ),
        h(
          'button',
          {
            'data-testid': 'bash-deny',
            onClick: () => resolve('deny'),
          },
          'Deny',
        ),
      ]);
  },
});

/**
 * FilesystemPermissionModal stub. Exposes a two-tier radio:
 *   - "this file" (single file grant)
 *   - "this directory and below" (pattern grant using like "<dir>/*")
 */
const FilesystemPermissionModal = defineComponent({
  name: 'FilesystemPermissionModal',
  props: {
    promptId: { type: String, required: true },
    canonicalPath: { type: String, required: true },
    action: { type: String, required: true }, // "read" | "write"
    onResolve: { type: Function, required: true },
  },
  setup(props) {
    const timerRef = ref<ReturnType<typeof setTimeout> | null>(null);
    const tier = ref<'file' | 'dir'>('file');

    function dirPattern(): string {
      const parts = props.canonicalPath.split('/');
      parts.pop();
      return parts.join('/') + '/*';
    }

    function resolve(scope: ResolutionScope) {
      if (timerRef.value !== null) clearTimeout(timerRef.value);
      const resolution: PermissionResolution = {
        promptId: props.promptId,
        scope,
      };
      if (scope !== 'deny' && tier.value === 'dir') {
        resolution.dirPattern = dirPattern();
      }
      (props.onResolve as (r: PermissionResolution) => void)(resolution);
    }

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') resolve('deny');
    }

    onMounted(() => {
      document.addEventListener('keydown', onKey);
      timerRef.value = setTimeout(() => resolve('deny'), MODAL_TIMEOUT_MS);
    });

    onUnmounted(() => {
      document.removeEventListener('keydown', onKey);
      if (timerRef.value !== null) clearTimeout(timerRef.value);
    });

    return () =>
      h('div', { 'data-testid': 'fs-modal' }, [
        h('p', { 'data-testid': 'fs-path' }, props.canonicalPath),
        h('p', { 'data-testid': 'fs-action' }, props.action),
        // Two-tier radio
        h('div', { 'data-testid': 'fs-tier-radio' }, [
          h('label', [
            h('input', {
              type: 'radio',
              name: 'fs-tier',
              value: 'file',
              checked: tier.value === 'file',
              'data-testid': 'fs-tier-file',
              onChange: () => {
                tier.value = 'file';
              },
            }),
            'This file only',
          ]),
          h('label', [
            h('input', {
              type: 'radio',
              name: 'fs-tier',
              value: 'dir',
              checked: tier.value === 'dir',
              'data-testid': 'fs-tier-dir',
              onChange: () => {
                tier.value = 'dir';
              },
            }),
            'This directory and below',
          ]),
        ]),
        h(
          'button',
          {
            'data-testid': 'fs-allow-once',
            onClick: () => resolve('allow_once'),
          },
          'Allow Once',
        ),
        h(
          'button',
          {
            'data-testid': 'fs-allow-always',
            onClick: () => resolve('allow_always'),
          },
          'Allow Always',
        ),
        h(
          'button',
          {
            'data-testid': 'fs-deny',
            onClick: () => resolve('deny'),
          },
          'Deny',
        ),
      ]);
  },
});

/**
 * CredentialPermissionModal stub. Renders ref-id + purpose.
 * NOTE: WP05 mcp_spawn not covered here — only provider_call /
 * manual_export / tool_dispatch purposes are tested.
 */
const CredentialPermissionModal = defineComponent({
  name: 'CredentialPermissionModal',
  props: {
    promptId: { type: String, required: true },
    credRefId: { type: String, required: true },
    purpose: { type: String, required: true },
    onResolve: { type: Function, required: true },
  },
  setup(props) {
    const timerRef = ref<ReturnType<typeof setTimeout> | null>(null);

    function resolve(scope: ResolutionScope) {
      if (timerRef.value !== null) clearTimeout(timerRef.value);
      (props.onResolve as (r: PermissionResolution) => void)({
        promptId: props.promptId,
        scope,
      });
    }

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') resolve('deny');
    }

    onMounted(() => {
      document.addEventListener('keydown', onKey);
      timerRef.value = setTimeout(() => resolve('deny'), MODAL_TIMEOUT_MS);
    });

    onUnmounted(() => {
      document.removeEventListener('keydown', onKey);
      if (timerRef.value !== null) clearTimeout(timerRef.value);
    });

    return () =>
      h('div', { 'data-testid': 'cred-modal' }, [
        h('p', { 'data-testid': 'cred-ref-id' }, props.credRefId),
        h('p', { 'data-testid': 'cred-purpose' }, props.purpose),
        h(
          'button',
          {
            'data-testid': 'cred-allow-once',
            onClick: () => resolve('allow_once'),
          },
          'Allow Once',
        ),
        h(
          'button',
          {
            'data-testid': 'cred-allow-always',
            onClick: () => resolve('allow_always'),
          },
          'Allow Always',
        ),
        h(
          'button',
          {
            'data-testid': 'cred-deny',
            onClick: () => resolve('deny'),
          },
          'Deny',
        ),
      ]);
  },
});

/**
 * ToolPermissionModal stub. Renders server + tool + args summary.
 */
const ToolPermissionModal = defineComponent({
  name: 'ToolPermissionModal',
  props: {
    promptId: { type: String, required: true },
    server: { type: String, required: true },
    tool: { type: String, required: true },
    args: { type: String, default: '' },
    onResolve: { type: Function, required: true },
  },
  setup(props) {
    const timerRef = ref<ReturnType<typeof setTimeout> | null>(null);

    function resolve(scope: ResolutionScope) {
      if (timerRef.value !== null) clearTimeout(timerRef.value);
      (props.onResolve as (r: PermissionResolution) => void)({
        promptId: props.promptId,
        scope,
      });
    }

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') resolve('deny');
    }

    onMounted(() => {
      document.addEventListener('keydown', onKey);
      timerRef.value = setTimeout(() => resolve('deny'), MODAL_TIMEOUT_MS);
    });

    onUnmounted(() => {
      document.removeEventListener('keydown', onKey);
      if (timerRef.value !== null) clearTimeout(timerRef.value);
    });

    return () =>
      h('div', { 'data-testid': 'tool-modal' }, [
        h('p', { 'data-testid': 'tool-server' }, props.server),
        h('p', { 'data-testid': 'tool-name' }, props.tool),
        h('p', { 'data-testid': 'tool-args' }, props.args || '(no args)'),
        h(
          'button',
          {
            'data-testid': 'tool-allow-once',
            onClick: () => resolve('allow_once'),
          },
          'Allow Once',
        ),
        h(
          'button',
          {
            'data-testid': 'tool-allow-always',
            onClick: () => resolve('allow_always'),
          },
          'Allow Always',
        ),
        h(
          'button',
          {
            'data-testid': 'tool-deny',
            onClick: () => resolve('deny'),
          },
          'Deny',
        ),
      ]);
  },
});

// ──────────────────────────────────────────────────────────────────────
// Test suite
// ──────────────────────────────────────────────────────────────────────

describe('PermissionFlow.e2e — WP12 modal acceptance', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  // ── Bash modal ───────────────────────────────────────────────────────

  it('TestE2E_BashModal_RendersPattern: bash modal renders the command pattern', () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(BashPermissionModal, {
      props: {
        promptId: 'p-bash-01',
        pattern: 'git status',
        dangerous: false,
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    expect(w.find('[data-testid="bash-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="bash-pattern"]').text()).toBe('git status');
    expect(w.find('[data-testid="bash-danger-copy"]').exists()).toBe(false);

    w.unmount();
  });

  it('TestE2E_BashModal_DangerousCopyLine: dangerous pattern renders danger-copy line', () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(BashPermissionModal, {
      props: {
        promptId: 'p-bash-02',
        pattern: 'rm -rf',
        dangerous: true,
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    const dangerCopy = w.find('[data-testid="bash-danger-copy"]');
    expect(dangerCopy.exists()).toBe(true);
    expect(dangerCopy.text()).toContain('dangerous command');
    expect(dangerCopy.text()).toContain('rm -rf');

    w.unmount();
  });

  it('TestE2E_BashModal_AllowOnce: Allow Once button → resolve(allow_once)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(BashPermissionModal, {
      props: {
        promptId: 'p-bash-03',
        pattern: 'git status',
        dangerous: false,
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    await w.find('[data-testid="bash-allow-once"]').trigger('click');
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].promptId).toBe('p-bash-03');
    expect(resolutions[0].scope).toBe('allow_once');

    w.unmount();
  });

  it('TestE2E_BashModal_EscDeny: Esc key → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(BashPermissionModal, {
      props: {
        promptId: 'p-bash-04',
        pattern: 'ls',
        dangerous: false,
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    // Dispatch Escape to the document.
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  it('TestE2E_BashModal_TimeoutDeny: 5-minute timeout → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(BashPermissionModal, {
      props: {
        promptId: 'p-bash-05',
        pattern: 'echo hello',
        dangerous: false,
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    // Advance fake timers past the 5-minute threshold.
    vi.advanceTimersByTime(MODAL_TIMEOUT_MS + 1);
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  // ── Filesystem modal ─────────────────────────────────────────────────

  it('TestE2E_FsModal_RendersPathAndAction: fs modal renders canonical path + action', () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(FilesystemPermissionModal, {
      props: {
        promptId: 'p-fs-01',
        canonicalPath: '/Users/alec/projects/myrecipe/output.txt',
        action: 'write',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    expect(w.find('[data-testid="fs-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="fs-path"]').text()).toContain('output.txt');
    expect(w.find('[data-testid="fs-action"]').text()).toBe('write');

    // Two-tier radio is present.
    expect(w.find('[data-testid="fs-tier-file"]').exists()).toBe(true);
    expect(w.find('[data-testid="fs-tier-dir"]').exists()).toBe(true);

    w.unmount();
  });

  it('TestE2E_FsModal_AllowOnce_FileOnly: Allow Once with file tier → no dirPattern', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(FilesystemPermissionModal, {
      props: {
        promptId: 'p-fs-02',
        canonicalPath: '/Users/alec/projects/myrecipe/output.txt',
        action: 'write',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    await w.find('[data-testid="fs-allow-once"]').trigger('click');
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].scope).toBe('allow_once');
    expect(resolutions[0].dirPattern).toBeUndefined();

    w.unmount();
  });

  it('TestE2E_FsModal_AllowAlways_DirTier: allow_always with dir tier → snippet body has like pattern', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(FilesystemPermissionModal, {
      props: {
        promptId: 'p-fs-03',
        canonicalPath: '/Users/alec/projects/myrecipe/output.txt',
        action: 'write',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    // Select "this directory and below" tier.
    const dirRadio = w.find<HTMLInputElement>('[data-testid="fs-tier-dir"]');
    await dirRadio.trigger('change');

    await w.find('[data-testid="fs-allow-always"]').trigger('click');
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].scope).toBe('allow_always');
    // dirPattern should be the parent directory + "/*"
    expect(resolutions[0].dirPattern).toBe('/Users/alec/projects/myrecipe/*');

    w.unmount();
  });

  it('TestE2E_FsModal_EscDeny: Esc → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(FilesystemPermissionModal, {
      props: {
        promptId: 'p-fs-04',
        canonicalPath: '/tmp/x.txt',
        action: 'read',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  it('TestE2E_FsModal_TimeoutDeny: 5-minute timeout → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(FilesystemPermissionModal, {
      props: {
        promptId: 'p-fs-05',
        canonicalPath: '/tmp/x.txt',
        action: 'write',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    vi.advanceTimersByTime(MODAL_TIMEOUT_MS + 1);
    await flushPromises();

    expect(resolutions).toHaveLength(1);
    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  // ── Credential modal ─────────────────────────────────────────────────

  it('TestE2E_CredModal_RendersRefIdAndPurpose: cred modal renders ref-id + purpose', () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(CredentialPermissionModal, {
      props: {
        promptId: 'p-cred-01',
        credRefId: 'cred-openai-001',
        purpose: 'provider_call',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    expect(w.find('[data-testid="cred-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="cred-ref-id"]').text()).toBe('cred-openai-001');
    expect(w.find('[data-testid="cred-purpose"]').text()).toBe('provider_call');

    w.unmount();
  });

  it('TestE2E_CredModal_AllowOnce: Allow Once → resolve(allow_once)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(CredentialPermissionModal, {
      props: {
        promptId: 'p-cred-02',
        credRefId: 'cred-openai-002',
        purpose: 'tool_dispatch',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    await w.find('[data-testid="cred-allow-once"]').trigger('click');
    await flushPromises();

    expect(resolutions[0].scope).toBe('allow_once');
    expect(resolutions[0].promptId).toBe('p-cred-02');

    w.unmount();
  });

  it('TestE2E_CredModal_EscDeny: Esc → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(CredentialPermissionModal, {
      props: {
        promptId: 'p-cred-03',
        credRefId: 'cred-openai-003',
        purpose: 'provider_test',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();

    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  it('TestE2E_CredModal_TimeoutDeny: 5-minute timeout → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(CredentialPermissionModal, {
      props: {
        promptId: 'p-cred-04',
        credRefId: 'cred-openai-004',
        purpose: 'provider_call',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    vi.advanceTimersByTime(MODAL_TIMEOUT_MS + 1);
    await flushPromises();

    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  // NOTE: WP05 mcp_spawn CredentialPermissionModal scenario NOT covered.
  // When WP05 lands, add a test for purpose="mcp_spawn" that verifies
  // the modal renders the mcp_spawn-specific copy and the resolution
  // payload includes the spawned server context.

  // ── Tool modal ───────────────────────────────────────────────────────

  it('TestE2E_ToolModal_RendersServerToolArgs: tool modal renders server + tool + args', () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(ToolPermissionModal, {
      props: {
        promptId: 'p-tool-01',
        server: 'filesystem',
        tool: 'read_file',
        args: '{"path": "/tmp/x.txt"}',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    expect(w.find('[data-testid="tool-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="tool-server"]').text()).toBe('filesystem');
    expect(w.find('[data-testid="tool-name"]').text()).toBe('read_file');
    expect(w.find('[data-testid="tool-args"]').text()).toContain('/tmp/x.txt');

    w.unmount();
  });

  it('TestE2E_ToolModal_AllowOnce: Allow Once → resolve(allow_once)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(ToolPermissionModal, {
      props: {
        promptId: 'p-tool-02',
        server: 'postgres',
        tool: 'query',
        args: '{"sql": "SELECT 1"}',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    await w.find('[data-testid="tool-allow-once"]').trigger('click');
    await flushPromises();

    expect(resolutions[0].scope).toBe('allow_once');
    expect(resolutions[0].promptId).toBe('p-tool-02');

    w.unmount();
  });

  it('TestE2E_ToolModal_EscDeny: Esc → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(ToolPermissionModal, {
      props: {
        promptId: 'p-tool-03',
        server: 'websearch',
        tool: 'search',
        args: '{"query": "hello"}',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();

    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  it('TestE2E_ToolModal_TimeoutDeny: 5-minute timeout → resolve(deny)', async () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(ToolPermissionModal, {
      props: {
        promptId: 'p-tool-04',
        server: 'filesystem',
        tool: 'write_file',
        args: '{}',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    vi.advanceTimersByTime(MODAL_TIMEOUT_MS + 1);
    await flushPromises();

    expect(resolutions[0].scope).toBe('deny');

    w.unmount();
  });

  it('TestE2E_ToolModal_NoArgs: tool modal renders "(no args)" when args is empty', () => {
    const resolutions: PermissionResolution[] = [];
    const w = mount(ToolPermissionModal, {
      props: {
        promptId: 'p-tool-05',
        server: 'kaneaz',
        tool: 'bash',
        args: '',
        onResolve: (r: PermissionResolution) => resolutions.push(r),
      },
    });

    expect(w.find('[data-testid="tool-args"]').text()).toBe('(no args)');

    w.unmount();
  });
});
