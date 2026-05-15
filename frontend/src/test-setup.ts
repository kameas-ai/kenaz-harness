// Vitest global setup. Anything common across the test suite goes here.

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

expect.extend(matchers);
