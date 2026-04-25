import vue from 'eslint-plugin-vue';
import tseslint from '@typescript-eslint/eslint-plugin';
import tsParser from '@typescript-eslint/parser';

/**
 * Flat config. The `no-restricted-imports` rule forbids `wailsjs/*` outside
 * `src/lib/harnessClient.ts` per plan §4.1 and FR-007 / C-001 — only the
 * typed RPC client is allowed to touch the Wails-generated bindings.
 */
export default [
  {
    files: ['src/**/*.{ts,vue,js}'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
        extraFileExtensions: ['.vue'],
      },
    },
    plugins: {
      vue,
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
