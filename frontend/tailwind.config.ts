import type { Config } from 'tailwindcss';

/**
 * Token-driven Tailwind theme. Every color resolves to a CSS variable
 * defined in `frontend/src/styles/tokens.css` (vendored verbatim from
 * Kenaz). Do NOT redefine token values here — only map them to utility
 * names. Privacy CI invariant #4 (plan §4.3) enforces no raw color
 * literal outside `tokens.css`.
 */
export default {
  content: ['./index.html', './src/**/*.{vue,ts,tsx,js,jsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        surface: {
          0: 'var(--surface-0)',
          1: 'var(--surface-1)',
          2: 'var(--surface-2)',
          3: 'var(--surface-3)',
          4: 'var(--surface-4)',
        },
        border: {
          DEFAULT: 'var(--border)',
          muted: 'var(--border-muted)',
          strong: 'var(--border-strong)',
        },
        ink: {
          DEFAULT: 'var(--ink)',
          muted: 'var(--ink-muted)',
          subtle: 'var(--ink-subtle)',
          dim: 'var(--ink-dim)',
          trace: 'var(--ink-trace)',
        },
        accent: {
          DEFAULT: 'var(--accent)',
          muted: 'var(--accent-muted)',
          dim: 'var(--accent-dim)',
          glow: 'var(--accent-glow)',
          hairline: 'var(--accent-hairline)',
        },
        signal: {
          ok: 'var(--ok)',
          warn: 'var(--warn)',
          danger: 'var(--danger)',
          info: 'var(--info)',
          violet: 'var(--violet)',
          neutral: 'var(--signal-neutral)',
          git: 'var(--signal-git)',
        },
        modal: {
          overlay: 'var(--modal-overlay)',
          shadow: 'var(--modal-shadow)',
        },
      },
      borderRadius: {
        sm: 'var(--radius-sm)',
        md: 'var(--radius-md)',
        lg: 'var(--radius-lg)',
      },
      fontFamily: {
        ui: 'var(--font-ui)',
        mono: 'var(--font-mono)',
      },
      transitionDuration: {
        fast: 'var(--motion-fast)',
        base: 'var(--motion-base)',
        slow: 'var(--motion-slow)',
      },
      transitionTimingFunction: {
        kenaz: 'cubic-bezier(0.2, 0, 0, 1)',
      },
      screens: {
        'two-col': '960px',
      },
    },
  },
  plugins: [],
} satisfies Config;
