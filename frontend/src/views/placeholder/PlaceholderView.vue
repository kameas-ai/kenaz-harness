<script setup lang="ts">
import CanvasHead from '@/shell/CanvasHead.vue';

defineProps<{
  number: string;
  section: string;
  title: string;
}>();
</script>

<template>
  <div>
    <CanvasHead
      :number="number"
      :section="section"
      :title="title"
      subtitle="This surface lands in a downstream UI mission consuming the chassis."
    />
    <div class="px-6 py-4 font-ui text-sm text-ink-muted">
      Placeholder surface.
    </div>
  </div>
</template>
