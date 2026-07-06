<script setup lang="ts">
/**
 * AgentProfileEditor — modal form for creating / editing a sub-agent profile.
 *
 * Shows a structured form for all profile fields. Read-only mode (bundled
 * profiles) disables all inputs and the save button.
 *
 * branch-subagent-interactive-01KZNP3B WP05.
 */
import { ref, watch } from 'vue';
import type { AgentProfile, AutonomyTier, SubagentMergePolicy } from '@/lib/types';

const props = defineProps<{
  profile: AgentProfile;
  readOnly?: boolean;
}>();

const emit = defineEmits<{
  save: [profile: AgentProfile];
  close: [];
}>();

// Editable local copy of the profile.
const local = ref<AgentProfile>({ ...props.profile });

watch(
  () => props.profile,
  (p) => {
    local.value = { ...p };
  },
);

const validationError = ref<string | null>(null);

function validateAndSave() {
  if (!local.value.id.trim()) {
    validationError.value = 'ID is required.';
    return;
  }
  if (!/^[a-z0-9-]+$/.test(local.value.id.trim())) {
    validationError.value = 'ID must be lowercase alphanumeric + hyphens only.';
    return;
  }
  if (!local.value.name.trim()) {
    validationError.value = 'Name is required.';
    return;
  }
  validationError.value = null;
  emit('save', { ...local.value });
}

const tierOptions: AutonomyTier[] = ['strict', 'cautious', 'default', 'bold', 'autonomous'];
const mergePolicyOptions: { value: SubagentMergePolicy; label: string }[] = [
  { value: 'auto', label: 'Auto (merge on completion)' },
  { value: 'confirm', label: 'Confirm (ask before merging)' },
  { value: 'manual', label: 'Manual (__subagent_merge only)' },
];

function toolListToString(tools: string[] | undefined): string {
  return (tools ?? []).join(', ');
}

function stringToToolList(s: string): string[] {
  return s
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean);
}
</script>

