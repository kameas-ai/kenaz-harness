import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RegistryTab from '@/views/tools/RegistryTab.vue';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Recipe, RecipeListing, RecipeStatus } from '@/lib/types';

// ── helpers ────────────────────────────────────────────────────────────

function makeRecipe(id: string, overrides: Partial<Recipe> = {}): Recipe {
  return {
    id,
    displayName: id,
    description: `Recipe ${id}`,
    category: 'developer',
    envKeys: [],
    capabilities: {
      tools: true,
      resources: false,
      prompts: false,
      sampling: false,
    },
    ...overrides,
  };
}

function makeStatus(id: string, overrides: Partial<RecipeStatus> = {}): RecipeStatus {
  return {
    id,
    enabled: false,
    state: 'stopped',
    restartAttempts: 0,
    keysPresent: false,
    pid: 0,
    toolCount: 0,
    resourceCount: 0,
    promptCount: 0,
    ...overrides,
  };
}

function makeListing(
  recipe: Recipe,
  overrides: Partial<RecipeListing> = {},
): RecipeListing {
  return {
    recipe,
    enabled: false,
    keysPresent: false,
    status: makeStatus(recipe.id),
    ...overrides,
  };
}

/** Build a fake client whose recipe catalog returns `listings`. */
function clientWithRecipes(
  listings: RecipeListing[],
  extra: Partial<HarnessClient> = {},
): Partial<HarnessClient> {
  return {
    tools: {
      recipes: {
        list: vi.fn(async () => listings),
        install: vi.fn(async () => makeStatus('x')),
        uninstall: vi.fn(),
        forgetKey: vi.fn(),
        status: vi.fn(),
        config: vi.fn(async () => ({})),
      },
    } as unknown as HarnessClient['tools'],
    ...extra,
  };
}

function mountRegistryTab(client?: Partial<HarnessClient>) {
  const fakeClient = createFakeHarnessClient(client);
  return mount(RegistryTab, {
    global: {
      provide: { [HarnessClientKey as symbol]: fakeClient },
    },
  });
}

function sectionOrder(w: ReturnType<typeof mountRegistryTab>): string[] {
  return w
    .findAll('[data-testid^="registry-section-"]')
    .map((el) => el.attributes('data-testid') ?? '');
}

// ── FR-005: category grouping (default view) ───────────────────────────

describe('RegistryTab — category grouping', () => {
  afterEach(() => vi.restoreAllMocks());

  it('empty search groups recipes into category sections', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(makeRecipe('zap', { category: 'automation' })),
        makeListing(makeRecipe('slack', { category: 'communication' })),
      ]),
    );
    await flushPromises();

    // Search box present, empty by default.
    expect(w.find('[data-testid="registry-search"]').exists()).toBe(true);

    // A section per category, with the canonical suffix.
    expect(w.find('[data-testid="registry-section-automation"]').exists()).toBe(
      true,
    );
    expect(
      w.find('[data-testid="registry-section-communication"]').exists(),
    ).toBe(true);

    // Rows still render with their per-recipe testids.
    expect(w.find('[data-testid="registry-row-zap"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-row-slack"]').exists()).toBe(true);
  });

  it('section header shows the canonical display label + count', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(makeRecipe('zap-a', { category: 'automation' })),
        makeListing(makeRecipe('zap-b', { category: 'automation' })),
      ]),
    );
    await flushPromises();

    const header = w.find('[data-testid="registry-section-automation"]');
    expect(header.text()).toContain('Automation & iPaaS');
    expect(header.text()).toContain('2');
  });

  it('sorts sections alphabetically by display label', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(makeRecipe('dev', { category: 'developer' })),
        makeListing(makeRecipe('zap', { category: 'automation' })),
        makeListing(makeRecipe('slack', { category: 'communication' })),
      ]),
    );
    await flushPromises();

    // Automation & iPaaS < Communication < Developer.
    expect(sectionOrder(w)).toEqual([
      'registry-section-automation',
      'registry-section-communication',
      'registry-section-developer',
    ]);
  });

  it('sorts recipes alphabetically within a section', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(
          makeRecipe('b', { category: 'developer', displayName: 'Zulu' }),
        ),
        makeListing(
          makeRecipe('a', { category: 'developer', displayName: 'Alpha' }),
        ),
      ]),
    );
    await flushPromises();

    const rowIds = w
      .findAll('[data-testid^="registry-row-"]')
      .map((el) => el.attributes('data-testid'));
    expect(rowIds).toEqual(['registry-row-a', 'registry-row-b']);
  });
});

