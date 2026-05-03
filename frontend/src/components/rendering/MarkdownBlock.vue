<script setup lang="ts">
/**
 * MarkdownBlock — consolidated markdown rendering entry point.
 *
 * Pipeline:
 *   1. Pre-tokenize math ($$...$$, $...$) via custom marked extension.
 *   2. marked.parse(GFM) → raw HTML with placeholder <div>s for code
 *      fences and math blocks.
 *   3. DOMPurify.sanitize via sanitize() helper.
 *   4. Mount into v-html; post-mount walker upgrades placeholders to
 *      Vue component instances (CodeBlock, MathInline, MathBlock,
 *      MermaidDiagram).
 *
 * Props:
 *   text           — raw markdown string.
 *   streaming      — true while tokens are still arriving.
 *   messageId      — message id used by CodeBlock for save-as-artifact.
 *   sessionId      — session id used by CodeBlock for save-as-artifact.
 *   extensions     — MarkdownExtensions dial (defaults to 'all').
 *
 * KaTeX and Mermaid are lazy-imported; they only load when the
 * corresponding content is actually present in the message.
 *
 * Security: every rendered HTML goes through DOMPurify. Code blocks
 * never execute — source bytes are transmitted via data-source-b64
 * attribute then base64-decoded in CodeBlock.vue.
 */
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
  createApp,
  inject,
  nextTick,
  type App,
} from 'vue';
import { marked, type Tokens } from 'marked';
import { sanitize } from '@/lib/markdown/sanitize';
import type { MarkdownExtensions } from '@/lib/types';

// Injected by App.vue (or passed directly via prop for tests).
const MD_EXTENSIONS_KEY = Symbol('mdExtensions');

const props = withDefaults(
  defineProps<{
    text: string;
    streaming?: boolean;
    messageId?: string;
    sessionId?: string;
    extensions?: MarkdownExtensions;
  }>(),
  {
    streaming: false,
    messageId: '',
    sessionId: '',
    extensions: undefined,
  },
);

// Resolved extensions — prop wins; fallback to injection; fallback to 'all'.
const injectedExtensions = inject<MarkdownExtensions>(
  MD_EXTENSIONS_KEY as symbol,
  'all',
);
const effectiveExtensions = computed<MarkdownExtensions>(
  () => props.extensions ?? injectedExtensions,
);

// ─── Math marked extension ──────────────────────────────────────────────────

/** Streaming-safe: only emit a math token when the closing delimiter
 * is present in the input buffer. Otherwise, the raw text passes
 * through and will be promoted on the next reactive update. */

interface MathToken {
  type: 'math-block' | 'math-inline';
  raw: string;
  tex: string;
  display: boolean;
}

const mathExtension = {
  name: 'math-block',
  level: 'block' as const,
  start(src: string) {
    return src.indexOf('$$');
  },
  tokenizer(src: string): MathToken | undefined {
    const match = /^\$\$([\s\S]+?)\$\$/.exec(src);
    if (match) {
      return {
        type: 'math-block',
        raw: match[0],
        tex: match[1].trim(),
        display: true,
      };
    }
    return undefined;
  },
  renderer(token: Tokens.Generic): string {
    const t = token as unknown as MathToken;
    const b64 = btoa(unescape(encodeURIComponent(t.tex)));
    return `<div data-md-block="math-block" data-tex-b64="${b64}"></div>`;
  },
};

const mathInlineExtension = {
  name: 'math-inline',
  level: 'inline' as const,
  start(src: string) {
    return src.indexOf('$');
  },
  tokenizer(src: string): MathToken | undefined {
    // Must have a non-whitespace char immediately after the opening $
    // and non-whitespace char immediately before the closing $.
    const match = /^\$([^\s$](?:[^$]*[^\s$])?)\$/.exec(src);
    if (match) {
      return {
        type: 'math-inline',
        raw: match[0],
        tex: match[1],
        display: false,
      };
    }
    return undefined;
  },
  renderer(token: Tokens.Generic): string {
    const t = token as unknown as MathToken;
    const b64 = btoa(unescape(encodeURIComponent(t.tex)));
    return `<span data-md-block="math-inline" data-tex-b64="${b64}"></span>`;
  },
};

// ─── Configured marked instance ─────────────────────────────────────────────

// marked.use() is additive and returns the same marked instance (singleton).
// We configure a local marked instance by calling use() once at module load.
// Extensions are registered as separate MarkedExtension objects so TypeScript
// is happy with the exact shape.

marked.use({ gfm: true, breaks: false });
marked.use({
  extensions: [mathExtension],
});
marked.use({
  extensions: [mathInlineExtension],
});
marked.use({
  renderer: {
    code(token) {
      // Emit a placeholder div; post-mount walker replaces with CodeBlock.
      const lang = (token.lang || '').trim();
      const b64 = btoa(unescape(encodeURIComponent(token.text)));
      return `<div data-md-block="code" data-lang="${lang}" data-source-b64="${b64}"></div>`;
    },
  },
});

