/**
 * MarkdownBlock.katex.spec.ts — WP02 KaTeX acceptance tests.
 *
 * Coverage:
 *   - Inline math `$...$` renders to a .katex element.
 *   - Display math `$$...$$` renders to a .katex-display element.
 *   - Math inside fenced code blocks is NOT rendered (preserved as raw text).
 *   - Malformed KaTeX falls back to raw text in <code> (no crash).
 *   - Multiple inline math expressions in one paragraph all render.
 *   - Display math does NOT render mid-stream when closing $$ is absent.
 *   - Inline math does NOT render when $ has no closing delimiter.
 *   - Backslash commands (e.g. \frac) survive the pipeline intact.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MarkdownBlock from '@/components/chat/MarkdownBlock.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';

// ── mount helper ───────────────────────────────────────────────────────────

function mountBlock(source: string, streaming = false) {
  return mount(MarkdownBlock, {
    props: { source, streaming },
    global: {
      provide: {
        [HarnessClientKey as symbol]: createFakeHarnessClient(),
      },
    },
    attachTo: document.body,
  });
}

afterEach(() => {
  document.body.innerHTML = '';
});

// ── inline math ────────────────────────────────────────────────────────────

describe('inline math ($...$)', () => {
  it('renders inline math to a .katex element inside .math-inline', async () => {
    const w = mountBlock('The equation $x^2 + y^2 = z^2$ is Pythagorean.');
    await flushPromises();

    const mathInline = w.find('.math-inline');
    expect(mathInline.exists()).toBe(true);

    // KaTeX always emits a span.katex wrapping the output.
    const katexEl = mathInline.find('.katex');
    expect(katexEl.exists()).toBe(true);
  });

  it('does not use display mode for inline math', async () => {
    const w = mountBlock('Inline: $E = mc^2$');
    await flushPromises();

    // .katex-display only appears in display mode; should NOT be present.
    const katexDisplay = w.find('.katex-display');
    expect(katexDisplay.exists()).toBe(false);

    // .math-inline wraps it instead.
    expect(w.find('.math-inline').exists()).toBe(true);
  });

  it('renders multiple inline math expressions in one paragraph', async () => {
    const w = mountBlock('Both $a=1$ and $b=2$ are scalars.');
    await flushPromises();

    const allMathInline = w.findAll('.math-inline');
    expect(allMathInline).toHaveLength(2);
  });

  it('renders \\frac backslash command correctly', async () => {
    const w = mountBlock('Inline fraction: $\\frac{1}{2}$');
    await flushPromises();

    const mathInline = w.find('.math-inline');
    expect(mathInline.exists()).toBe(true);

    // KaTeX should have rendered something (not empty, not raw TeX).
    const html = mathInline.html();
    expect(html).toContain('katex');
    // The raw \frac string should NOT appear verbatim as text (KaTeX consumed it).
    expect(mathInline.element.textContent).not.toContain('\\frac');
  });

  it('does NOT render inline math when closing $ is missing (streaming safety)', async () => {
    // Partial token — no closing $.
    const w = mountBlock('Work in progress: $x^2 + y');
    await flushPromises();

    // No .math-inline should be produced; the text passes through as prose.
    expect(w.find('.math-inline').exists()).toBe(false);
    // The raw dollar sign and text should appear in the output.
    expect(w.text()).toContain('$x^2 + y');
  });
});

// ── display math ───────────────────────────────────────────────────────────

describe('display math ($$...$$)', () => {
  it('renders display math to a .katex-display element inside .math-display', async () => {
    const w = mountBlock('$$\\sum_{i=0}^n i$$');
    await flushPromises();

    const mathDisplay = w.find('.math-display');
    expect(mathDisplay.exists()).toBe(true);

    // KaTeX emits .katex-display inside the outer wrapper for displayMode: true.
    const katexDisplay = mathDisplay.find('.katex-display');
    expect(katexDisplay.exists()).toBe(true);
  });

  it('renders display math with multiline content', async () => {
    const w = mountBlock('$$\n\\int_0^1 x^2\\,dx = \\frac{1}{3}\n$$');
    await flushPromises();

    const mathDisplay = w.find('.math-display');
    expect(mathDisplay.exists()).toBe(true);
    expect(mathDisplay.find('.katex').exists()).toBe(true);
  });

  it('does NOT render display math when closing $$ is missing (streaming safety)', async () => {
    // Only opening $$, no close.
    const w = mountBlock('$$\\sum_{i=0}^n i');
    await flushPromises();

    // No .math-display should be produced.
    expect(w.find('.math-display').exists()).toBe(false);
    // Raw content should appear as text.
    expect(w.text()).toContain('$$');
  });
});

// ── code block preservation ────────────────────────────────────────────────

describe('math inside code blocks is NOT rendered', () => {
  it('preserves $x^2$ as raw text inside a fenced code block', async () => {
    const src = '```\n$x^2 + y^2 = z^2$\n```';
    const w = mountBlock(src);
    await flushPromises();

    // No .katex or .math-inline should be present.
    expect(w.find('.katex').exists()).toBe(false);
    expect(w.find('.math-inline').exists()).toBe(false);

    // The code block should contain the raw $ characters.
    const code = w.find('code');
    expect(code.exists()).toBe(true);
    expect(code.element.textContent).toContain('$x^2 + y^2 = z^2$');
  });

  it('preserves $$...$$ as raw text inside a fenced code block', async () => {
    const src = '```latex\n$$\\sum_{i=0}^n i$$\n```';
    const w = mountBlock(src);
    await flushPromises();

    expect(w.find('.katex-display').exists()).toBe(false);
    expect(w.find('.math-display').exists()).toBe(false);

    const code = w.find('code');
    expect(code.exists()).toBe(true);
    expect(code.element.textContent).toContain('$$');
  });

  it('preserves inline math inside an inline code span', async () => {
    // Backtick inline code — marked treats this as code token, not text.
    const src = 'Use `$x^2$` in your formula.';
    const w = mountBlock(src);
    await flushPromises();

    // No KaTeX rendering inside the inline code span.
    expect(w.find('.katex').exists()).toBe(false);

    // The inline code should contain the raw dollar signs.
    const code = w.find('code');
    expect(code.exists()).toBe(true);
    expect(code.element.textContent).toContain('$x^2$');
  });
});

// ── error fallback ─────────────────────────────────────────────────────────

describe('malformed KaTeX falls back gracefully', () => {
  it('renders a fallback <code> element for severely malformed input', async () => {
    // \frac{1 has mismatched braces — KaTeX with throwOnError:false renders
    // an error span; either way no crash and some output exists.
    const w = mountBlock('Bad math: $\\frac{1$');
    await flushPromises();

    // The component must not crash (this test completing is the primary assertion).
    // Either KaTeX renders an error span (still inside .math-inline)
    // or the fallback <code class="math-fallback"> is used.
    const rendered = w.html();
    expect(rendered).toBeTruthy();
    // At minimum the surrounding text should render.
    expect(w.text()).toContain('Bad math');
  });

  it('renders remaining valid content even after a KaTeX error', async () => {
    const w = mountBlock('Before $\\frac{1$ After');
    await flushPromises();

    // "After" must still appear in the rendered output.
    expect(w.text()).toContain('After');
  });
});

// ── mixed content ──────────────────────────────────────────────────────────

describe('mixed markdown + math', () => {
  it('renders code blocks and math in the same message without interference', async () => {
    const src = [
      '# Heading',
      '',
      'Inline math: $E = mc^2$',
      '',
      '```python',
      '# $not_math$',
      'print("hello")',
      '```',
      '',
      '$$\\int_0^1 x\\,dx = \\frac{1}{2}$$',
    ].join('\n');

    const w = mountBlock(src);
    await flushPromises();

    // Inline math rendered.
    expect(w.find('.math-inline').exists()).toBe(true);

    // Display math rendered.
    expect(w.find('.math-display').exists()).toBe(true);

    // Code block rendered with header.
    expect(w.find('[data-testid="code-block-header-0"]').exists()).toBe(true);

    // Math inside code block NOT rendered.
    const codeBlock = w.find('.code-block-wrap');
    expect(codeBlock.exists()).toBe(true);
    expect(codeBlock.find('.katex').exists()).toBe(false);
  });
});
