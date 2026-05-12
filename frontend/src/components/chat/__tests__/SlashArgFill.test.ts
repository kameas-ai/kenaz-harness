/**
 * SlashArgFill.test.ts
 *
 * Component tests for the arg-fill chip strip + input widgets.
 * user-slash-commands-01KQ8TD9 WP06.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SlashArgFill from '@/components/chat/SlashArgFill.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { UserCommand } from '@/lib/types';

// ── Fixtures ──────────────────────────────────────────────────────────────

const TEXT_CMD: UserCommand = {
  name: 'greet',
  scope: 'global',
  kind: 'text',
  description: 'Greeting command',
  modelInvokable: false,
  body: 'Hello {{name}}',
  inputs: [
    { name: 'name', kind: 'text', required: true, hint: 'Your name' },
  ],
};

const MULTI_CMD: UserCommand = {
  name: 'deploy',
  scope: 'global',
  kind: 'tool',
  description: 'Deploy command',
  modelInvokable: false,
  tool: 'bash',
  toolArgsTemplate: './deploy.sh --env {{env}} --count {{count}}',
  inputs: [
    {
      name: 'env',
      kind: 'enum',
      required: true,
      enumValues: ['dev', 'staging', 'prod'],
    },
    { name: 'count', kind: 'number', required: false, default: '1' },
  ],
};

const ALL_KINDS_CMD: UserCommand = {
  name: 'allkinds',
  scope: 'global',
  kind: 'text',
  description: 'All 6 input kinds',
  modelInvokable: false,
  body: '{{a}}{{b}}{{c}}{{d}}{{e}}{{f}}',
  inputs: [
    { name: 'a', kind: 'text', required: true },
    { name: 'b', kind: 'number', required: true },
    { name: 'c', kind: 'enum', required: true, enumValues: ['x', 'y'] },
    { name: 'd', kind: 'file', required: false },
    { name: 'e', kind: 'artifact_ref', required: false },
    { name: 'f', kind: 'project_ref', required: false },
  ],
};

// ── Mount helper ──────────────────────────────────────────────────────────

function mountFill(
  command: UserCommand,
  prefilled?: Record<string, string>,
  pickFileImpl?: () => Promise<string>,
) {
  const fakePickFile = pickFileImpl ?? vi.fn(async () => '');
  return mount(SlashArgFill, {
    props: { command, prefilled },
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              shell: {
                openInOSBrowser: vi.fn(),
                pathComplete: async () => [],
                readFile: async () => ({ dataBase64: '', mediaType: '' }),
                pickFile: fakePickFile,
              },
            });
          },
        },
      ],
    },
  });
}

// ── Tests ────────────────────────────────────────────────────────────────

describe('SlashArgFill — structure', () => {
  it('renders the panel root with correct testid', () => {
    const w = mountFill(TEXT_CMD);
    expect(w.find('[data-testid="slash-arg-fill"]').exists()).toBe(true);
  });

  it('shows the command name in the title', () => {
    const w = mountFill(TEXT_CMD);
    expect(w.find('[data-testid="arg-fill-title"]').text()).toContain('greet');
  });

  it('renders one chip per input', () => {
    const w = mountFill(MULTI_CMD);
    const chips = w.findAll('[data-testid^="arg-chip-"]');
    expect(chips).toHaveLength(2);
    expect(chips[0].text()).toContain('env');
    expect(chips[1].text()).toContain('count');
  });

  it('marks optional inputs with "?" in the chip', () => {
    const w = mountFill(MULTI_CMD);
    const countChip = w.find('[data-testid="arg-chip-count"]');
    expect(countChip.text()).toContain('?');
  });

  it('emits cancel when the X button is clicked', async () => {
    const w = mountFill(TEXT_CMD);
    await w.find('[data-testid="arg-fill-cancel"]').trigger('click');
    expect(w.emitted('cancel')).toBeTruthy();
  });

  it('emits cancel when the cancel button in the action row is clicked', async () => {
    const w = mountFill(TEXT_CMD);
    await w.find('[data-testid="arg-fill-cancel-btn"]').trigger('click');
    expect(w.emitted('cancel')).toBeTruthy();
  });
});

describe('SlashArgFill — text widget', () => {
  it('renders a text input for kind=text', () => {
    const w = mountFill(TEXT_CMD);
    expect(w.find('[data-testid="arg-widget-text-name"]').exists()).toBe(true);
  });

  it('submit button is disabled when required text input is empty', () => {
    const w = mountFill(TEXT_CMD);
    const btn = w.find('[data-testid="arg-fill-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(true);
  });

  it('submit button enables when required text is filled', async () => {
    const w = mountFill(TEXT_CMD);
    const input = w.find('[data-testid="arg-widget-text-name"]');
    await input.setValue('Alice');
    const btn = w.find('[data-testid="arg-fill-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('emits submit with resolved args when submit is clicked', async () => {
    const w = mountFill(TEXT_CMD);
    const input = w.find('[data-testid="arg-widget-text-name"]');
    await input.setValue('Alice');
    await w.find('[data-testid="arg-fill-submit"]').trigger('click');
    expect(w.emitted('submit')).toBeTruthy();
    const [args] = w.emitted('submit')![0] as [Record<string, string>];
    expect(args).toEqual({ name: 'Alice' });
  });

  it('seeds from prefilled prop', async () => {
    const w = mountFill(TEXT_CMD, { name: 'Bob' });
    await flushPromises();
    const input = w.find('[data-testid="arg-widget-text-name"]') as ReturnType<typeof w.find>;
    expect((input.element as HTMLInputElement).value).toBe('Bob');
  });

  it('submit is enabled immediately when prefilled provides all required args', async () => {
    const w = mountFill(TEXT_CMD, { name: 'Bob' });
    await flushPromises();
    const btn = w.find('[data-testid="arg-fill-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });
});

describe('SlashArgFill — number widget', () => {
  it('renders a number input for kind=number', () => {
    const w = mountFill(MULTI_CMD);
    expect(w.find('[data-testid="arg-widget-number-count"]').exists()).toBe(true);
  });

  it('pre-seeds number widget from input.default', async () => {
    const w = mountFill(MULTI_CMD);
    await flushPromises();
    const input = w.find('[data-testid="arg-widget-number-count"]') as ReturnType<typeof w.find>;
    expect((input.element as HTMLInputElement).value).toBe('1');
  });
});

describe('SlashArgFill — enum widget', () => {
  it('renders a <select> for kind=enum', () => {
    const w = mountFill(MULTI_CMD);
    expect(w.find('[data-testid="arg-widget-enum-env"]').exists()).toBe(true);
  });

  it('populates the <select> with enumValues', () => {
    const w = mountFill(MULTI_CMD);
    const opts = w.findAll('[data-testid="arg-widget-enum-env"] option');
    const optTexts = opts.map((o) => o.text());
    expect(optTexts).toContain('dev');
    expect(optTexts).toContain('staging');
    expect(optTexts).toContain('prod');
  });

  it('selecting an enum value enables submit (with optional number defaulted)', async () => {
    const w = mountFill(MULTI_CMD);
    await flushPromises();
    const sel = w.find('[data-testid="arg-widget-enum-env"]');
    await sel.setValue('staging');
    const btn = w.find('[data-testid="arg-fill-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('submit emits correct args after enum + number selection', async () => {
    const w = mountFill(MULTI_CMD);
    await flushPromises();
    await w.find('[data-testid="arg-widget-enum-env"]').setValue('prod');
    await w.find('[data-testid="arg-widget-number-count"]').setValue('3');
    await w.find('[data-testid="arg-fill-submit"]').trigger('click');
    const [args] = w.emitted('submit')![0] as [Record<string, string>];
    expect(args.env).toBe('prod');
    expect(args.count).toBe('3');
  });
});

describe('SlashArgFill — file widget', () => {
  it('renders the file widget with a pick-file button for kind=file', () => {
    const w = mountFill(ALL_KINDS_CMD);
    expect(w.find('[data-testid="arg-widget-file-d"]').exists()).toBe(true);
    expect(w.find('[data-testid="arg-pick-file-d"]').exists()).toBe(true);
  });

  it('calls shell.pickFile and populates the input on picker confirm', async () => {
    const pickFileMock = vi.fn(async () => '/home/user/report.pdf');
    const w = mountFill(ALL_KINDS_CMD, undefined, pickFileMock);
    await w.find('[data-testid="arg-pick-file-d"]').trigger('click');
    await flushPromises();
    expect(pickFileMock).toHaveBeenCalledOnce();
    const input = w.find('[data-testid="arg-widget-file-d"] input') as ReturnType<typeof w.find>;
    expect((input.element as HTMLInputElement).value).toBe('/home/user/report.pdf');
  });

  it('does not populate the input when picker returns empty string (cancelled)', async () => {
    const pickFileMock = vi.fn(async () => '');
    const w = mountFill(ALL_KINDS_CMD, undefined, pickFileMock);
    await w.find('[data-testid="arg-pick-file-d"]').trigger('click');
    await flushPromises();
    const input = w.find('[data-testid="arg-widget-file-d"] input') as ReturnType<typeof w.find>;
    expect((input.element as HTMLInputElement).value).toBe('');
  });
});

describe('SlashArgFill — artifact_ref and project_ref widgets', () => {
  it('renders a text input for kind=artifact_ref', () => {
    const w = mountFill(ALL_KINDS_CMD);
    expect(w.find('[data-testid="arg-widget-artifact_ref-e"]').exists()).toBe(true);
  });

  it('renders a text input for kind=project_ref', () => {
    const w = mountFill(ALL_KINDS_CMD);
    expect(w.find('[data-testid="arg-widget-project_ref-f"]').exists()).toBe(true);
  });
});

describe('SlashArgFill — all 6 widget kinds', () => {
  it('renders all 6 widget kinds for a command that declares all kinds', () => {
    const w = mountFill(ALL_KINDS_CMD);
    expect(w.find('[data-testid="arg-widget-text-a"]').exists()).toBe(true);
    expect(w.find('[data-testid="arg-widget-number-b"]').exists()).toBe(true);
    expect(w.find('[data-testid="arg-widget-enum-c"]').exists()).toBe(true);
    expect(w.find('[data-testid="arg-widget-file-d"]').exists()).toBe(true);
    expect(w.find('[data-testid="arg-widget-artifact_ref-e"]').exists()).toBe(true);
    expect(w.find('[data-testid="arg-widget-project_ref-f"]').exists()).toBe(true);
  });

  it('submit only enabled once required text and number and enum are all filled', async () => {
    const w = mountFill(ALL_KINDS_CMD);
    await flushPromises();
    // Initially disabled (a=text required, b=number required, c=enum required)
    expect(
      (w.find('[data-testid="arg-fill-submit"]').element as HTMLButtonElement).disabled,
    ).toBe(true);
    await w.find('[data-testid="arg-widget-text-a"]').setValue('hello');
    await w.find('[data-testid="arg-widget-number-b"]').setValue('42');
    await w.find('[data-testid="arg-widget-enum-c"]').setValue('x');
    // d, e, f are optional — should now be enabled
    expect(
      (w.find('[data-testid="arg-fill-submit"]').element as HTMLButtonElement).disabled,
    ).toBe(false);
  });
});

describe('SlashArgFill — chip navigation', () => {
  it('clicking a chip focuses that input (sets focusedIndex)', async () => {
    const w = mountFill(MULTI_CMD);
    // Click the second chip (count)
    await w.find('[data-testid="arg-chip-count"]').trigger('click');
    // The chip should now have aria-selected="true"
    expect(
      w.find('[data-testid="arg-chip-count"]').attributes('aria-selected'),
    ).toBe('true');
    expect(
      w.find('[data-testid="arg-chip-env"]').attributes('aria-selected'),
    ).toBe('false');
  });
});

describe('SlashArgFill — keyboard: Escape emits cancel', () => {
  it('pressing Escape on an input widget emits cancel', async () => {
    const w = mountFill(TEXT_CMD);
    const input = w.find('[data-testid="arg-widget-text-name"]');
    await input.trigger('keydown', { key: 'Escape' });
    expect(w.emitted('cancel')).toBeTruthy();
  });
});

describe('SlashArgFill — command with no inputs', () => {
  const NO_INPUT_CMD: UserCommand = {
    name: 'simple',
    scope: 'global',
    kind: 'text',
    description: 'No args',
    modelInvokable: false,
    body: 'hello world',
  };

  it('renders the panel even when inputs is undefined', () => {
    const w = mountFill(NO_INPUT_CMD);
    expect(w.find('[data-testid="slash-arg-fill"]').exists()).toBe(true);
  });

  it('submit button is enabled when there are no required inputs', () => {
    const w = mountFill(NO_INPUT_CMD);
    const btn = w.find('[data-testid="arg-fill-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('emits submit with empty args when no inputs declared', async () => {
    const w = mountFill(NO_INPUT_CMD);
    await w.find('[data-testid="arg-fill-submit"]').trigger('click');
    const [args] = w.emitted('submit')![0] as [Record<string, string>];
    expect(args).toEqual({});
  });
});