const markedInstance = marked;

// ─── HTML computation ────────────────────────────────────────────────────────

const html = computed(() => {
  if (!props.text) return '';
  // Math extension is always registered on the marked singleton (module-level
  // side-effect). When extensions disable math, the upgradeMathBlocks walker
  // will replace placeholders with escaped raw text instead of KaTeX output.
  // This avoids the complexity of configuring per-render marked instances.
  const raw = markedInstance.parse(props.text, { async: false }) as string;
  return sanitize(raw);
});

// ─── Post-mount placeholder walker ──────────────────────────────────────────

const containerRef = ref<HTMLElement | null>(null);
// Track mounted child apps for cleanup.
const mountedApps: App[] = [];

function unmountAll() {
  for (const app of mountedApps) {
    try {
      app.unmount();
    } catch {
      // already unmounted
    }
  }
  mountedApps.length = 0;
}

async function upgradeCodeBlocks(root: HTMLElement) {
  const nodes = root.querySelectorAll<HTMLElement>('[data-md-block="code"]');
  if (!nodes.length) return;

  for (const node of nodes) {
    const lang = node.getAttribute('data-lang') ?? '';
    const b64 = node.getAttribute('data-source-b64') ?? '';
    let source = '';
    try {
      source = decodeURIComponent(escape(atob(b64)));
    } catch {
      source = b64;
    }

    // Skip mermaid when extensions gate disables diagrams.
    const ext = effectiveExtensions.value;
    const isMermaid = lang === 'mermaid';
    if (isMermaid && (ext === 'basic' || ext === 'math')) {
      // Render as plain code fence.
      node.outerHTML = `<pre><code class="language-${lang}">${escapeHtml(source)}</code></pre>`;
      continue;
    }

    const host = document.createElement('div');
    node.replaceWith(host);

    let componentDef: Parameters<typeof createApp>[0];
    let componentProps: Record<string, unknown>;

    if (isMermaid) {
      const { default: MermaidDiagram } = await import(
        '@/components/rendering/MermaidDiagram.vue'
      );
      componentDef = MermaidDiagram;
      componentProps = { source, streaming: props.streaming };
    } else {
      const { default: CodeBlock } = await import(
        '@/components/rendering/CodeBlock.vue'
      );
      componentDef = CodeBlock;
      componentProps = {
        lang,
        source,
        streaming: props.streaming,
        messageId: props.messageId,
        sessionId: props.sessionId,
      };
    }

    const app = createApp(componentDef, componentProps);
    app.mount(host);
    mountedApps.push(app);
  }
}

async function upgradeMathBlocks(root: HTMLElement) {
  const ext = effectiveExtensions.value;
  if (ext === 'basic' || ext === 'diagrams') {
    // Replace placeholders with escaped raw text.
    const blockNodes = root.querySelectorAll<HTMLElement>(
      '[data-md-block="math-block"]',
    );
    for (const node of blockNodes) {
      try {
        const b64 = node.getAttribute('data-tex-b64') ?? '';
        const tex = decodeURIComponent(escape(atob(b64)));
        node.outerHTML = `<p>$$${escapeHtml(tex)}$$</p>`;
      } catch {
        node.outerHTML = '';
      }
    }
    const inlineNodes = root.querySelectorAll<HTMLElement>(
      '[data-md-block="math-inline"]',
    );
    for (const node of inlineNodes) {
      try {
        const b64 = node.getAttribute('data-tex-b64') ?? '';
        const tex = decodeURIComponent(escape(atob(b64)));
        node.outerHTML = `$${escapeHtml(tex)}$`;
      } catch {
        node.outerHTML = '';
      }
    }
    return;
  }

  const blockNodes = root.querySelectorAll<HTMLElement>(
    '[data-md-block="math-block"]',
  );
  const inlineNodes = root.querySelectorAll<HTMLElement>(
    '[data-md-block="math-inline"]',
  );

  if (!blockNodes.length && !inlineNodes.length) return;

  // Lazy-load KaTeX only when math content is present.
  const [katexModule] = await Promise.all([
    import('katex'),
    import('katex/dist/katex.min.css'),
  ]);
  const katex = katexModule.default;

  for (const node of blockNodes) {
    try {
      const b64 = node.getAttribute('data-tex-b64') ?? '';
      const tex = decodeURIComponent(escape(atob(b64)));
      const rendered = katex.renderToString(tex, {
        throwOnError: false,
        displayMode: true,
        output: 'html',
        trust: false,
      });
      const clean = sanitize(rendered, 'katex');
      const wrapper = document.createElement('div');
      wrapper.className = 'md-math-block';
      wrapper.innerHTML = clean;
      node.replaceWith(wrapper);
    } catch {
      node.outerHTML = '';
    }
  }

  for (const node of inlineNodes) {
    try {
      const b64 = node.getAttribute('data-tex-b64') ?? '';
      const tex = decodeURIComponent(escape(atob(b64)));
      const rendered = katex.renderToString(tex, {
        throwOnError: false,
        displayMode: false,
        output: 'html',
        trust: false,
      });
      const clean = sanitize(rendered, 'katex');
      const span = document.createElement('span');
      span.className = 'md-math-inline';
      span.innerHTML = clean;
      node.replaceWith(span);
    } catch {
      node.outerHTML = '';
    }
  }
}