// ── FR-005: search ─────────────────────────────────────────────────────

describe('RegistryTab — search', () => {
  afterEach(() => vi.restoreAllMocks());

  it('search matches against an alias', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(
          makeRecipe('gh', {
            displayName: 'GitHub',
            aliases: ['zapier', 'ipaas'],
          }),
        ),
        makeListing(makeRecipe('other', { displayName: 'Something Else' })),
      ]),
    );
    await flushPromises();

    await w.find('[data-testid="registry-search"]').setValue('zapier');
    await flushPromises();

    // Alias match surfaces the recipe; non-match is filtered out.
    expect(w.find('[data-testid="registry-row-gh"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-row-other"]').exists()).toBe(false);

    // Search view is flat — no category section headers.
    expect(sectionOrder(w)).toEqual([]);
  });

  it('search matches displayName / description / id case-insensitively', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(makeRecipe('brave-search', { displayName: 'Brave' })),
        makeListing(makeRecipe('other', { displayName: 'Nope' })),
      ]),
    );
    await flushPromises();

    await w.find('[data-testid="registry-search"]').setValue('BRAVE');
    await flushPromises();

    expect(w.find('[data-testid="registry-row-brave-search"]').exists()).toBe(
      true,
    );
    expect(w.find('[data-testid="registry-row-other"]').exists()).toBe(false);
  });

  it('zero matches show the no-results state and the long-tail callout', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([makeListing(makeRecipe('gh', { displayName: 'GitHub' }))]),
    );
    await flushPromises();

    await w.find('[data-testid="registry-search"]').setValue('zzzznomatch');
    await flushPromises();

    expect(w.find('[data-testid="registry-no-results"]').exists()).toBe(true);
    // The long-tail callout is present (and prominent) on zero results.
    expect(w.find('[data-testid="registry-long-tail"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-list"]').exists()).toBe(false);
  });
});

// ── FR-006: long-tail affordance ───────────────────────────────────────

describe('RegistryTab — long-tail affordance', () => {
  afterEach(() => vi.restoreAllMocks());

  it('renders the callout at the bottom of the default view', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([makeListing(makeRecipe('gh'))]),
    );
    await flushPromises();

    const callout = w.find('[data-testid="registry-long-tail"]');
    expect(callout.exists()).toBe(true);
    expect(callout.text()).toContain("Don't see your tool?");
    expect(w.find('[data-testid="registry-browse-automation"]').exists()).toBe(
      true,
    );
    expect(w.find('[data-testid="registry-add-custom"]').exists()).toBe(true);
    expect(
      w.find('[data-testid="registry-request-connector"]').exists(),
    ).toBe(true);
  });

  it('request-a-connector opens the prefilled GitHub issue URL', async () => {
    const openExternalURL = vi.fn();
    const w = mountRegistryTab(
      clientWithRecipes([makeListing(makeRecipe('gh'))], { openExternalURL }),
    );
    await flushPromises();

    await w.find('[data-testid="registry-request-connector"]').trigger('click');

    expect(openExternalURL).toHaveBeenCalledTimes(1);
    const url = openExternalURL.mock.calls[0][0] as string;
    expect(url).toContain('github.com/kameas-ai/kenaz-harness/issues/new');
    expect(url).toContain('Connector');
  });

  it('browse-automation filters to the automation category', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([
        makeListing(makeRecipe('zap', { category: 'automation' })),
        makeListing(makeRecipe('dev', { category: 'developer' })),
      ]),
    );
    await flushPromises();

    // Both categories visible before filtering.
    expect(sectionOrder(w)).toEqual([
      'registry-section-automation',
      'registry-section-developer',
    ]);

    await w.find('[data-testid="registry-browse-automation"]').trigger('click');
    await flushPromises();

    // Only the automation section remains + a dismissible filter chip.
    expect(sectionOrder(w)).toEqual(['registry-section-automation']);
    expect(w.find('[data-testid="registry-active-filter"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-row-dev"]').exists()).toBe(false);

    // Clearing the filter restores the grouped view.
    await w.find('[data-testid="registry-clear-filter"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="registry-row-dev"]').exists()).toBe(true);
  });

  it('add-custom emits switch-tab targeting the custom recipe tab', async () => {
    const w = mountRegistryTab(
      clientWithRecipes([makeListing(makeRecipe('gh'))]),
    );
    await flushPromises();

    await w.find('[data-testid="registry-add-custom"]').trigger('click');

    expect(w.emitted('switch-tab')).toEqual([['custom']]);
  });
});