<template>
  <!-- Modal overlay -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
    role="dialog"
    aria-modal="true"
    aria-label="Edit sub-agent profile"
    data-testid="profile-editor-modal"
    @click.self="emit('close')"
  >
    <div class="relative w-full max-w-2xl rounded-lg bg-surface-0 shadow-xl">
      <!-- Header -->
      <div class="flex items-center justify-between border-b border-border-muted px-6 py-4">
        <h2 class="font-ui text-base font-semibold text-ink">
          {{ readOnly ? 'View Profile' : profile?.id ? 'Edit Profile' : 'New Profile' }}
        </h2>
        <button
          type="button"
          class="font-ui text-sm text-ink-muted hover:text-ink"
          data-testid="profile-editor-close"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>

      <!-- Scrollable form body -->
      <div class="max-h-[70vh] overflow-y-auto px-6 py-4">
        <div v-if="readOnly" class="mb-4 rounded bg-accent/10 px-3 py-2 font-ui text-xs text-accent">
          This is a built-in profile. Duplicate it to create an editable copy.
        </div>

        <div class="flex flex-col gap-4">
          <!-- ID -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-id">
              ID <span class="text-status-error">*</span>
            </label>
            <input
              id="profile-id"
              v-model="local.id"
              type="text"
              placeholder="my-custom-agent"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-id-input"
            />
            <p class="mt-1 font-ui text-[11px] text-ink-subtle">
              Lowercase alphanumeric + hyphens only. Unique per profile set.
            </p>
          </div>

          <!-- Name -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-name">
              Name <span class="text-status-error">*</span>
            </label>
            <input
              id="profile-name"
              v-model="local.name"
              type="text"
              placeholder="My Custom Agent"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-name-input"
            />
          </div>

          <!-- Description -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-description">
              Description
            </label>
            <input
              id="profile-description"
              v-model="local.description"
              type="text"
              placeholder="One-sentence summary of what this profile does."
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-description-input"
            />
          </div>

          <!-- Model -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-model">
              Model
            </label>
            <input
              id="profile-model"
              v-model="local.model"
              type="text"
              placeholder="anthropic/claude-haiku-4-5 (empty = inherit parent)"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-model-input"
            />
          </div>

          <!-- Autonomy Tier -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-tier">
              Autonomy Tier
            </label>
            <select
              id="profile-tier"
              v-model="local.autonomyTier"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-tier-select"
            >
              <option v-for="tier in tierOptions" :key="tier" :value="tier">{{ tier }}</option>
            </select>
          </div>

          <!-- Merge Policy -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-merge">
              Merge Policy
            </label>
            <select
              id="profile-merge"
              v-model="local.mergePolicy"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-merge-select"
            >
              <option
                v-for="opt in mergePolicyOptions"
                :key="opt.value"
                :value="opt.value"
              >
                {{ opt.label }}
              </option>
            </select>
          </div>

          <!-- Allowed Tools -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-allowed">
              Allowed Tools
            </label>
            <input
              id="profile-allowed"
              :value="toolListToString(local.allowedTools)"
              type="text"
              placeholder="kenaz__read_file, kenaz__grep (empty = all tools)"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-allowed-input"
              @change="
                (e) => {
                  local.allowedTools = stringToToolList((e.target as HTMLInputElement).value);
                }
              "
            />
            <p class="mt-1 font-ui text-[11px] text-ink-subtle">Comma-separated tool IDs. Empty = inherit parent session.</p>
          </div>

          <!-- Denied Tools -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-denied">
              Denied Tools
            </label>
            <input
              id="profile-denied"
              :value="toolListToString(local.deniedTools)"
              type="text"
              placeholder="kenaz__bash, kenaz__write_file"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-denied-input"
              @change="
                (e) => {
                  local.deniedTools = stringToToolList((e.target as HTMLInputElement).value);
                }
              "
            />
            <p class="mt-1 font-ui text-[11px] text-ink-subtle">Always denied, even if in allowed list. Deny wins.</p>
          </div>

          <!-- Budget Tokens -->
          <div class="flex gap-4">
            <div class="flex-1">
              <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-budget-tokens">
                Token Budget
              </label>
              <input
                id="profile-budget-tokens"
                v-model.number="local.budgetTokens"
                type="number"
                min="0"
                placeholder="50000"
                :disabled="readOnly"
                class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
                data-testid="profile-budget-tokens-input"
              />
            </div>
            <div class="flex-1">
              <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-budget-time">
                Time Budget (s)
              </label>
              <input
                id="profile-budget-time"
                v-model.number="local.budgetTimeS"
                type="number"
                min="0"
                placeholder="300"
                :disabled="readOnly"
                class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
                data-testid="profile-budget-time-input"
              />
            </div>
          </div>

          <!-- System Prompt Override -->
          <div>
            <label class="mb-1 block font-ui text-xs font-medium text-ink" for="profile-sysprompt">
              System Prompt Override
            </label>
            <textarea
              id="profile-sysprompt"
              v-model="local.systemPromptOverride"
              rows="5"
              placeholder="(empty = inherit parent system prompt)"
              :disabled="readOnly"
              class="w-full rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-mono text-xs text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
              data-testid="profile-sysprompt-input"
            />
          </div>

          <!-- Validation error -->
          <div
            v-if="validationError"
            class="rounded border border-status-error/30 bg-status-error/5 px-3 py-2 font-ui text-sm text-status-error"
            role="alert"
            data-testid="profile-editor-error"
          >
            {{ validationError }}
          </div>
        </div>
      </div>

      <!-- Footer actions -->
      <div
        class="flex justify-end gap-3 border-t border-border-muted px-6 py-3"
      >
        <button
          type="button"
          class="rounded border border-border-muted px-4 py-1.5 font-ui text-sm text-ink hover:bg-surface-1"
          data-testid="profile-editor-cancel"
          @click="emit('close')"
        >
          {{ readOnly ? 'Close' : 'Cancel' }}
        </button>
        <button
          v-if="!readOnly"
          type="button"
          class="rounded bg-accent px-4 py-1.5 font-ui text-sm text-white hover:bg-accent/90 disabled:opacity-50"
          data-testid="profile-editor-save"
          @click="validateAndSave"
        >
          Save
        </button>
      </div>
    </div>
  </div>
</template>
