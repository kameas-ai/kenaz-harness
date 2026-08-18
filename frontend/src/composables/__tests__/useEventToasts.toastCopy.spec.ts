/**
 * AC-7 (self-update-repair-01PMUP01): no shipped toast string names a
 * control that does not exist. `UpdateIndicator` / `UpdateMenu` /
 * `UpdateToast` (the ⬆ rail affordance) were retired by
 * os-menu-bar-01NDFSEX16 §FR-006/§FR-020/§FR-021 in favour of
 * Help → Check for Updates; the update:available toast's copy pointed at
 * the deleted glyph until this mission (spec §1.3).
 *
 * A source-level scan rather than a mount-and-fire behavioural test: the
 * composable's setup (harnessClient, broker, connection-state, event
 * stream) is heavy, and the actual defect + fix are entirely in the
 * string literal — reading the shipped source is the direct assertion,
 * not a proxy for one.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const SOURCE_PATH = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
  'useEventToasts.ts',
);

function readSource(): string {
  return readFileSync(SOURCE_PATH, 'utf-8');
}

describe('useEventToasts — update-available toast copy (AC-7)', () => {
  it('contains no ⬆ character anywhere in the shipped source', () => {
    const src = readSource();
    expect(src).not.toContain('⬆');
  });

  it("names the control that replaced it — 'Help' + 'Check for Updates'", () => {
    // The replacement target: core/menu/state.go's
    // UpdateMenuLabel(UpdateIdle) === "Check for Updates…" living under
    // the "Help" submenu (core/menu/menu.go's buildHelpMenu →
    // wailsmenu.SubMenu("Help", sub)). This test locks the TS copy to
    // those two literal strings; a rename on either side without
    // updating this file's expectation is the failure mode this test
    // exists to catch — see also core/menu/state_test.go's
    // TestUpdateTopicState_MatchesProductionTopics for the analogous
    // Go-side topic lock.
    const src = readSource();
    const match = src.match(/push\(`Kenaz \$\{ver\} is available[^`]*`/);
    expect(match, 'update-available toast push() call not found').not.toBeNull();
    const toastTemplate = match![0];
    expect(toastTemplate).toContain('Help');
    expect(toastTemplate).toContain('Check for Updates');
  });
});
