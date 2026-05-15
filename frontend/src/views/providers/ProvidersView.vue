<script setup lang="ts">
/**
 * ProvidersView — /providers route. Renders the merged bundle + personal
 * provider list, lets the user add a personal provider via the AddProvider
 * drawer, and runs TestProvider inline on the new entry to surface
 * credential validity.
 *
 * The drawer is implemented inline rather than as a separate Drawer
 * primitive: shadcn-vue's drawer pulls in Radix-Vue's dialog primitive
 * which the chassis already vendors, but for the providers-ui mission a
 * plain right-anchored panel keeps the bundle-size budget intact.
 */

import { computed, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { AddProviderInput, Provider, TestResult } from '@/lib/types';
import AddProviderForm from './AddProviderForm.vue';
import ProviderRow from './ProviderRow.vue';
import LocalRuntimesSection from './LocalRuntimesSection.vue';

const client = useHarnessClient();

const providers = ref<readonly Provider[]>([]);
const loading = ref(false);
const drawerOpen = ref(false);
const editingProvider = ref<Provider | null>(null);
const testingId = ref<string | null>(null);
const lastTestResult = ref<{ id: string; result: TestResult } | null>(null);
const errorMessage = ref<string>('');

async function refresh(): Promise<void> {
  loading.value = true;
  try {
    providers.value = await client.llm.listProviders();
    errorMessage.value = '';
  } catch (e) {
    errorMessage.value =
      e instanceof Error ? e.message : 'Failed to load providers.';
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void refresh();
});

function openDrawer(): void {
  editingProvider.value = null;
  drawerOpen.value = true;
}

function openEditDrawer(p: Provider): void {
  editingProvider.value = p;
  drawerOpen.value = true;
}

function closeDrawer(): void {
  drawerOpen.value = false;
  editingProvider.value = null;
}

const editingForForm = computed(() => {
  const p = editingProvider.value;
  if (!p) return null;
  return {
    id: p.id,
    name: p.name,
    kind: (p.kind ?? 'anthropic') as Provider['kind'] extends infer K ? NonNullable<K> : never,
    model: p.model,
    region: p.region,
    credKind: (p.cred?.kind ?? 'keychain') as
      | 'keychain'
      | 'aws_profile'
      | 'env'
      | 'file',
    credLocator: p.cred?.locator ?? '',
  };
});

async function onAddSubmit(input: AddProviderInput): Promise<void> {
  errorMessage.value = '';
  const editing = editingProvider.value;
  try {
    if (editing) {
      await client.llm.updateProvider(input);
    } else {
      await client.llm.addProvider(input);
    }
    drawerOpen.value = false;
    editingProvider.value = null;
    await refresh();
    // Run TestProvider against the just-saved entry and surface the
    // outcome inline.
    testingId.value = input.id;
    const result = await client.llm.testProvider(input.id);
    lastTestResult.value = { id: input.id, result };
    await refresh();
  } catch (e) {
    errorMessage.value =
      e instanceof Error
        ? e.message
        : editing
          ? 'Failed to update provider.'
          : 'Failed to add provider.';
  } finally {
    testingId.value = null;
  }
}

async function onTest(id: string): Promise<void> {
  errorMessage.value = '';
  testingId.value = id;
  try {
    const result = await client.llm.testProvider(id);
    lastTestResult.value = { id, result };
    await refresh();
  } catch (e) {
    errorMessage.value =
      e instanceof Error ? e.message : 'Failed to test provider.';
  } finally {
    testingId.value = null;
  }
}

async function onRemove(id: string): Promise<void> {
  errorMessage.value = '';
  try {
    await client.llm.removeProvider(id);
    if (lastTestResult.value?.id === id) lastTestResult.value = null;
    await refresh();
  } catch (e) {
    errorMessage.value =
      e instanceof Error ? e.message : 'Failed to remove provider.';
  }
}

const inlineTestMessage = computed(() => {
  if (!lastTestResult.value) return '';
  const { id, result } = lastTestResult.value;
  const prefix = result.success ? 'Validated' : 'Failed';
  const latency = result.latency_ms
    ? ` (${result.latency_ms}ms)`
    : '';
  const message = result.message ? ` — ${result.message}` : '';
  return `${prefix} ${id}${latency}${message}`;
});

const inlineTestClass = computed(() =>
  lastTestResult.value?.result.success
    ? 'text-signal-ok'
    : 'text-signal-danger',
);
</script>

<template>
  <div>
    <CanvasHead
      number="04"
      section="PROVIDERS"
      title="Configured LLM providers"
      subtitle="Bundle providers ship with a signed bundle. Personal providers live in your local providers.json — credentials always behind the OS keychain."
    >
      <template #trailing>
        <Button
          variant="accent"
          :data-testid="'open-add-provider'"
          @click="openDrawer"
        >
          Add provider
        </Button>
      </template>
    </CanvasHead>
    <SettingsTabs />

    <div class="px-6 py-4">
      <!-- Local runtimes detection cards (absent when feature flag off) -->
      <LocalRuntimesSection :on-provider-added="refresh" />

      <div
        v-if="errorMessage"
        class="mb-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 text-sm text-signal-danger"
        role="alert"
      >
        {{ errorMessage }}
      </div>

      <div
        v-if="lastTestResult"
        class="mb-3 rounded-sm border border-border-muted bg-surface-1 px-3 py-2 text-xs font-ui"
        :class="inlineTestClass"
        role="status"
        :data-testid="'last-test-result'"
      >
        {{ inlineTestMessage }}
      </div>

      <table
        class="w-full border-collapse text-left"
        :data-testid="'providers-table'"
      >
        <thead>
          <tr
            class="border-b border-border-muted text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            <th class="px-4 py-2 font-ui font-medium">Name</th>
            <th class="px-4 py-2 font-ui font-medium">Source</th>
            <th class="px-4 py-2 font-ui font-medium">Model</th>
            <th class="px-4 py-2 font-ui font-medium">Status</th>
            <th class="px-4 py-2 font-ui font-medium text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <ProviderRow
            v-for="p in providers"
            :key="p.id"
            :provider="p"
            :testing="testingId === p.id"
            @test="onTest"
            @edit="openEditDrawer"
            @remove="onRemove"
          />
          <tr v-if="!loading && providers.length === 0">
            <td
              colspan="5"
              class="px-4 py-6 text-center text-sm text-ink-muted"
            >
              No providers configured. Click "Add provider" to add one.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Right-anchored drawer for the add form -->
    <div
      v-if="drawerOpen"
      class="fixed inset-0 z-50 flex"
      role="dialog"
      aria-modal="true"
      aria-label="Add provider"
    >
      <div
        class="flex-1 bg-modal-overlay"
        :data-testid="'drawer-scrim'"
        @click="closeDrawer"
      />
      <div
        class="w-[420px] max-w-full overflow-y-auto border-l border-border-muted bg-surface-0 shadow-lg"
        :data-testid="'add-provider-drawer'"
      >
        <header
          class="flex items-center justify-between border-b border-border-muted px-6 py-4"
        >
          <div>
            <div
              class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
            >
              {{ editingProvider ? 'EDIT PROVIDER' : 'ADD PROVIDER' }}
            </div>
            <h2 class="mt-1 font-ui text-lg font-semibold text-ink">
              {{
                editingProvider
                  ? `Edit ${editingProvider.name || editingProvider.id}`
                  : 'New personal provider'
              }}
            </h2>
          </div>
          <Button variant="ghost" @click="closeDrawer">Close</Button>
        </header>
        <AddProviderForm
          :editing="editingForForm"
          @submit="onAddSubmit"
          @cancel="closeDrawer"
        />
      </div>
    </div>
  </div>
</template>
