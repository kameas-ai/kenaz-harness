// Vitest global setup. Anything common across the test suite goes here.

// ── happy-dom: spec-compliant Node.prototype.nodeName ────────────────────────
// Per WebIDL, `nodeName` is a single accessor on Node.prototype that returns
// the real name for every node type, and that is what browsers (WebKit, Blink,
// Gecko) and jsdom ship. happy-dom instead defines a base getter on
// Node.prototype that returns "" and overrides it on each subclass.
//
// DOMPurify >= 3.4.8 reads names through the getter captured from
// Node.prototype, so a clobbered `nodeName` own property cannot lie to it
// (GHSA-x4vx-rjvf-j5p4 hardening). Under happy-dom every element then looks
// nameless and sanitize() returns garbage, e.g. `<script>` survives. The
// product is unaffected because real webviews are spec-compliant. Verified:
// DOMPurify 3.4.17 under jsdom strips <script> and keeps allowlisted markup.
//
// This shim fixes only the base getter that the test DOM gets wrong. The
// real DOMPurify still runs, and the sanitize tests still exercise it.
// It must run before anything imports dompurify, because DOMPurify captures
// the getter when its factory runs at module load. Setup files run before
// test modules are imported. Remove it once happy-dom's base getter is
// spec-compliant: the probe below then skips the patch on its own.
{
  const nodeProto = window.Node.prototype;
  const baseGetter = Object.getOwnPropertyDescriptor(nodeProto, 'nodeName')?.get;
  if (baseGetter && baseGetter.call(document.createElement('p')) === '') {
    Object.defineProperty(nodeProto, 'nodeName', {
      configurable: true,
      enumerable: true,
      get(this: Node): string {
        // Defer to the most-derived own `nodeName` getter (happy-dom's
        // per-subclass implementation); this skips any instance-level
        // clobbering, matching browser semantics for the prototype getter.
        let proto = Object.getPrototypeOf(this) as object | null;
        while (proto && proto !== nodeProto) {
          const get = Object.getOwnPropertyDescriptor(proto, 'nodeName')?.get;
          if (get) return get.call(this) as string;
          proto = Object.getPrototypeOf(proto) as object | null;
        }
        return baseGetter.call(this) as string;
      },
    });
  }
}

// ── Wails bridge stub ────────────────────────────────────────────────────────
// useServedMode() detects served mode by checking for the absence of
// window.go.rpc.Bindings (the Wails-injected binding surface).  In tests the
// jsdom/happy-dom environment doesn't have Wails, so without this stub every
// test would see "served mode = true" and views would render the
// NotAvailableInServedMode panel instead of their real content.
//
// Inject a minimal stub so the detection returns false (desktop mode) in tests.
// Tests that specifically want to verify served-mode behaviour should mock
// useServedMode (via vi.mock) or use dispatchServedEvent directly.
(window as unknown as { go: unknown }).go = {
  rpc: { Bindings: Object.create(null) as object },
};

// ── axe-core / vitest-axe integration ────────────────────────────────────────
// Extend vitest `expect` with `toHaveNoViolations` so any test can assert:
//
//   const results = await axe(wrapper.element);
//   expect(results).toHaveNoViolations();
//
// The matcher is imported here so it is registered once, globally, before any
// test file runs. Tests import `axe` directly from 'vitest-axe'.
//
// Note: happy-dom does not implement CSS layout, so the `color-contrast` rule
// always reports false positives (computed contrast is 0/0). All a11y test
// files disable this rule explicitly with:
//   axe(el, { rules: { 'color-contrast': { enabled: false } } })
import { expect } from 'vitest';
import * as matchers from 'vitest-axe/matchers';
import { config } from '@vue/test-utils';
import { defineComponent, h } from 'vue';

expect.extend(matchers);

// ── Radix-Vue DialogPortal stub ──────────────────────────────────────────────
// The a11y-backlog-cleanup-01NDFSEX07 WP02 migration wrapped ArtifactPreview
// and the ProvidersView drawer in <DialogRoot><DialogPortal>. DialogPortal
// teleports content to document.body so wrapper.find() can't reach it from
// the test wrapper. Stub it as a transparent passthrough during tests —
// the production teleport behavior is preserved at build time.
const DialogPortalStub = defineComponent({
  name: 'DialogPortalStub',
  setup(_, { slots }) {
    return () => h('div', { 'data-testid': 'dialog-portal-stub' }, slots.default?.());
  },
});

config.global.stubs = {
  ...(config.global.stubs as Record<string, unknown> | undefined),
  DialogPortal: DialogPortalStub,
};
