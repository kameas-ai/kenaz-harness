// Vitest global setup. Anything common across the test suite goes here.

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
