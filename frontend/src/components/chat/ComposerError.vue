<script setup lang="ts">
/**
 * ComposerError — inline error banner shown above the chat composer when
 * a message fails due to an ErrUnsupportedFeature (knob policy rejection)
 * or other pre-flight LLM error that needs a user-actionable hint.
 *
 * The component is displayed when:
 *   - A /effort setting is incompatible with the current model.
 *   - Any other ErrUnsupportedFeature the KnobPolicy raises.
 *
 * It renders above the ChatInput area and is dismissed when the user
 * clears it explicitly or when a new session message succeeds.
 *
 * (provider-implementation-uniformity-01KQ8V4F WP08)
 */

import { computed } from 'vue';
import {
  parseUnsupportedFeatureError,
  friendlyUnsupportedFeatureError,
} from '@/lib/errors';

const props = defineProps<{
  /**
   * The raw error string from the backend. The component inspects
   * this to decide how to render the message.
   * Pass null / undefined to hide the banner.
   */
  error: string | null | undefined;
}>();

const emit = defineEmits<{
  dismiss: [];
}>();

/** True when the error is a recognised ErrUnsupportedFeature. */
const parsed = computed(() =>
  props.error ? parseUnsupportedFeatureError(props.error) : null,
);

/** Friendly message to display. Falls back to raw string for unknown errors. */
const message = computed<string | null>(() => {
  if (!props.error) return null;
  const friendly = friendlyUnsupportedFeatureError(props.error);
  if (friendly) return friendly;
  // Not an ErrUnsupportedFeature — show the raw string as a generic error.
  return props.error;
});

/** True when we have something to show. */
const visible = computed(() => !!message.value);

/** Feature label for the pill, e.g. "reasoning.openai_effort". */
const featureLabel = computed<string | null>(() => {
  return parsed.value?.feature ?? null;
});

function onDismiss() {
  emit('dismiss');
}
</script>

<template>
  <div
    v-if="visible"
    role="alert"
    data-testid="composer-error"
    class="flex items-start gap-2 rounded-md border border-signal-danger/30 bg-signal-danger/10 px-3 py-2 text-sm text-signal-danger"
  >
    <span aria-hidden="true" class="mt-0.5 shrink-0 text-[14px]">⚠</span>
    <div class="flex-1 min-w-0">
      <p class="leading-snug" data-testid="composer-error-message">
        {{ message }}
      </p>
      <p
        v-if="featureLabel"
        class="mt-0.5 font-mono text-[11px] text-signal-danger/70"
        data-testid="composer-error-feature"
      >
        feature: {{ featureLabel }}
      </p>
    </div>
    <button
      type="button"
      class="shrink-0 text-signal-danger/70 hover:text-signal-danger transition-colors leading-none"
      aria-label="Dismiss error"
      data-testid="composer-error-dismiss"
      @click="onDismiss"
    >
      ✕
    </button>
  </div>
</template>
