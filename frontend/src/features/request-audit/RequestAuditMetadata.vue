<template>
  <section class="overflow-hidden rounded-md border border-gray-200 bg-gray-50/60 dark:border-dark-700 dark:bg-dark-900/50" data-test="request-summary">
    <div class="space-y-3 p-4">
      <div class="flex flex-wrap items-center gap-2">
        <RequestAuditPlatform :platform="record.group_platform" />
        <span class="text-xs text-gray-500 dark:text-dark-300">{{ kindLabel }}</span>
        <span class="ml-auto inline-flex items-center gap-1.5 text-xs" :class="record.capture_state === 'complete' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
          <span class="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true" />{{ stateLabel }}
        </span>
      </div>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <h4 class="flex min-w-0 flex-1 items-start gap-2 font-mono text-sm font-semibold text-gray-900 dark:text-white sm:text-base">
          <span v-if="record.method" class="shrink-0 rounded bg-primary-500/10 px-1.5 py-0.5 text-xs leading-5 text-primary-600 dark:text-primary-400">{{ record.method }}</span>
          <span class="min-w-0 break-all leading-6">{{ record.route_pattern || kindLabel }}</span>
        </h4>
        <span v-if="record.http_status" class="rounded border px-2 py-1 font-mono text-xs font-semibold" :class="record.http_status >= 400 ? 'border-red-500/25 bg-red-500/10 text-red-600 dark:text-red-400' : 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'">{{ record.http_status }}</span>
      </div>
      <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-dark-300">
        <time :datetime="record.received_at">{{ new Date(record.received_at).toLocaleString(locale) }}</time>
        <span>{{ t('admin.requestAudit.duration') }} <span class="font-mono tabular-nums text-gray-700 dark:text-dark-100">{{ record.duration_ms ?? '—' }} ms</span></span>
        <span>{{ t('admin.requestAudit.bytes') }} <span class="font-mono tabular-nums">{{ formatBytes(record.stored_body_bytes || 0) }} / {{ formatBytes(record.observed_body_bytes || 0) }}</span></span>
      </div>
      <p v-if="record.capture_reason" class="break-words text-xs text-amber-600 dark:text-amber-400">{{ record.capture_reason }}</p>
    </div>
    <dl class="grid gap-4 border-t border-gray-200 p-4 text-sm dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-4">
      <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.user') }}</dt><dd class="mt-1 break-all font-medium text-gray-900 dark:text-white">{{ userName }}</dd><dd v-if="record.user_username && record.user_email" class="mt-1 break-all text-xs text-gray-500 dark:text-dark-300">{{ record.user_email }}</dd></div>
      <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.apiKey') }}</dt><dd class="mt-1 break-all font-medium text-gray-900 dark:text-white">{{ keyName }}</dd></div>
      <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.group') }}</dt><dd class="mt-1 break-all font-medium text-gray-900 dark:text-white">{{ groupName }}</dd></div>
      <div class="min-w-0"><dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.model') }}</dt><dd class="mt-1 break-all font-mono text-xs leading-5 text-gray-700 dark:text-dark-100">{{ record.model || '—' }}</dd></div>
      <div class="min-w-0 border-t border-gray-200 pt-3 dark:border-dark-700 sm:col-span-2 lg:col-span-4"><dt class="text-xs text-gray-500 dark:text-dark-300">{{ t('admin.requestAudit.session') }}</dt><dd class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-dark-100">{{ record.session_id || t('admin.requestAudit.noSession') }}</dd></div>
    </dl>
    <details class="border-t border-gray-200 dark:border-dark-700">
      <summary class="cursor-pointer px-4 py-3 text-xs font-medium text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white">{{ t('admin.requestAudit.technicalDetails') }}</summary>
      <dl class="grid gap-x-6 gap-y-4 px-4 pb-4 sm:grid-cols-2">
        <div v-for="field in fields" :key="field.key" class="min-w-0"><dt class="text-xs text-gray-500 dark:text-dark-300">{{ t(`admin.requestAudit.${field.key}`) }}</dt><dd class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-dark-100">{{ field.value ?? '—' }}</dd></div>
      </dl>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatBytes } from '@/utils/format'
import RequestAuditPlatform from './RequestAuditPlatform.vue'
import { REQUEST_AUDIT_KINDS } from './types'
import type { RequestAuditRecord } from './types'

const props = defineProps<{ record: RequestAuditRecord }>()
const { t, locale } = useI18n()
const userName = computed(() => props.record.user_username || props.record.user_email || (props.record.user_id ? t('admin.requestAudit.userFallback', { id: props.record.user_id }) : t('admin.requestAudit.unidentifiedUser')))
const keyName = computed(() => props.record.api_key_name || (props.record.api_key_id ? t('admin.requestAudit.keyFallback', { id: props.record.api_key_id }) : t('admin.requestAudit.unidentifiedKey')))
const groupName = computed(() => props.record.group_name || (props.record.group_id ? t('admin.requestAudit.groupFallback', { id: props.record.group_id }) : t('admin.requestAudit.unidentifiedGroup')))
const kindLabel = computed(() => REQUEST_AUDIT_KINDS.includes(props.record.kind) ? t(`admin.requestAudit.kinds.${props.record.kind}`) : props.record.kind)
const stateLabel = computed(() => ['complete', 'capturing', 'partial'].includes(props.record.capture_state) ? t(`admin.requestAudit.states.${props.record.capture_state}`) : props.record.capture_state)
const fields = computed(() => [
  { key: 'requestId', value: props.record.id },
  { key: 'userId', value: props.record.user_id },
  { key: 'apiKeyId', value: props.record.api_key_id },
  { key: 'groupId', value: props.record.group_id },
  { key: 'protocol', value: props.record.protocol },
  { key: 'result', value: [props.record.http_status, props.record.outcome, props.record.upstream_summary].filter(Boolean).join(' · ') },
  { key: 'sessionSource', value: props.record.session_source },
  { key: 'sessionResolution', value: props.record.session_resolution },
  { key: 'clientRequestId', value: props.record.client_request_id },
  { key: 'connectionId', value: props.record.connection_id },
  { key: 'messageSequence', value: props.record.message_seq },
  { key: 'messageType', value: props.record.message_type },
])
</script>