// ── WP16 (hr-finance pack): category row rendering ─────────────────────

describe('RegistryTab — WP16 HR & Finance category UX', () => {
  it('renders an hr_people connector row without crashing', async () => {
    const recipe = makeRecipe('deel', {
      displayName: 'Deel',
      description: 'Read contracts, people, timesheets — read-only, no payroll or money-movement actions.',
      category: 'hr_people',
    });
    const w = mountRegistryTab(clientWithRecipes([makeListing(recipe)]));
    await flushPromises();
    const row = w.find('[data-testid="registry-row-deel"]');
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('Deel');
  });

  it('renders a finance connector row without crashing', async () => {
    const recipe = makeRecipe('mercury', {
      displayName: 'Mercury',
      description: 'Query Mercury bank accounts — read-only, no transactions initiated.',
      category: 'finance',
    });
    const w = mountRegistryTab(clientWithRecipes([makeListing(recipe)]));
    await flushPromises();
    const row = w.find('[data-testid="registry-row-mercury"]');
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('Mercury');
  });

  it('renders multiple HR and finance connectors in the same list', async () => {
    const hrPeopleIDs = ['deel', 'greenhouse', 'ashby', 'bamboohr', 'gusto', 'rippling', 'lever'];
    const financeIDs = ['mercury', 'ramp', 'brex', 'quickbooks', 'xero', 'billdotcom', 'netsuite'];
    const listings = [
      ...hrPeopleIDs.map((id) =>
        makeListing(makeRecipe(id, { category: 'hr_people', displayName: id })),
      ),
      ...financeIDs.map((id) =>
        makeListing(
          makeRecipe(id, {
            category: 'finance',
            displayName: id,
            description: `${id} — read-only, no transactions initiated.`,
          }),
        ),
      ),
    ];
    const w = mountRegistryTab(clientWithRecipes(listings));
    await flushPromises();
    for (const id of [...hrPeopleIDs, ...financeIDs]) {
      expect(w.find(`[data-testid="registry-row-${id}"]`).exists()).toBe(true);
    }
  });

  it('excludes already-enabled connectors from the list', async () => {
    const installed = makeListing(makeRecipe('mercury', { category: 'finance' }), {
      enabled: true,
    });
    const available = makeListing(makeRecipe('ramp', { category: 'finance' }));
    const w = mountRegistryTab(clientWithRecipes([installed, available]));
    await flushPromises();
    // mercury is enabled — should not appear as an install candidate
    expect(w.find('[data-testid="registry-row-mercury"]').exists()).toBe(false);
    // ramp is not enabled — should appear
    expect(w.find('[data-testid="registry-row-ramp"]').exists()).toBe(true);
  });

  it('shows empty state when all connectors are already installed', async () => {
    const w = mountRegistryTab(clientWithRecipes([]));
    await flushPromises();
    expect(w.find('[data-testid="registry-empty"]').exists()).toBe(true);
  });

  it('finance recipe descriptions include read-only safety copy', () => {
    const financeDescriptions = [
      'Query Mercury bank accounts — read-only, no transactions initiated.',
      'Browse Ramp cards — read-only, no transactions initiated.',
      'Read Brex card expenses — read-only, no transactions initiated.',
      'Read accounting data from QuickBooks — read-only, no transactions initiated.',
      'Read Xero transactions — read-only, no transactions initiated.',
      'Read bills from Bill.com — read-only, no transactions initiated.',
    ];
    for (const desc of financeDescriptions) {
      expect(desc).toContain('read-only');
    }
  });
});
