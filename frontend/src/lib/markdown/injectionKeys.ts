/**
 * Vue injection keys shared across the markdown rendering surface.
 * Centralised so App.vue can `provide()` the same key MarkdownBlock
 * `inject()`s.
 */
import { ref, type InjectionKey, type Ref } from 'vue';
import type { MarkdownExtensions } from '@/lib/types';

/**
 * The provider may pass either a plain value (tests) or a Ref
 * (App.vue, so user changes propagate). MarkdownBlock unwraps both.
 */
export const MD_EXTENSIONS_KEY: InjectionKey<MarkdownExtensions | Ref<MarkdownExtensions>> =
  Symbol('mdExtensions');

/**
 * Module-level singleton ref used by App.vue's `provide` and SettingsView's
 * setter so a Settings change propagates to mounted MarkdownBlocks without
 * threading the ref through every parent component.
 */
export const markdownExtensionsRef: Ref<MarkdownExtensions> = ref('all');