function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;'); // css-tokens-allow: HTML numeric entity, not a hex color literal
}

/** Wrap bare <table> elements in a horizontal-scroll container. */
function upgradeTables(root: HTMLElement) {
  // Avoid double-wrapping tables that already sit inside md-table-wrap.
  const tables = root.querySelectorAll<HTMLTableElement>('table');
  for (const table of tables) {
    if (
      table.parentElement &&
      table.parentElement.classList.contains('md-table-wrap')
    ) {
      continue;
    }
    const wrapper = document.createElement('div');
    wrapper.className = 'md-table-wrap';
    table.parentNode?.insertBefore(wrapper, table);
    wrapper.appendChild(table);
  }
}

async function runUpgrades() {
  const root = containerRef.value;
  if (!root) return;
  unmountAll();
  upgradeTables(root);
  await upgradeMathBlocks(root);
  await upgradeCodeBlocks(root);
}

// Run upgrades whenever html changes (which fires on every text update).
// flush: 'post' ensures the v-html DOM update has committed before we walk.
watch(html, async () => {
  await nextTick();
  await runUpgrades();
}, { flush: 'post' });

onMounted(async () => {
  await runUpgrades();
});

onBeforeUnmount(() => {
  unmountAll();
});
</script>

<template>
  <div ref="containerRef" class="md-body" v-html="html"></div>
</template>

<style scoped>
.md-body :deep(p) {
  margin: 0 0 0.65em 0;
  line-height: 1.55;
}
.md-body :deep(p:last-child) {
  margin-bottom: 0;
}
.md-body :deep(ul),
.md-body :deep(ol) {
  margin: 0 0 0.65em 1.25em;
  padding: 0;
  line-height: 1.55;
}
.md-body :deep(li) {
  margin-bottom: 0.15em;
}
.md-body :deep(ul) {
  list-style: disc;
}
.md-body :deep(ol) {
  list-style: decimal;
}
.md-body :deep(h1),
.md-body :deep(h2),
.md-body :deep(h3),
.md-body :deep(h4) {
  font-weight: 600;
  margin: 0.6em 0 0.3em 0;
  line-height: 1.25;
}
.md-body :deep(h1) {
  font-size: 1.2em;
}
.md-body :deep(h2) {
  font-size: 1.1em;
}
.md-body :deep(h3),
.md-body :deep(h4) {
  font-size: 1em;
  color: var(--ink-muted);
}
.md-body :deep(strong) {
  color: var(--ink);
  font-weight: 600;
}
.md-body :deep(em) {
  font-style: italic;
}
.md-body :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.9em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  padding: 0.05em 0.35em;
  color: var(--ink);
}
.md-body :deep(pre) {
  font-family: var(--font-mono);
  font-size: 0.88em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  padding: 0.65em 0.85em;
  margin: 0.5em 0 0.75em 0;
  overflow-x: auto;
  line-height: 1.45;
}
.md-body :deep(pre code) {
  background: transparent;
  border: 0;
  padding: 0;
  color: var(--ink);
}
.md-body :deep(blockquote) {
  border-left: 2px solid var(--border-strong);
  padding-left: 0.75em;
  margin: 0.5em 0;
  color: var(--ink-muted);
}
.md-body :deep(a) {
  color: var(--accent);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.md-body :deep(a:hover) {
  color: var(--ink);
}
.md-body :deep(hr) {
  border: 0;
  border-top: 1px solid var(--border-muted);
  margin: 0.85em 0;
}
.md-body :deep(table) {
  border-collapse: collapse;
  min-width: 100%;
  font-size: 0.95em;
}
.md-body :deep(.md-table-wrap) {
  overflow-x: auto;
  margin: 0.5em 0;
}
.md-body :deep(th),
.md-body :deep(td) {
  border: 1px solid var(--border-muted);
  padding: 0.3em 0.6em;
  text-align: left;
  white-space: nowrap;
}
.md-body :deep(th) {
  background: var(--surface-2);
  font-weight: 600;
}
.md-body :deep(tbody tr:nth-child(even)) {
  background: var(--surface-2);
}

/* Math rendering */
.md-body :deep(.md-math-block) {
  margin: 0.75em 0;
  overflow-x: auto;
  text-align: center;
}
.md-body :deep(.md-math-inline) {
  display: inline;
}

/* KaTeX overrides to use design tokens */
.md-body :deep(.katex) {
  font-size: 1.05em;
}
</style>
