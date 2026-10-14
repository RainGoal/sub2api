<template>
  <dl class="grid grid-cols-2 gap-x-4 gap-y-4 text-sm">
    <div class="col-span-2 min-w-0"><dt class="text-xs text-gray-500">{{ t('admin.requestAudit.metrics.instance') }}</dt><dd class="mt-1 break-all font-mono text-xs">{{ status.instance_id }}</dd></div>
    <div v-for="metric in metrics" :key="metric.key" class="min-w-0"><dt class="text-xs text-gray-500">{{ t(`admin.requestAudit.metrics.${metric.key}`) }}</dt><dd class="mt-1 break-words font-mono text-xs">{{ metric.value }}</dd></div>
  </dl>
  <p class="mt-4 border-t border-gray-200 pt-3 text-xs leading-5 text-gray-500 dark:border-dark-700">{{ t('admin.requestAudit.counterHint') }}</p>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RequestAuditStatus } from './types'

const props = defineProps<{ status: RequestAuditStatus }>()
const { t, locale } = useI18n()
const date = (value?: string) => value ? new Date(value).toLocaleString(locale.value) : '—'
const metrics = computed(() => [
  { key: 'accepted', value: props.status.accepted_requests },
  { key: 'written', value: props.status.written_events },
  { key: 'droppedRequests', value: props.status.dropped_requests },
  { key: 'droppedEvents', value: props.status.dropped_events },
  { key: 'writeFailures', value: props.status.write_failures },
  { key: 'banFailures', value: props.status.ban_check_failures },
  { key: 'queueBytes', value: `${props.status.queue_bytes} B` },
  { key: 'bufferBytes', value: `${props.status.buffer_bytes} B` },
  { key: 'queueEvents', value: props.status.queue_events },
  { key: 'lastWrite', value: date(props.status.last_write_at) },
  { key: 'lastCleanup', value: date(props.status.last_cleanup_at) },
  { key: 'cleanupDeleted', value: props.status.cleanup_deleted },
  { key: 'cleanupFailures', value: props.status.cleanup_failures },
  { key: 'writerBackoff', value: date(props.status.writer_backoff_until) },
])
</script>
