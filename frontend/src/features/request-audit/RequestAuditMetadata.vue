<template>
  <dl class="grid gap-3 border-t border-gray-200 pt-4 text-sm dark:border-dark-700 sm:grid-cols-2">
    <div v-for="field in fields" :key="field.key" class="min-w-0"><dt class="text-xs text-gray-500">{{ t(`admin.requestAudit.${field.key}`) }}</dt><dd class="mt-1 break-all font-mono text-xs">{{ field.value || '—' }}</dd></div>
  </dl>
  <div class="grid gap-2 sm:grid-cols-3">
    <div v-for="part in parts" :key="part" class="rounded border border-gray-200 p-3 dark:border-dark-700">
      <p class="text-xs font-semibold">{{ t(`admin.requestAudit.parts.${part}`) }}</p>
      <template v-if="record.terminal_manifest?.[part]">
        <p class="mt-1 text-xs" :class="record.terminal_manifest[part]?.complete ? 'text-emerald-500' : 'text-amber-500'">{{ t(`admin.requestAudit.states.${record.terminal_manifest[part]?.complete ? 'complete' : 'partial'}`) }}</p>
        <p class="mt-1 font-mono text-xs text-gray-500">{{ record.terminal_manifest[part]?.expected_bytes }} B · {{ t('admin.requestAudit.chunks', { count: record.terminal_manifest[part]?.expected_chunks }) }}</p>
        <p v-if="record.terminal_manifest[part]?.reason" class="mt-1 break-all text-xs text-amber-500">{{ record.terminal_manifest[part]?.reason }}</p>
      </template>
      <p v-else class="mt-1 text-xs text-amber-500">{{ t('admin.requestAudit.manifestUnavailable') }}</p>
    </div>
  </div>
  <p class="text-xs leading-5 text-gray-500">{{ t('admin.requestAudit.manifestHint') }}</p>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RequestAuditRecord } from './types'

const props = defineProps<{ record: RequestAuditRecord }>()
const { t, locale } = useI18n()
const parts = ['headers', 'body', 'trailers'] as const
const fields = computed(() => [
  { key: 'time', value: new Date(props.record.received_at).toLocaleString(locale.value) },
  { key: 'kind', value: t(`admin.requestAudit.kinds.${props.record.kind}`) },
  { key: 'request', value: [props.record.method, props.record.route_pattern].filter(Boolean).join(' ') },
  { key: 'model', value: props.record.model },
  { key: 'protocol', value: props.record.protocol },
  { key: 'result', value: [props.record.http_status, props.record.outcome, props.record.upstream_summary].filter(Boolean).join(' · ') },
  { key: 'duration', value: props.record.duration_ms == null ? '' : `${props.record.duration_ms} ms` },
  { key: 'bytes', value: `${props.record.stored_body_bytes ?? 0} / ${props.record.observed_body_bytes ?? 0} B` },
  { key: 'sessionSource', value: props.record.session_source },
  { key: 'sessionResolution', value: props.record.session_resolution },
  { key: 'clientRequestId', value: props.record.client_request_id },
  { key: 'connectionId', value: props.record.connection_id },
  { key: 'messageSequence', value: props.record.message_seq },
  { key: 'messageType', value: props.record.message_type },
])
</script>
