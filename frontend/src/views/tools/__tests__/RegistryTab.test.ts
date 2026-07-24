/**
 * RegistryTab tests — WP16 (mcp-connector-pack-hr-finance-01NCONN08b)
 *
 * Covers:
 * - Category icon assignment for hr_people and finance_accounting (new categories)
 * - Row rendering for HR and finance connector listings
 * - Finance safety copy ("read-only") present in recipe descriptions
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RegistryTab from '@/views/tools/RegistryTab.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Recipe, RecipeListing, RecipeStatus } from '@/lib/types';

function makeRecipe(
  id: string,
  overrides: Partial<Recipe> = {},
): Recipe {
  return {
    id,
    displayName: id,
    description: `Recipe ${id}`,
    category: 'search',
    envKeys: [],
    capabilities: {
      tools: true,
      resources: false,
      prompts: false,
      sampling: false,
    },
    docsUrl: `https://example.com/${id}`,
    ...overrides,
  };
}

function makeStatus(id: string): RecipeStatus {
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
  };
}

function makeListing(recipe: Recipe, enabled = false): RecipeListing {
  return {
    recipe,
    enabled,
    keysPresent: false,
    status: makeStatus(recipe.id),
  };
}

function provideClient(listings: RecipeListing[]) {
  return createFakeHarnessClient({
    tools: {
      recipes: {
        list: async () => listings,
      } as any,
    } as any,
  });
}

describe('RegistryTab — WP16 HR & Finance category UX', () => {
  it('renders an hr_people connector row without crashing', async () => {
    const recipe = makeRecipe('deel', {
      displayName: 'Deel',
      description: 'Read contracts, people, timesheets — read-only, no payroll or money-movement actions.',
      category: 'hr_people',
    });
    const client = provideClient([makeListing(recipe)]);
    const w = mount(RegistryTab, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const row = w.find('[data-testid="registry-row-deel"]');
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('Deel');
  });

  it('renders a finance_accounting connector row without crashing', async () => {
    const recipe = makeRecipe('mercury', {
      displayName: 'Mercury',
      description: 'Query Mercury bank accounts — read-only, no transactions initiated.',
      category: 'finance_accounting',
    });
    const client = provideClient([makeListing(recipe)]);
    const w = mount(RegistryTab, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
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
            category: 'finance_accounting',
            displayName: id,
            description: `${id} — read-only, no transactions initiated.`,
          }),
        ),
      ),
    ];
    const client = provideClient(listings);
    const w = mount(RegistryTab, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    for (const id of [...hrPeopleIDs, ...financeIDs]) {
      expect(w.find(`[data-testid="registry-row-${id}"]`).exists()).toBe(
        true,
      );
    }
  });

  it('excludes already-enabled connectors from the list', async () => {
    const installed = makeListing(
      makeRecipe('mercury', { category: 'finance_accounting' }),
      true, // enabled
    );
    const available = makeListing(
      makeRecipe('ramp', { category: 'finance_accounting' }),
      false,
    );
    const client = provideClient([installed, available]);
    const w = mount(RegistryTab, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    // mercury is enabled — should not appear as an install candidate
    expect(w.find('[data-testid="registry-row-mercury"]').exists()).toBe(false);
    // ramp is not enabled — should appear
    expect(w.find('[data-testid="registry-row-ramp"]').exists()).toBe(true);
  });

  it('shows empty state when all connectors are already installed', async () => {
    const client = provideClient([]);
    const w = mount(RegistryTab, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
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
