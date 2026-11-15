import type { Config } from 'tailwindcss';

/**
 * Token-driven Tailwind theme. Every color resolves to a CSS variable
 * defined in `frontend/src/styles/tokens.css`, which `@import`s
 * `@kameas-ai/kenaz-design-system/tokens.css` directly (see that file's
 * header + REFRESH.md). Do NOT redefine token values here — only map
 * them to utility names. Privacy CI invariant #4 (plan §4.3) enforces no
 * raw color literal outside `tokens.css`.
 */
/**
 * A DS colour token as a Tailwind colour. Plain use (`text-ink`) resolves
 * to the bare `var(--ink)`; an opacity modifier (`bg-accent/10`) resolves
 * through `color-mix` against transparent. Without this, Tailwind cannot
 * apply `/NN` to a `var()` colour and silently emits NOTHING — which is
 * what ~130 `bg-accent/10`-style classes in this tree did before DS v0.13.0
 * adoption.
 */
type OpacityArg = { opacityValue?: string };
// Tailwind 3 accepts a function colour at runtime (`({ opacityValue }) =>
// string`), but its `Config` type only declares strings, hence the cast.
const tok = (v: string): string =>
  (({ opacityValue }: OpacityArg): string =>
    opacityValue === undefined || String(opacityValue).startsWith('var(')
      ? `var(${v})`
      : `color-mix(in oklab, var(${v}) calc(${opacityValue} * 100%), transparent)`) as unknown as string;
const soft = (v: string): string => `color-mix(in oklab, var(${v}) 6%, var(--surface-1))`;

export default {
  content: ['./index.html', './src/**/*.{vue,ts,tsx,js,jsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        surface: {
          0: tok('--surface-0'),
          1: tok('--surface-1'),
          2: tok('--surface-2'),
          3: tok('--surface-3'),
          4: tok('--surface-4'),
        },
        border: {
          DEFAULT: tok('--border'),
          muted: tok('--border-muted'),
          strong: tok('--border-strong'),
        },
        ink: {
          DEFAULT: tok('--ink'),
          muted: tok('--ink-muted'),
          subtle: tok('--ink-subtle'),
          dim: tok('--ink-dim'),
          trace: tok('--ink-trace'),
        },
        accent: {
          DEFAULT: tok('--accent'),
          muted: tok('--accent-muted'),
          dim: tok('--accent-dim'),
          glow: tok('--accent-glow'),
          hairline: tok('--accent-hairline'),
        },
        signal: {
          ok: tok('--ok'),
          warn: tok('--warn'),
          danger: tok('--danger'),
          info: tok('--info'),
          violet: tok('--violet'),
          neutral: tok('--signal-neutral'),
          git: tok('--signal-git'),
          // DS "signal text" cuts — use these for WORDS (status labels,
          // error copy); keep the bare signal for dots, bars and borders.
          'ok-text': tok('--ok-text'),
          'warn-text': tok('--warn-text'),
          'danger-text': tok('--danger-text'),
          // DS callout field (`.callout--{ok,warn,info}` in components.css):
          // 6% of the signal over --surface-1.
          'ok-soft': soft('--ok'),
          'warn-soft': soft('--warn'),
          'danger-soft': soft('--danger'),
          'info-soft': soft('--info'),
          'violet-soft': soft('--violet'),
        },
        modal: {
          overlay: tok('--modal-overlay'),
          shadow: tok('--modal-shadow'),
        },
      },
      // Elevation. The DS publishes one shadow colour (--modal-shadow) and
      // no shadow scale; these are the geometries its own surfaces use
      // (popover 0 2px 8px, menu 0 6px 18px, modal 0 24px 64px — wizard.css).
      boxShadow: {
        sm: '0 1px 2px var(--modal-shadow)',
        DEFAULT: '0 2px 8px var(--modal-shadow)',
        md: '0 2px 8px var(--modal-shadow)',
        lg: '0 6px 18px var(--modal-shadow)',
        xl: '0 24px 64px var(--modal-shadow)',
        '2xl': '0 24px 64px var(--modal-shadow)',
      },
      // Radii — DS v0.13.0 (`--radius-*`). `rounded` (DEFAULT) and
      // `rounded-xl` are re-pointed at the DS steps of the same pixel value
      // (4px / 12px), and `rounded-full` at `--radius-pill`, so every radius
      // utility in the tree resolves to a token. scripts/ci/check-css-tokens.mjs
      // forbids `rounded-[…]` arbitrary values.
      borderRadius: {
        xs: 'var(--radius-xs)',
        DEFAULT: 'var(--radius-sm)',
        sm: 'var(--radius-sm)',
        md: 'var(--radius-md)',
        lg: 'var(--radius-lg)',
        xl: 'var(--radius-lg)',
        pill: 'var(--radius-pill)',
        full: 'var(--radius-pill)',
      },
      // Spacing — the DS v0.13.0 `--space-*` steps, keyed by the Tailwind
      // step of the same pixel value (`p-3` = 12px = var(--space-12)), so
      // the change is value-identical and every on-scale spacing utility is
      // token-backed. Off-scale steps (7, 9, 10, …) stay Tailwind defaults
      // because the same scale feeds width/height/inset; the gate forbids
      // them for padding/margin/gap instead.
      spacing: {
        '0.5': 'var(--space-2)',
        '1': 'var(--space-4)',
        '1.5': 'var(--space-6)',
        '2': 'var(--space-8)',
        '2.5': 'var(--space-10)',
        '3': 'var(--space-12)',
        '3.5': 'var(--space-14)',
        '4': 'var(--space-16)',
        '5': 'var(--space-20)',
        '6': 'var(--space-24)',
        '8': 'var(--space-32)',
        '12': 'var(--space-48)',
      },
      // Type — DS v0.13.0 `--type-*` sizes. Named utilities replace the
      // `text-[Npx]` literals the views used to carry; `text-xs` / `text-sm`
      // keep their Tailwind line-heights but resolve to the DS steps of the
      // same size (12px small / 14px body). `text-base|lg|xl|2xl|3xl` are
      // off the DS scale and forbidden by check-css-tokens.mjs.
      fontSize: {
        'eyebrow-xs': 'var(--type-eyebrow-xs-size)',
        tag: 'var(--type-tag-size)',
        eyebrow: 'var(--type-eyebrow-size)',
        caption: 'var(--type-caption-size)',
        small: 'var(--type-small-size)',
        ui: 'var(--type-ui-size)',
        body: 'var(--type-body-size)',
        h3: 'var(--type-h3-size)',
        title: 'var(--type-title-size)',
        'canvas-title': 'var(--type-canvas-title-size)',
        hero: 'var(--type-hero-size)',
        xs: ['var(--type-small-size)', { lineHeight: '1rem' }],
        sm: ['var(--type-body-size)', { lineHeight: '1.25rem' }],
      },
      letterSpacing: {
        eyebrow: 'var(--type-eyebrow-tracking)',
        'eyebrow-xs': 'var(--type-eyebrow-xs-tracking)',
        meta: 'var(--type-meta-mono-tracking)',
        h3: 'var(--type-h3-tracking)',
        title: 'var(--type-title-tracking)',
        hero: 'var(--type-hero-tracking)',
      },
      fontFamily: {
        ui: 'var(--font-ui)',
        mono: 'var(--font-mono)',
        // Heading register. DS 0.12 retired the serif (Newsreader /
        // --font-serif); every heading points at --font-heading, which the
        // DS resolves to Geist.
        heading: 'var(--font-heading)',
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
