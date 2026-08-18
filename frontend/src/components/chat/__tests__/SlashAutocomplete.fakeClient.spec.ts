/**
 * SlashAutocomplete.fakeClient.spec.ts — the fake client's slash.list()
 * data no longer lies about "(coming soon)" commands (engineer-truth-
 * pass-01PMTP01 WP07, finding B18, comment ⑲ — the carved-out
 * exception where the lie is in data, not a comment).
 *
 * Before this WP, createFakeHarnessClient().slash.list() returned
 * comingSoon: true with "(coming soon)" descriptions for /memorize,
 * /recall, /forget and /branch — contradicting the live Go registry
 * (all four are real implementations) and the Go test that pins it,
 * core/rpc/views/slashcmd/impl_test.go's
 * TestAPI_List_SortedAndNoneComingSoon ("all default commands are
 * wired in v0.3.0"). This is the frontend mirror of that Go test: it
 * renders SlashAutocomplete against the fake client's real data and
 * asserts zero "(coming soon)" badges.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SlashAutocomplete from '@/components/chat/SlashAutocomplete.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';

describe('SlashAutocomplete — against the fake client (B18 / mirrors TestAPI_List_SortedAndNoneComingSoon)', () => {
  it('renders zero "(coming soon)" badges for the fake client\'s command list', async () => {
    const client = createFakeHarnessClient();
    const commands = await client.slash.list();

    // Sanity: the fake still returns all seven default commands.
    expect(commands.map((c) => c.name).sort()).toEqual(
      ['branch', 'clear', 'forget', 'help', 'memorize', 'model', 'recall'],
    );

    const w = mount(SlashAutocomplete, {
      props: { commands, query: '', activeIndex: 0 },
    });

    expect(w.findAll('[data-testid="slash-coming-soon-tag"]')).toHaveLength(0);
    expect(w.text()).not.toContain('coming soon');
    for (const cmd of commands) {
      expect(cmd.comingSoon, `${cmd.name}.comingSoon`).toBe(false);
    }
  });
});
