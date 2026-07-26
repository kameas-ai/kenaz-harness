/**
 * Host named-theme override (workspace spec 073 FR-3/FR-4).
 *
 * The workbench chrome appends ?theme=<ink|linen|azure|paper> to the
 * harness iframe src; the override is ephemeral and never touches the
 * persisted light/dark/system setting.
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { hostThemeParam, applyHostTheme, HOST_THEMES } from '../useTheme';

beforeEach(() => {
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.classList.remove('dark');
});

describe('hostThemeParam', () => {
  it('accepts every named host theme', () => {
    for (const t of HOST_THEMES) {
      expect(hostThemeParam(`?theme=${t}`)).toBe(t);
    }
  });

  it('rejects unknown values and absence', () => {
    expect(hostThemeParam('?theme=dark')).toBeNull();
    expect(hostThemeParam('?theme=solarized')).toBeNull();
    expect(hostThemeParam('')).toBeNull();
    expect(hostThemeParam('?workbench=abc')).toBeNull();
  });
});

describe('applyHostTheme', () => {
  it('sets data-theme and maps the dark class per theme', () => {
    const root = document.documentElement;

    applyHostTheme('paper');
    expect(root.getAttribute('data-theme')).toBe('paper');
    expect(root.classList.contains('dark')).toBe(false);
    expect(root.style.colorScheme).toBe('light');

    applyHostTheme('ink');
    expect(root.getAttribute('data-theme')).toBe('ink');
    expect(root.classList.contains('dark')).toBe(true);
    expect(root.style.colorScheme).toBe('dark');

    applyHostTheme('azure');
    expect(root.classList.contains('dark')).toBe(true);

    applyHostTheme('linen');
    expect(root.classList.contains('dark')).toBe(false);
  });
});
