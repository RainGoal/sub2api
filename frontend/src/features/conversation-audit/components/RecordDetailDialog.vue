<template>
  <BaseDialog :show="show" :title="t('admin.conversationAudit.detail.title')" width="extra-wide" :close-on-click-outside="true" @close="$emit('close')">
    <div v-if="loading" class="flex min-h-64 items-center justify-center">
      <div class="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-b-primary-600 dark:border-dark-700 dark:border-b-primary-400"></div>
    </div>
    <div v-else-if="detail" class="space-y-5">
      <section class="border-b border-gray-200 pb-5 dark:border-dark-700">
        <div class="flex flex-wrap items-center gap-2">
          <span :class="outcomeClass(detail.metadata.outcome_status)">{{ outcomeLabel(detail.metadata.outcome_status) }}</span>
          <span class="font-mono text-xs text-gray-500 dark:text-dark-400">{{ detail.metadata.protocol }}</span>
          <span v-if="detail.metadata.http_status" class="font-mono text-xs text-gray-500 dark:text-dark-400">HTTP {{ detail.metadata.http_status }}</span>
        </div>
        <div class="mt-3 grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <MetadataField :label="t('admin.conversationAudit.detail.createdAt')" :value="formatTime(detail.metadata.created_at)" />
          <MetadataField :label="t('admin.conversationAudit.filters.requestId')" :value="detail.metadata.request_id" mono />
          <MetadataField :label="t('admin.conversationAudit.filters.sessionId')" :value="detail.metadata.session_id || '—'" mono />
          <MetadataField :label="t('admin.conversationAudit.detail.endpoint')" :value="detail.metadata.inbound_endpoint" mono />
          <MetadataField :label="t('admin.conversationAudit.detail.user')" :value="`${detail.metadata.user_name || '—'} (#${detail.metadata.user_id})`" />
          <MetadataField :label="t('admin.conversationAudit.detail.key')" :value="`${detail.metadata.api_key_name || '—'} (#${detail.metadata.api_key_id})`" />
          <MetadataField :label="t('admin.conversationAudit.detail.group')" :value="detail.metadata.group_name || (detail.metadata.group_id ? `#${detail.metadata.group_id}` : '—')" />
          <MetadataField :label="t('admin.conversationAudit.detail.account')" :value="detail.metadata.account_name || (detail.metadata.account_id ? `#${detail.metadata.account_id}` : '—')" />
          <MetadataField :label="t('admin.conversationAudit.detail.model')" :value="detail.metadata.effective_model || detail.metadata.requested_model || '—'" mono />
          <MetadataField :label="t('admin.conversationAudit.detail.capture')" :value="captureLabel(detail.metadata.capture_status)" />
          <MetadataField :label="t('admin.conversationAudit.detail.requestStorage')" :value="storageLabel('request')" mono />
          <MetadataField :label="t('admin.conversationAudit.detail.responseStorage')" :value="storageLabel('response')" mono />
        </div>
        <div v-if="detail.metadata.error_code || detail.metadata.degraded_reason" class="mt-4 border-l-2 border-red-400 pl-3 font-mono text-xs text-red-600 dark:text-red-300">
          {{ detail.metadata.error_code || detail.metadata.degraded_reason }}
        </div>
      </section>

      <div class="tabs inline-flex" role="tablist" :aria-label="t('admin.conversationAudit.detail.title')">
        <button type="button" role="tab" class="tab" :class="{ 'tab-active': side === 'request' }" :aria-selected="side === 'request'" @click="side = 'request'">
          {{ t('admin.conversationAudit.detail.request') }}
        </button>
        <button type="button" role="tab" class="tab" :class="{ 'tab-active': side === 'response' }" :aria-selected="side === 'response'" @click="side = 'response'">
          {{ t('admin.conversationAudit.detail.response') }}
        </button>
      </div>

      <section v-if="activePayload.available && activePayload.payload" class="space-y-3">
        <article v-for="(message, index) in activePayload.payload.messages" :key="`${message.role}-${index}`" class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
          <header class="border-b border-gray-200 bg-gray-50 px-4 py-2 font-mono text-xs font-semibold uppercase text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300">
            {{ message.role }}
          </header>
          <div class="space-y-3 bg-white p-4 dark:bg-dark-900">
            <pre v-for="(item, itemIndex) in message.content" :key="itemIndex" class="whitespace-pre-wrap break-words font-mono text-xs leading-6 text-gray-700 dark:text-dark-200">{{ contentText(item) }}</pre>
          </div>
        </article>
        <div v-if="activePayload.payload.error" class="rounded-lg border border-red-200 bg-red-50 p-4 font-mono text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300">
          {{ activePayload.payload.error.code }}: {{ activePayload.payload.error.message }}
        </div>
      </section>
      <div v-else class="rounded-lg border border-dashed border-gray-300 px-5 py-12 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400">
        {{ payloadUnavailableLabel }}
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { ConversationAuditContentItem, ConversationAuditRecordDetail } from '../types'
import { formatBytes } from '../viewModel'

const props = defineProps<{ show: boolean; detail: ConversationAuditRecordDetail | null; loading: boolean }>()
defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const side = ref<'request' | 'response'>('request')
watch(() => props.show, (show) => { if (show) side.value = 'request' })

const MetadataField = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true }, mono: { type: Boolean, default: false } },
  setup(fieldProps) {
    return () => h('div', { class: 'min-w-0' }, [
      h('div', { class: 'text-xs text-gray-400 dark:text-dark-500' }, fieldProps.label),
      h('div', { class: ['mt-1 break-all text-gray-800 dark:text-dark-100', fieldProps.mono ? 'font-mono text-xs' : 'font-medium'] }, fieldProps.value),
    ])
  },
})

const activePayload = computed(() => props.detail?.[side.value] || { available: false })
const payloadUnavailableLabel = computed(() => {
  const code = activePayload.value.error_code
  return code ? t(`admin.conversationAudit.errors.${code}`) : t('admin.conversationAudit.detail.unavailable')
})

function contentText(item: ConversationAuditContentItem): string {
  if (item.type === 'media_omitted') {
    return t('admin.conversationAudit.detail.mediaOmitted', { type: item.media_type || 'media', bytes: formatBytes(item.encoded_bytes || item.omitted_bytes || 0) })
  }
  if (item.text) return item.text
  if (item.content) return item.content
  if (item.name || item.arguments) return `${item.name || item.type}\n${item.arguments || ''}`.trim()
  if (item.url) return item.url
  return JSON.stringify(item, null, 2)
}
function formatTime(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'long' }).format(new Date(value))
}
function outcomeLabel(value?: string): string { return t(`admin.conversationAudit.outcome.${value || 'unknown'}`) }
function captureLabel(value: string): string { return t(`admin.conversationAudit.capture.${value}`) }
function outcomeClass(value?: string): string {
  const base = 'inline-flex rounded px-2 py-0.5 text-xs font-semibold '
  if (value === 'completed') return base + 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300'
  if (value === 'partial' || value === 'timeout') return base + 'bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300'
  return base + 'bg-red-100 text-red-700 dark:bg-red-950/50 dark:text-red-300'
}
function storageLabel(payloadSide: 'request' | 'response'): string {
  if (!props.detail) return '—'
  const metadata = props.detail.metadata
  const original = metadata[`${payloadSide}_original_bytes`]
  const stored = metadata[`${payloadSide}_stored_bytes`]
  return `${formatBytes(stored)} / ${formatBytes(original)}`
}
</script>
