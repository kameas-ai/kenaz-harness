import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import PreviewFrame from '../PreviewFrame.vue';

/**
 * PreviewFrame unit tests — renderer dispatch coverage.
 *
 * Each test mounts PreviewFrame with a specific kind and verifies
 * that the correct renderer slot is activated (data-testid presence).
 * This is the minimal coverage required by the spec (WP03 acceptance:
 * "Vitest coverage for the renderer dispatch").
 */

describe('PreviewFrame — renderer dispatch', () => {
  it('renders markdown renderer for kind="markdown"', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'markdown', content: '# Hello' },
    });
    expect(w.find('[data-testid="preview-markdown"]').exists()).toBe(true);
    expect(w.find('[data-testid="preview-plain"]').exists()).toBe(false);
  });

  it('renders code renderer for kind="code"', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'code', content: 'const x = 1;', language: 'typescript' },
    });
    expect(w.find('[data-testid="preview-code"]').exists()).toBe(true);
  });

  it('renders image renderer for kind="image"', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'image', content: 'data:image/png;base64,abc' },
    });
    expect(w.find('[data-testid="preview-image"]').exists()).toBe(true);
  });

  it('renders diff renderer for kind="diff"', () => {
    const diff = '+added line\n-removed line\n context';
    const w = mount(PreviewFrame, {
      props: { kind: 'diff', content: diff },
    });
    expect(w.find('[data-testid="preview-diff"]').exists()).toBe(true);
  });

  it('renders table renderer for kind="table" with valid JSON', () => {
    const table = JSON.stringify([
      ['Name', 'Age'],
      ['Alice', '30'],
      ['Bob', '25'],
    ]);
    const w = mount(PreviewFrame, {
      props: { kind: 'table', content: table },
    });
    expect(w.find('[data-testid="preview-table"]').exists()).toBe(true);
    // Header cells should be rendered
    expect(w.text()).toContain('Name');
    expect(w.text()).toContain('Age');
  });

  it('renders table invalid-data notice for kind="table" with bad JSON', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'table', content: 'not-json' },
    });
    expect(w.find('[data-testid="preview-table"]').exists()).toBe(true);
    expect(w.text()).toContain('Invalid table data');
  });

  it('renders html renderer for kind="html"', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'html', content: '<p>Hello <b>world</b></p>' },
    });
    expect(w.find('[data-testid="preview-html"]').exists()).toBe(true);
  });

  it('renders vue-component placeholder for kind="vue-component"', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'vue-component', content: '' },
    });
    expect(w.find('[data-testid="preview-vue-component"]').exists()).toBe(true);
    expect(w.text()).toContain('future release');
  });

  it('renders plain renderer as fallback for kind="plain"', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'plain', content: 'hello world' },
    });
    expect(w.find('[data-testid="preview-plain"]').exists()).toBe(true);
    expect(w.text()).toContain('hello world');
  });

  it('renders plain renderer as fallback for unknown kind', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'unknown-kind', content: 'fallback text' },
    });
    expect(w.find('[data-testid="preview-plain"]').exists()).toBe(true);
    expect(w.text()).toContain('fallback text');
  });

  it('shows language tag in header when provided', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'code', content: 'fn main() {}', language: 'rust' },
    });
    expect(w.text()).toContain('rust');
  });

  it('emits correct data-preview-kind attribute', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'markdown', content: '# Test' },
    });
    expect(w.find('[data-testid="preview-frame"]').attributes('data-preview-kind')).toBe('markdown');
  });

  it('blocks remote image URLs', () => {
    const w = mount(PreviewFrame, {
      props: { kind: 'image', content: 'https://example.com/img.png' },
    });
    const img = w.find('img');
    // Remote URL is stripped; the image element should not appear.
    expect(img.exists()).toBe(false);
    expect(w.text()).toContain('unavailable');
  });
});
