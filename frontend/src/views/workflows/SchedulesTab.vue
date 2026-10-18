<script setup lang="ts">
/**
 * SchedulesTab — Workflows › Schedules. nav-ia-sweep-01DOGF0F WP04 (FR-3).
 *
 * One home for "things that run on a timer": workflow cron schedules
 * (WorkflowSchedulesSection, formerly Settings › Authoring › Workflows'
 * schedule modal) and scheduled chats (ScheduledChatsPanel, formerly
 * Settings › Runtime › Scheduled Chats). Run history for both stays on the
 * Runs tab.
 */
import WorkflowSchedulesSection from './WorkflowSchedulesSection.vue';
import ScheduledChatsPanel from './scheduledchat/ScheduledChatsPanel.vue';
import type { WorkflowsClient } from '@/lib/workflowsClient';
import type { ScheduledChatClient } from '@/lib/scheduledChatClient';

defineProps<{
  client: WorkflowsClient;
  chatClient: ScheduledChatClient;
}>();
</script>

<template>
  <div class="space-y-8" data-testid="workflows-schedules-tab">
    <WorkflowSchedulesSection :client="client" />
    <ScheduledChatsPanel :client="chatClient" />
  </div>
</template>
