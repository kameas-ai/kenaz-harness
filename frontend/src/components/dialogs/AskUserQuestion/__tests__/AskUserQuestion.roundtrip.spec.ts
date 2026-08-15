/**
 * AskUserQuestion.roundtrip.spec.ts — one test per question kind,
 * asserting the SHAPE that reaches client.elicit.submitAnswer.
 *
 * The shape is a contract, not a detail. kenaz__ask_user_question's JSON
 * schema tells the model what `answer` will look like per kind ("string
 * for text/date/radio/file, number for number/slider, array of strings
 * for checkbox"), and the backend passes whatever arrives straight
 * through to the model as a json.RawMessage. So a wrong shape here is not
 * a rendering bug — it is the harness lying to the model about its own
 * tool.
 *
 * These seven kinds had never executed inside the app: the dialog was
 * never mounted (see AppElicitationMount.spec.ts), so every child here
 * was exercised only by whatever unit test it happened to have. Two
 * (file, preview) had one; the other five had none.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AskUserQuestion from '../AskUserQuestion.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import type { ElicitRequest } from '@/lib/types';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  EventsOff: (topic: string) => void;
  emit: (topic: string, payload?: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn(topic, cb) {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => { s!.delete(cb); };
    },
    EventsOff(topic) { handlers.delete(topic); },
    emit(topic, payload) {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload ?? null);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

describe('AskUserQuestion — answer shape per question kind', () => {
  let rt: FakeRuntime;
  let client: ReturnType<typeof createFakeHarnessClient>;
  let submitSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    client = createFakeHarnessClient();
    client.shell.pickFile = vi.fn().mockResolvedValue('/tmp/report.pdf');
    submitSpy = vi.spyOn(client.elicit, 'submitAnswer').mockResolvedValue(undefined) as never;
  });

  afterEach(() => {
    delete (window as unknown as { runtime?: unknown }).runtime;
  });

  function mountDialog() {
    return mount(AskUserQuestion, {
      attachTo: document.body,
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
  }

  /** Emits an ask, runs the caller's interaction, clicks Submit, returns the answer sent. */
  async function roundtrip(
    req: Partial<ElicitRequest>,
    interact?: (wrapper: ReturnType<typeof mountDialog>) => Promise<void>,
  ): Promise<unknown> {
    const wrapper = mountDialog();
    await flushPromises();
    rt.emit('elicit:pending', { request_id: 'r1', question: 'Q?', ...req });
    await flushPromises();
    if (interact) await interact(wrapper);
    await flushPromises();
    await wrapper.find('[data-testid="ask-dialog-submit"]').trigger('click');
    await flushPromises();
    expect(submitSpy).toHaveBeenCalledTimes(1);
    const call = submitSpy.mock.calls[0];
    expect(call[0]).toBe('r1');
    expect(call[2]).toBe(false);
    wrapper.unmount();
    return call[1];
  }

  it('radio → the selected option value (a string)', async () => {
    const answer = await roundtrip(
      {
        kind: 'radio',
        options: [
          { value: 'sqlite', label: 'SQLite' },
          { value: 'postgres', label: 'Postgres' },
        ],
      },
      async (w) => {
        await w.find('[data-testid="radio-option-postgres"] input').trigger('change');
      },
    );
    expect(answer).toBe('postgres');
  });

  it('checkbox → an array of the ticked values', async () => {
    const answer = await roundtrip(
      {
        kind: 'checkbox',
        options: [
          { value: 'go', label: 'Go' },
          { value: 'ts', label: 'TypeScript' },
          { value: 'rs', label: 'Rust' },
        ],
      },
      async (w) => {
        await w.find('[data-testid="checkbox-option-go"] input').trigger('change');
        await w.find('[data-testid="checkbox-option-rs"] input').trigger('change');
      },
    );
    // An ARRAY — not the string '["go","rs"]'. Pre-stringifying on the
    // frontend double-encodes, because the Go parameter is a
    // json.RawMessage filled from the argument's own JSON text.
    expect(Array.isArray(answer)).toBe(true);
    expect(answer).toEqual(['go', 'rs']);
  });

  it('text → the typed string', async () => {
    const answer = await roundtrip({ kind: 'text' }, async (w) => {
      const ta = w.find('[data-testid="text-question-input"]');
      await ta.setValue('ship it on friday');
    });
    expect(answer).toBe('ship it on friday');
  });

  it('number → a number, not the input string', async () => {
    const answer = await roundtrip({ kind: 'number', min: 1, max: 100 }, async (w) => {
      await w.find('[data-testid="number-question-input"]').setValue('42');
    });
    expect(answer).toBe(42);
    expect(typeof answer).toBe('number');
  });

  it('slider → a number', async () => {
    const answer = await roundtrip({ kind: 'slider', min: 0, max: 10, step: 1 }, async (w) => {
      await w.find('[data-testid="slider-question-input"]').setValue('7');
    });
    expect(answer).toBe(7);
    expect(typeof answer).toBe('number');
  });

  it('date → a YYYY-MM-DD string', async () => {
    const answer = await roundtrip({ kind: 'date' }, async (w) => {
      await w.find('[data-testid="date-question-input"]').setValue('2026-08-14');
    });
    expect(answer).toBe('2026-08-14');
  });

  it('file → the picked path string', async () => {
    const answer = await roundtrip({ kind: 'file' }, async (w) => {
      await w.find('[data-testid="file-question-pick-btn"]').trigger('click');
      await flushPromises();
    });
    expect(answer).toBe('/tmp/report.pdf');
  });

  it('defaults are pre-filled and submit without further interaction', async () => {
    const answer = await roundtrip({
      kind: 'text',
      default_value: 'the default answer',
    });
    expect(answer).toBe('the default answer');
  });

  it('renders the preview pane when the ask carries one', async () => {
    const wrapper = mountDialog();
    await flushPromises();
    rt.emit('elicit:pending', {
      request_id: 'r-preview',
      question: 'Does this diff look right?',
      kind: 'radio',
      options: [{ value: 'y', label: 'Yes' }],
      preview: { kind: 'code', content: 'const x = 1;', language: 'typescript' },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="ask-dialog-preview-pane"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('const x = 1;');
    wrapper.unmount();
  });

  it('an unknown kind says so instead of rendering a dead dialog', async () => {
    const wrapper = mountDialog();
    await flushPromises();
    rt.emit('elicit:pending', {
      request_id: 'r-weird',
      question: 'Pick a colour',
      kind: 'colour-wheel',
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="ask-dialog-unknown-kind"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it('a second question of the SAME kind does not inherit the first answer', async () => {
    const wrapper = mountDialog();
    await flushPromises();

    const opts = [
      { value: 'a', label: 'A' },
      { value: 'b', label: 'B' },
    ];
    rt.emit('elicit:pending', { request_id: 'q1', question: 'First?', kind: 'radio', options: opts });
    rt.emit('elicit:pending', {
      request_id: 'q2',
      question: 'Second?',
      kind: 'radio',
      options: [
        { value: 'x', label: 'X' },
        { value: 'y', label: 'Y' },
      ],
    });
    await flushPromises();

    // Answer the first with the non-default option.
    await wrapper.find('[data-testid="radio-option-b"] input').trigger('change');
    await wrapper.find('[data-testid="ask-dialog-submit"]').trigger('click');
    await flushPromises();

    // The second question is now the head. Submit it untouched: it must
    // send ITS OWN default ('x'), never 'b' and never null. The input
    // subtree is keyed on request_id so the child remounts and re-emits.
    expect(wrapper.find('[data-testid="ask-dialog-question"]').text()).toContain('Second?');
    await wrapper.find('[data-testid="ask-dialog-submit"]').trigger('click');
    await flushPromises();

    expect(submitSpy).toHaveBeenCalledTimes(2);
    expect(submitSpy.mock.calls[0][1]).toBe('b');
    expect(submitSpy.mock.calls[1][1]).toBe('x');
    wrapper.unmount();
  });

  it('an already-resolved ask clears instead of trapping the user behind a modal', async () => {
    submitSpy.mockRejectedValue(
      new Error('elicit: unknown or already-resolved request ID'),
    );
    const wrapper = mountDialog();
    await flushPromises();
    rt.emit('elicit:pending', {
      request_id: 'r-stale',
      question: 'Timed out?',
      kind: 'radio',
      options: [{ value: 'y', label: 'Yes' }],
    });
    await flushPromises();

    await wrapper.find('[data-testid="ask-dialog-cancel"]').trigger('click');
    await flushPromises();

    // The overlay is app-global: a stale ask that cannot be dismissed
    // locks the user out of the entire app.
    expect(wrapper.find('[data-testid="ask-user-question-dialog"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it('ignores deferred-mode asks — nothing is blocked on them', async () => {
    const wrapper = mountDialog();
    await flushPromises();
    rt.emit('elicit:pending', {
      request_id: 'r-deferred',
      question: 'Whenever you get a moment',
      kind: 'text',
      mode: 'deferred',
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="ask-user-question-dialog"]').exists()).toBe(false);
    wrapper.unmount();
  });
});
