import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SlashAutocomplete from '@/components/chat/SlashAutocomplete.vue';
import type { SlashCommandInfo } from '@/lib/types';

const COMMANDS: SlashCommandInfo[] = [
  { name: 'branch', description: 'branch desc', comingSoon: true },
  { name: 'clear', description: 'clear desc', comingSoon: false },
  { name: 'forget', description: 'forget desc', comingSoon: true },
  { name: 'help', description: 'help desc', comingSoon: false },
  { name: 'memorize', description: 'mem desc', comingSoon: true },
  { name: 'model', description: 'model desc', comingSoon: false },
  { name: 'recall', description: 'recall desc', comingSoon: true },
];

function mountAutocomplete(props: {
  query?: string;
  activeIndex?: number;
  commands?: readonly SlashCommandInfo[];
} = {}) {
  return mount(SlashAutocomplete, {
    props: {
      commands: props.commands ?? COMMANDS,
      query: props.query ?? '',
      activeIndex: props.activeIndex ?? 0,
    },
  });
}

describe('SlashAutocomplete', () => {
  it('renders every command when query is empty', () => {
    const w = mountAutocomplete();
    const opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts).toHaveLength(COMMANDS.length);
  });

  it('filters by case-insensitive prefix match on name', () => {
    const w = mountAutocomplete({ query: 'me' });
    const opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts).toHaveLength(1);
    expect(opts[0].text()).toContain('memorize');
  });

  it('case-insensitive: uppercase query still matches', () => {
    const w = mountAutocomplete({ query: 'MO' });
    const opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts).toHaveLength(1);
    expect(opts[0].text()).toContain('model');
  });

  it('renders nothing when no command matches', () => {
    const w = mountAutocomplete({ query: 'zzz-no-match' });
    const root = w.find('[data-testid="slash-suggestions"]');
    expect(root.exists()).toBe(false);
  });

  it('marks coming-soon commands with the tag', () => {
    const w = mountAutocomplete({ query: 'branch' });
    const tag = w.find('[data-testid="slash-coming-soon-tag"]');
    expect(tag.exists()).toBe(true);
  });

  it('does not mark real commands with the coming-soon tag', () => {
    const w = mountAutocomplete({ query: 'help' });
    const tag = w.find('[data-testid="slash-coming-soon-tag"]');
    expect(tag.exists()).toBe(false);
  });

  it('highlights the active option via aria-selected', () => {
    const w = mountAutocomplete({ query: '', activeIndex: 2 });
    const opts = w.findAll('[role="option"]');
    expect(opts[0].attributes('aria-selected')).toBe('false');
    expect(opts[2].attributes('aria-selected')).toBe('true');
  });

  it('emits select with the command name on mousedown', async () => {
    const w = mountAutocomplete({ query: '' });
    const opts = w.findAll('[data-testid^="slash-option-"]');
    await opts[3].trigger('mousedown');
    expect(w.emitted('select')).toBeTruthy();
    // commands sorted alphabetically: branch, clear, forget, help, memorize, model, recall
    // index 3 → help
    expect(w.emitted('select')![0]).toEqual(['help']);
  });

  it('exposes the filtered slice via the component instance', () => {
    const w = mountAutocomplete({ query: 'm' });
    // memorize, model
    const exposed = (w.vm as unknown as { filtered: readonly SlashCommandInfo[] })
      .filtered;
    expect(exposed.map((c) => c.name)).toEqual(['memorize', 'model']);
  });

  it('shows the description alongside each command name', () => {
    const w = mountAutocomplete({ query: 'help' });
    expect(w.text()).toContain('help desc');
  });

  it('returns the full list when query is whitespace-only', () => {
    const w = mountAutocomplete({ query: '   ' });
    const opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts).toHaveLength(COMMANDS.length);
  });
});
