import vue from 'eslint-plugin-vue';
import tseslint from '@typescript-eslint/eslint-plugin';
import tsParser from '@typescript-eslint/parser';
import vueParser from 'vue-eslint-parser';
import vueA11y from 'eslint-plugin-vuejs-accessibility';

/**
 * Flat config. The `no-restricted-imports` rule forbids `wailsjs/*` outside
 * `src/lib/harnessClient.ts` per plan §4.1 and FR-007 / C-001 — only the
 * typed RPC client is allowed to touch the Wails-generated bindings.
 *
 * eslint-plugin-vuejs-accessibility is included for the a11y audit pass
 * (accessibility-audit-01KQ8TDA). Rules are tuned per the WP01 triage below.
 */
export default [
  // ── TypeScript + JS files ──────────────────────────────────────────────
  {
    files: ['src/**/*.{ts,js}'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
      },
    },
    plugins: {
      '@typescript-eslint': tseslint,
    },
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['**/wailsjs/*', '../../wailsjs/*', '../wailsjs/*'],
              message:
                'Only frontend/src/lib/harnessClient.ts may import wailsjs/* — see plan §4.1.',
            },
          ],
        },
      ],
      '@typescript-eslint/no-explicit-any': 'error',
    },
  },
  // ── Vue SFC files ─────────────────────────────────────────────────────
  {
    files: ['src/**/*.vue'],
    languageOptions: {
      parser: vueParser,
      parserOptions: {
        parser: tsParser,
        ecmaVersion: 'latest',
        sourceType: 'module',
      },
    },
    plugins: {
      vue,
      '@typescript-eslint': tseslint,
      'vuejs-accessibility': vueA11y,
    },
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['**/wailsjs/*', '../../wailsjs/*', '../wailsjs/*'],
              message:
                'Only frontend/src/lib/harnessClient.ts may import wailsjs/* — see plan §4.1.',
            },
          ],
        },
      ],
      '@typescript-eslint/no-explicit-any': 'error',

      // ── vuejs-accessibility rules ──────────────────────────────────────
      // WP01 triage: rules enabled for a11y audit pass
      // (accessibility-audit-01KQ8TDA WP01).
      //
      // Enforcement levels:
      //   error  — structural correctness; always fixable; no false positives
      //   warn   — pattern has known exceptions in this codebase; triage
      //            documented per violation in WP05 report
      //
      //   - anchor-has-content: error — nav links must have text
      //   - aria-props: error — catches typos in aria-* attribute names
      //   - aria-role: error — catches invalid role values
      //   - aria-unsupported-elements: error — structural correctness
      //   - form-control-has-label: error — all form inputs need labels
      //   - heading-has-content: error — headings must have text
      //   - interactive-supports-focus: warn — icon-only buttons in rail may
      //     rely on parent aria-label; deferred to WP04
      //   - label-has-for: warn — some dynamic id associations are indirect
      //   - media-has-caption: warn — audio/video artifact previews deferred;
      //     Wails WebKit has limited caption support
      //   - mouse-events-have-key-events: warn — drag-and-drop interactions
      //     have dedicated keyboard fallbacks evaluated in WP03
      'vuejs-accessibility/anchor-has-content': 'error',
      'vuejs-accessibility/aria-props': 'error',
      'vuejs-accessibility/aria-role': 'error',
      'vuejs-accessibility/aria-unsupported-elements': 'error',
      'vuejs-accessibility/form-control-has-label': 'error',
      'vuejs-accessibility/heading-has-content': 'error',
      'vuejs-accessibility/interactive-supports-focus': 'warn',
      'vuejs-accessibility/label-has-for': 'warn',
      'vuejs-accessibility/media-has-caption': 'warn',
      'vuejs-accessibility/mouse-events-have-key-events': 'warn',
    },
  },
  {
    files: ['src/lib/harnessClient.ts'],
    rules: {
      'no-restricted-imports': 'off',
    },
  },
  {
    ignores: ['dist/**', 'node_modules/**', 'wailsjs/**'],
  },
];
