/**
 * IframeSandbox.test.ts — render tests for IframeSandbox.vue
 * (artifact-preview-binary-rendering-01KQ8TD5 WP04 acceptance).
 */

import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import IframeSandbox from '../IframeSandbox.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

const withFakeClient = {
  global: {
    plugins: [
      {
        install(app: import('vue').App) {
          provideFakeClient(app);
        },
      },
    ],
  },
};

describe('IframeSandbox', () => {
  it('renders an iframe with sandbox="allow-same-origin"', () => {
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html: '<p>test</p>' },
    });

    const iframe = w.find('iframe');
    expect(iframe.exists()).toBe(true);
    expect(iframe.attributes('sandbox')).toBe('allow-same-origin');
  });

  it('iframe srcdoc contains the CSP meta tag', () => {
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html: '<p>test</p>' },
    });

    const iframe = w.find('iframe');
    const srcdoc = iframe.attributes('srcdoc') ?? '';
    expect(srcdoc).toContain('Content-Security-Policy');
    expect(srcdoc).toContain("default-src 'none'");
    expect(srcdoc).toContain("base-uri 'none'");
    expect(srcdoc).toContain("form-action 'none'");
  });

  it('iframe srcdoc contains the artifact html in body', () => {
    const html = '<p>artifact content</p>';
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html },
    });

    const srcdoc = w.find('iframe').attributes('srcdoc') ?? '';
    expect(srcdoc).toContain(`<body>${html}</body>`);
  });

  it('sandbox attribute does NOT include allow-scripts', () => {
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html: '<script>alert(1)</script>' },
    });

    const sandbox = w.find('iframe').attributes('sandbox') ?? '';
    expect(sandbox).not.toContain('allow-scripts');
  });

  it('shows Open in browser button when openExternallyPath is provided', () => {
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html: '<p>test</p>', openExternallyPath: '/tmp/artifact.html' },
    });

    const btn = w.find('[data-testid="iframe-sandbox-open-external"]');
    expect(btn.exists()).toBe(true);
    expect(btn.text()).toContain('Open in browser');
  });

  it('does not show Open in browser button when openExternallyPath is absent', () => {
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html: '<p>test</p>' },
    });

    const btn = w.find('[data-testid="iframe-sandbox-open-external"]');
    expect(btn.exists()).toBe(false);
  });

  it('does not use raw color literals (privacy CI invariant #4)', () => {
    const w = mount(IframeSandbox, {
      ...withFakeClient,
      props: { html: '<p>test</p>' },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
